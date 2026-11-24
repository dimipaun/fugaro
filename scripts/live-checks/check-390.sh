#!/usr/bin/env bash
# Check 390 — `fugaro image add` alone leaves the entry in the catalog and
# nothing owed, as ONE script YOU run.
#
# It is AP10 of docs/composite-commands/implementation-plan.md, run by YOU on
# the sandbox installation after the release that ships the composite commands
# is installed. What it measures is the whole of spec rule 1 for one command: a
# management command writes its change AND runs the steps that make it live, so
# no second command is needed (D1561). `fugaro image add` owes three steps — the
# catalog image's build, the redeploy of the checkout's repository (per
# repository, D1574) and the publication of the catalog (D1577) — and this check
# reads, from outside the CLI, that all three landed.
#
# What it reads, in this order:
#
#   1. The checkout, and the installation: the current directory is a git work
#      tree whose `origin` remote names the repository whose jobs the redeploy
#      is for (the repository stage is per repository, D1574), and
#      `fugaro doctor --json` names the project, its GCP project and its region.
#      Held to `--project` where you typed one.
#   2. The entry, before anything is written: `fugaro image ls --json` must not
#      already hold the name this check adds. An entry left by an earlier
#      attempt is removed first (its own `fugaro image rm`), so running this
#      check twice in a row works and a crashed run does not wedge it.
#   3. **The command, which is the fact this check is**: `fugaro image add
#      <entry> --dockerfile <owner/name:path>`, with its stdout and stderr in a
#      scratch directory of this check's own, under a deadline (1800 seconds by
#      default). This BUILDS AN IMAGE, REDEPLOYS THE REPOSITORY'S JOBS AND
#      PUBLISHES THE CATALOG — it is a real change to the installation, and the
#      teardown below is what takes it back.
#   4. The digest the build recorded: `fugaro image ls --json`'s row for the
#      entry, whose `digest` is the `sha256:…` the build promoted `latest` to
#      (the catalog build's own record, gcp.CatalogRecordKey).
#   5. The jobs: `gcloud run jobs list --format=json`, the jobs of the
#      repository the checkout belongs to (matched by the job's own
#      FUGARO_REMOTE and by its `fugaro_repo` label, the way check 193 reads
#      them). Every job carries the deploy's own stamp of the catalog entry its
#      image was deployed from (FUGARO_IMAGE_ENTRY, D1371), and every stamp must
#      name an entry the project's catalog holds: a job left on an image no
#      catalog describes is a job the redeploy missed. The entry this check adds
#      is deliberately not what it looks for — a catalog gives a job an image
#      only through a workflow's `image_name:`, its `needs:`, the project's map
#      or the catalog's default (catalog.JobImage), and an entry a job selects is
#      one `fugaro image rm` refuses to remove (image_catalog.go's
#      catalogPointedAt), so an entry this check adds and removes again is one no
#      job can be running.
#   6. The digest the job's image resolves to now:
#      `gcloud artifacts docker images describe <the job's image>` — the job is
#      deployed with the moving tag (D1152), so the
#      digest it will pull is the registry's, and it is the one that has to
#      equal the digest the catalog records for the entry that job's image came
#      from (the redeploy's own work: it pins the digest the build produced).
#   7. What is still owed: `fugaro apply --status --json`. The add ran its three
#      steps, so nothing may be left — an entry that was written and left owed
#      is exactly the failure this check exists to catch (D1561, D1563).
#   8. The teardown: `fugaro image rm <entry>`, the inverse change, and the
#      record read again. This check leaves the installation as it found it.
#
# **What a PASS means, and what it does not.** A PASS means one command made one
# entry live and nothing was left owed: the entry is in the catalog with the
# digest the build recorded, the repository's jobs are the catalog's own answer
# (each stamp names an entry the catalog holds), the image the job is deployed
# with resolves to the digest the catalog records for the entry its image came
# from, and `fugaro apply --status` says nothing is owed. It does NOT mean a run
# on those jobs would work — no run is launched here — and it does not measure
# the *other* management commands: they are check 391's subject.
#
# What a real run does to your installation: it adds a catalog entry, builds it,
# redeploys the repository's jobs and publishes the catalog, then removes the
# entry and redeploys again. Both are real changes, and the teardown is what
# puts the installation back. Run it on the sandbox installation.
#
# Usage:      bash scripts/live-checks/check-390.sh --dockerfile <owner/name:path>
#                                                 [--entry <catalog entry>]
#                                                 [--project <fugaro project>]
#                                                 [--fugaro-flags '<flags>']
#                                                 [--deadline <seconds>] [--dry-run]
#             --dockerfile is the source of the entry this check adds: a
#             Dockerfile in one of the project's repositories, as
#             `owner/name:path`. It is required, and it is the one flag a coding
#             agent's session may use of `image add` (D1573).
#             --entry is the name of the catalog entry this check owns (default
#             `chk390-catalog`). It is added and removed by this check, so give
#             it a name nothing else uses.
#             --project is the Fugaro project the installation is, and
#             --fugaro-flags is a string of extra flags for every `fugaro` call
#             (e.g. `--config /path/to/project.yaml`). It is split on
#             whitespace, so a value with a space in it cannot be passed.
#             --deadline is how long the add may take, in seconds (default 1800;
#             CHECK390_DEADLINE does the same): it builds an image, so it is
#             minutes and not seconds.
#             --dry-run prints the plan and makes no call at all.
# Needs:      bash, git, mktemp, grep, sed, head, tail, cut, awk, python3, a
#             `fugaro` on PATH with `doctor --json`, `image ls --json`,
#             `image add`, `image rm` and `apply --status --json`, and a
#             `gcloud` with `run jobs list` and `artifacts docker images
#             describe`. A missing tool stops the run with one line naming it.
# Where:      your own machine, in a checkout of a repository the project
#             onboards, against an installation whose project is NOT `belong`:
#             that name is refused before anything is run, in what you typed and
#             in what doctor answers. A coding agent's session is refused too.
# Costs:      one Cloud Build (the entry's image), two repository redeploys (the
#             add's and the teardown's), two policy publications, and one
#             `gcloud run jobs list`. No model call and no run is launched.
# Runs:       by YOU. No agent runs any step of it.
# Output:     progress on stderr, ONE block on stdout headed CHECK 390 RESULT.
#             Exit 0 when the claim held, 1 when it did not (a row says which),
#             and 2 when this run could not tell (a guard stopped it, a tool or
#             a document could not be read, an internal error ended it).
#
# CHECK390_FUGARO names the binary (default `fugaro`), CHECK390_GCLOUD names
# `gcloud`, CHECK390_DEADLINE the default deadline, CHECK390_DRY_RUN makes every
# run a dry one and CHECK390_KEEP_SCRATCH keeps the scratch directory (with the
# add's whole transcript) instead of removing it.
set -euo pipefail
# -E as well, so the ERR trap below is inherited by the functions: without it a
# failing command inside one of them ends the run with no fact recorded at all.
set -E

# No xtrace, whatever the shell it was started from had: this run writes the
# project's catalog and reads the installation's own documents.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
REACHED_END=no
HELPED=no
DRY_RUN=""
USAGE="check-390.sh --dockerfile <owner/name:path> [--entry <catalog entry>] [--project <fugaro project>] [--fugaro-flags '<flags>'] [--deadline <seconds>] [--dry-run]"
die() { say "$*"; ABORT_REASON=$*; exit 2; }

stop() {
  fact aborted FAIL "$1"
  ABORT_REASON=$1
  owner "stopped: $1"
  exit 2
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# unmeasured: a row this check could NOT measure, for a reason that is not a
# finding about the release. The row reads FAIL in the block — it is not a pass
# — and the verdict ends the run as one that could not tell (exit 2).
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE"; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the composite commands"
  fi
  ABORT_REASON="an internal error ended the run (line $line)"
  owner "stopped: $ABORT_REASON"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() { command -v "$1" >/dev/null 2>&1 || die "Check 390 needs '$1' on PATH and it is not there${2:+ ($2)}"; }
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 390 needs bash"
  need mktemp
  need grep
  need sed
  need head
  need tail
  need cut
  need awk "the rows of the run's own transcript are joined into one line with it"
  need python3 "the JSON documents this check reads are parsed with it"
  need git "the checkout's origin remote names the repository whose jobs the redeploy is for"
  need "$FUGARO_BIN" "image add is the command this check measures"
  need "$GCLOUD_BIN" "the deployed job's image and the digest it resolves to are read with it"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk390-mine"
SCRATCH_REMOVED=no
KEEP_SCRATCH=${CHECK390_KEEP_SCRATCH:-}
PRINTED=""
FACTS_CAPTURED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk390.XXXXXXXX") || die "Check 390 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 390 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident -- set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# cleanup runs IN THIS SHELL, never in a subshell: remove_scratch records what
# it managed to do in SCRATCH_REMOVED, and a `$(cleanup)` would throw that
# answer away with the subshell.
cleanup() {
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  / | "${HOME:-}" | "${HOME:-}"/)
    say "refusing to remove a scratch directory of '$SCRATCH'"
    return 0
    ;;
  esac
  if [ ! -f "$SCRATCH/$SCRATCH_MARKER" ]; then
    say "refusing to remove $SCRATCH: it carries no marker from this check"
    return 0
  fi
  if [ -n "$KEEP_SCRATCH" ]; then
    say "CHECK390_KEEP_SCRATCH is set: $SCRATCH is kept (the add's transcript is in add.out and add.err)"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# ------------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), and this check reads and writes your installation and prints the block you would paste back. Run it without -x"
  fi
  fact xtrace PASS "this shell is not tracing: no trace of this run can copy a document it read or a value in one"
}

# guard_agent_session: this check is the owner's, and the command it runs writes
# the project's catalog. The loop is the five markers that say an agent, and not
# a person, is driving this shell: agentenv.AgentMarkers. CLAUDE_CODE_SSE_PORT is
# not one of them (it says this shell sits in an editor's terminal) and is
# dropped from this script's environment instead.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    fact agent-session FAIL "${marker} is set, so this is a coding agent's session: Check 390 writes a real catalog entry and redeploys real jobs, and it is yours to run at your own terminal, on the sandbox installation"
    stop "${marker} is set, so this is a coding agent's session: Check 390 is yours to run at your own terminal, on the sandbox installation"
  fi
  unset CLAUDE_CODE_SSE_PORT
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for. The editor extension's own marker (CLAUDE_CODE_SSE_PORT) is dropped from this script's environment rather than refused: it says this shell sits in an editor's terminal, not that an agent is driving it"
}

# is_belong NAME: the installation named `belong` is the owner's own. A substring
# test on purpose, so a name that merely contains it is refused too.
is_belong() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
  *belong*) return 0 ;;
  esac
  return 1
}

guard_belong() { # guard_belong NAME WHERE
  if is_belong "$1"; then
    fact belong FAIL "the project '$1' ($2) matches the installation 'belong', which this check must never touch: it would write the owner's own catalog"
    stop "the project '$1' ($2) matches the installation 'belong', which this check must never touch: it would write the owner's own catalog"
  fi
}

# ------------------------------------------------------------- the jsons
# Every document this check reads, in one program: a live check on the owner's
# machine has python3 and no jq. Each query prints `key=value` lines, so the
# shell holds them without a second parser.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import json
import re
import sys

SHA = re.compile(r"sha256:[0-9a-f]{64}")


def load(path):
    try:
        with open(path, "r", encoding="utf-8", errors="replace") as fh:
            return json.load(fh)
    except (OSError, ValueError):
        return None


def doctor(path):
    """The installation `fugaro doctor --json` names. A document with no project
    in it answers nothing: the run below would be about an installation this
    check cannot name, and its rows would be about whichever one is ambient."""
    doc = load(path)
    p = (doc or {}).get("project") if isinstance(doc, dict) else None
    if not isinstance(p, dict):
        return
    print("name=%s" % (p.get("name") or ""))
    print("gcp=%s" % (p.get("gcp_project") or ""))
    print("region=%s" % (p.get("region") or ""))
    print("registry=%s" % (p.get("registry_host") or ""))


def entry(path, name):
    """The catalog entry `name` as `fugaro image ls --json` holds it: whether it
    is there at all, and the digest the build recorded for it. An entry with no
    digest has never been built, which is a reading of its own and not a zero."""
    doc = load(path)
    rows = (doc or {}).get("entries") if isinstance(doc, dict) else None
    if not isinstance(rows, list):
        print("read=no")
        return
    print("read=yes")
    print("count=%d" % len(rows))
    for r in rows:
        if isinstance(r, dict) and r.get("name") == name:
            print("present=yes")
            print("digest=%s" % (r.get("digest") or ""))
            print("source=%s" % (r.get("source") or ""))
            return
    print("present=no")


def spec_of(doc):
    """The container spec of a Cloud Run job. gcloud's `run jobs list` forces
    the v1 API, so the container sits at
    spec.template.spec.template.spec, and the known paths are tried in order so
    a shape this check does not know is searched for rather than assumed."""
    for path in (("spec", "template", "spec", "template", "spec"),
                 ("spec", "template", "spec"),
                 ("spec", "template", "spec", "containers")):
        node = doc
        for key in path:
            node = node.get(key) if isinstance(node, dict) else None
            if node is None:
                break
        if isinstance(node, dict) and isinstance(node.get("containers"), list):
            return node
    return None


def image_of(job):
    spec = spec_of(job)
    for c in (spec or {}).get("containers") or []:
        if isinstance(c, dict) and c.get("image"):
            return c["image"]
    return ""


def env_of(job, name):
    """The value of one env var of a job's container, as a string. `gcloud run
    jobs list` forces the v1 API, where an env entry's value is the string
    itself, so it is read as one: a value of another shape answers nothing
    rather than raising, which would empty the whole reading and turn every row
    that reads it into NOT MEASURED."""
    spec = spec_of(job)
    for c in (spec or {}).get("containers") or []:
        for e in (c.get("env") or []) if isinstance(c, dict) else []:
            if isinstance(e, dict) and e.get("name") == name:
                value = e.get("value")
                return value if isinstance(value, str) else ""
    return ""


def remote_of(job):
    """FUGARO_REMOTE: the repository the job is for."""
    return env_of(job, "FUGARO_REMOTE")


def stamp_of(job):
    """FUGARO_IMAGE_ENTRY: the catalog entry the job's image was deployed from,
    written by the deploy on every job and empty on a job the catalog chose no
    image for (a repository-built image, a checkout: clone or shared job —
    D1371). It is the deploy's own statement of where the image came from, and
    the image itself cannot be asked: a catalog entry built from a Dockerfile
    is indistinguishable from a repository's own derived image."""
    return env_of(job, "FUGARO_IMAGE_ENTRY")


def jobs(path, repo):
    """The jobs of one repository, one line each: `job=<name> image=<ref>
    entry=<the entry the image came from, or ->`.

    A job is this repository's when its own FUGARO_REMOTE names it or when its
    `fugaro_repo` label is the repository's slug (`<readable>-<16 hex>`, which is
    what the installation writes) — the two spellings check 193 matches, and a
    reading that leaned on either alone would miss a job the other catches.
    `entry` is the job's own FUGARO_IMAGE_ENTRY stamp, and `-` is a job the
    catalog chose no image for."""
    doc = load(path)
    if not isinstance(doc, list):
        print("read=no")
        return
    print("read=yes")
    slug = repo.lower().replace("/", "-")
    n = 0
    for job in doc:
        if not isinstance(job, dict):
            continue
        name = ((job.get("metadata") or {}).get("name")) or ""
        if not name:
            continue
        label = ((job.get("metadata") or {}).get("labels") or {}).get("fugaro_repo") or ""
        owns = (label.startswith(slug + "-")
                and re.fullmatch(r"[0-9a-f]{16}", label[len(slug) + 1:]) is not None)
        remote = remote_of(job)
        if not owns and remote != "https://" + repo + ".git" and not remote.endswith("/" + repo + ".git"):
            continue
        n += 1
        print("job=%s image=%s entry=%s" % (name, image_of(job), stamp_of(job) or "-"))
    print("count=%d" % n)


def catalog(path):
    """The project's catalog as `fugaro image ls --json` holds it: every entry,
    with the digest the build recorded for it. A job's stamp is held to these
    rows, so an entry that is not one of them is a job on an image no catalog
    describes."""
    doc = load(path)
    rows = (doc or {}).get("entries") if isinstance(doc, dict) else None
    if not isinstance(rows, list):
        print("read=no")
        return
    print("read=yes")
    print("count=%d" % len(rows))
    for r in rows:
        if isinstance(r, dict) and r.get("name"):
            print("row=%s digest=%s" % (r["name"], r.get("digest") or ""))


def sha(path):
    """The one sha256 a document carries: `gcloud artifacts docker images
    describe`'s summary digest. It is found by shape rather than by path, so a
    gcloud that moves the field is read rather than answered as empty."""
    try:
        with open(path, "r", encoding="utf-8", errors="replace") as fh:
            text = fh.read()
    except OSError:
        print("digest=")
        return
    m = SHA.search(text)
    print("digest=%s" % (m.group(0) if m else ""))


def pending(path):
    """What `fugaro apply --status --json` says is owed. `steps` is the record's
    own order, and `read=no` is a document this check could not read at all —
    which is not the same answer as an empty record."""
    doc = load(path)
    if not isinstance(doc, dict) or not isinstance(doc.get("pending"), list):
        print("read=no")
        return
    print("read=yes")
    print("recorded=%s" % ("yes" if doc.get("recorded") else "no"))
    print("current=%s" % ("yes" if doc.get("current") else "no"))
    for row in doc["pending"]:
        if isinstance(row, dict):
            print("step=%s" % (row.get("step") or ""))
    print("owed=%d" % len(doc["pending"]))


if __name__ == "__main__":
    cmd = sys.argv[1]
    if cmd == "doctor":
        doctor(sys.argv[2])
    elif cmd == "entry":
        entry(sys.argv[2], sys.argv[3])
    elif cmd == "jobs":
        jobs(sys.argv[2], sys.argv[3])
    elif cmd == "catalog":
        catalog(sys.argv[2])
    elif cmd == "sha":
        sha(sys.argv[2])
    elif cmd == "pending":
        pending(sys.argv[2])
    else:
        sys.exit(64)
PY
}

read_json() { # read_json DEST QUERY ARGS...
  local dest=$1 query=$2
  shift 2
  python3 "$SCRATCH/read.py" "$query" "$@" >"$dest" 2>"$dest.err" || : >"$dest"
}

field() { # field FILE KEY
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# last_line FILE: the last non-blank line of a file, bounded.
last_line() {
  if [ -f "$1" ]; then
    grep -v '^[[:space:]]*$' "$1" 2>/dev/null | tail -n 1 | cut -c1-300 || true
  fi
}

# fugaro_call KEY ARGS...: one `fugaro` call, its two streams in the scratch
# directory, its status in FUGARO_STATUS. The ERR trap is dropped inside it,
# because a failing exit status is one of this check's own readings.
FUGARO_STATUS=0
fugaro_call() { # fugaro_call KEY ARGS...
  local key=$1
  shift
  : >"$SCRATCH/$key.out"
  : >"$SCRATCH/$key.err"
  FUGARO_STATUS=0
  (trap - ERR; exec "$FUGARO_BIN" "$@") >"$SCRATCH/$key.out" 2>"$SCRATCH/$key.err" || FUGARO_STATUS=$?
}

# run_bounded SECONDS KEY CMD...: run one command with its stdin on /dev/null
# and its two streams in the scratch directory, and end it at the deadline. The
# child execs, so the pid the deadline kills IS the command.
RUN_STATUS=0
RUN_TIMED_OUT=no
RUN_SECONDS=0
run_bounded() { # run_bounded SECONDS KEY CMD...
  local secs=$1 key=$2
  shift 2
  RUN_TIMED_OUT=no
  local started=$SECONDS
  : >"$SCRATCH/$key.out"
  : >"$SCRATCH/$key.err"
  (trap - ERR; exec "$@") </dev/null >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
  local pid=$! waited=0
  while kill -0 "$pid" 2>/dev/null; do
    if [ "$waited" -ge "$secs" ]; then
      RUN_TIMED_OUT=yes
      say "the run is still going after ${secs}s: killing it (TERM, then KILL)"
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

fugaro_flags() { # fugaro_flags -- extra flags...
  local out=()
  if [ ${#FUGARO_FLAGS[@]} -gt 0 ]; then out+=("${FUGARO_FLAGS[@]}"); fi
  if [ -n "$PROJECT" ]; then out+=(--project "$PROJECT"); fi
  if [ ${#out[@]} -gt 0 ]; then printf '%s\n' "${out[@]}"; fi
}

# ---------------------------------------------------------------- the checkout
# The repository stage is per repository (D1574): the redeploy `image add` runs
# is the checkout's own, so this check has to know which repository it stands in.
judge_checkout() {
  local top rc=0 url
  top=$(git rev-parse --show-toplevel 2>/dev/null) || rc=$?
  if [ "$rc" != 0 ] || [ -z "$top" ]; then
    stop "this directory is not a git work tree: the redeploy 'image add' runs is the checkout's own repository (D1574), so run this check in a checkout of a repository the project onboards"
  fi
  url=$(git -C "$top" remote get-url origin 2>/dev/null) || rc=$?
  if [ "$rc" != 0 ] || [ -z "$url" ]; then
    stop "$top has no 'origin' remote, so this check cannot name the repository whose jobs the redeploy is for"
  fi
  REPO_NAME=$(repo_of "$url") || stop "the origin remote of $top ($url) does not name a repository this check can read as owner/name"
  CHECKOUT=$top
  fact checkout PASS "$CHECKOUT is a git work tree and its origin remote names ${REPO_NAME}: the redeploy below is that repository's, and the jobs this check reads are its own"
}

# repo_of URL prints `owner/name` out of an origin remote, in the shapes git
# prints them. A remote that is a path on this machine names no repository, so it
# is a refusal (exit 1) rather than a made-up owner/name.
repo_of() { # repo_of <url>
  python3 - "$1" <<'PY'
import re, sys
u = sys.argv[1].strip()
u = re.sub(r"\.git$", "", u)
u = re.sub(r"^[a-zA-Z][a-zA-Z0-9+.-]*://", "", u)
if u.startswith(("/", "./", "../", "~")):
    sys.exit(1)
head = u.split("/", 1)[0]
if "@" in head:
    u = u.split("@", 1)[1]
u = re.sub(r"^[^/:]+:", "", u)
parts = [p for p in re.split(r"[/:]", u) if p]
if len(parts) < 2:
    sys.exit(1)
print("%s/%s" % (parts[-2], parts[-1]))
PY
}

# ------------------------------------------------------------ the installation
judge_project() {
  local flags=()
  while IFS= read -r line; do flags+=("$line"); done < <(fugaro_flags)
  fugaro_call doctor doctor --json ${flags[@]+"${flags[@]}"}
  local status=$FUGARO_STATUS
  read_json "$SCRATCH/project.txt" doctor "$SCRATCH/doctor.out"
  PROJECT_NAME=$(field "$SCRATCH/project.txt" name)
  PROJECT_GCP=$(field "$SCRATCH/project.txt" gcp)
  PROJECT_REGION=$(field "$SCRATCH/project.txt" region)
  if [ -z "$PROJECT_NAME" ] || [ -z "$PROJECT_GCP" ] || [ -z "$PROJECT_REGION" ]; then
    stop "fugaro doctor --json named no project (exit $status: $(last_line "$SCRATCH/doctor.err")), so this check cannot tell which installation it would write: run it where a project config is in force, or pass --project <name>"
  fi
  if [ -n "$PROJECT" ] && [ "$PROJECT" != "$PROJECT_NAME" ]; then
    stop "you typed --project $PROJECT and the local config in force answers $PROJECT_NAME (fugaro doctor --json): two different installations, and this check will not pick one"
  fi
  fact installation PASS "the installation is project $PROJECT_NAME on GCP project $PROJECT_GCP in region $PROJECT_REGION (fugaro doctor --json): the entry below is added to that project's catalog and the jobs read are that project's"
}

# ---------------------------------------------------------------- the entry
# image_ls writes the catalog to $SCRATCH/entries.json and the entry's row to
# $SCRATCH/entry.txt.
image_ls() {
  local flags=()
  while IFS= read -r line; do flags+=("$line"); done < <(fugaro_flags)
  fugaro_call entries image ls --json ${flags[@]+"${flags[@]}"}
  IMAGE_LS_STATUS=$FUGARO_STATUS
  read_json "$SCRATCH/entry.txt" entry "$SCRATCH/entries.out" "$ENTRY"
}

step_entry() {
  image_ls
  if [ "$(field "$SCRATCH/entry.txt" read)" != yes ]; then
    unmeasured entry "fugaro image ls --json did not answer a catalog (exit $IMAGE_LS_STATUS: $(last_line "$SCRATCH/entries.err")), so this check cannot tell whether the entry ${ENTRY} is already there -- and an entry this check cannot read is one it must not add. The catalog is the project's own document; fix what the line names and run the check again"
    SKIP_REST=yes
    return 0
  fi
  ENTRY_PRESENT=$(field "$SCRATCH/entry.txt" present)
  if [ "$ENTRY_PRESENT" != yes ]; then
    fact entry PASS "the project's catalog holds no entry named ${ENTRY} ($(field "$SCRATCH/entry.txt" count) entr(ies) read): the add below is a new entry, and the teardown removes exactly it"
    return 0
  fi
  # A leftover of an earlier attempt: this check owns the name it adds, so it
  # takes its own leftover away rather than refusing to run again.
  say "the entry ${ENTRY} is already in the catalog: removing it first, because this check owns that name (a leftover of an earlier attempt)"
  local flags=()
  while IFS= read -r line; do flags+=("$line"); done < <(fugaro_flags)
  fugaro_call rm-leftover image rm "$ENTRY" ${flags[@]+"${flags[@]}"}
  if [ "$FUGARO_STATUS" != 0 ]; then
    unmeasured entry "the catalog already holds an entry named ${ENTRY}, which this check adds and removes, and removing that leftover failed (exit $FUGARO_STATUS: $(last_line "$SCRATCH/rm-leftover.err")). Remove it yourself (fugaro image rm ${ENTRY}) or pass --entry with a name nothing else uses, and run the check again"
    SKIP_REST=yes
    return 0
  fi
  fact entry PASS "the catalog already held an entry named ${ENTRY} (a leftover of an earlier attempt) and this check removed it with its own fugaro image rm: the add below is a new entry again, and running this check twice in a row works"
}

# ------------------------------------------------------------------ the add
step_add() {
  ADD_OK=no
  local flags=()
  while IFS= read -r line; do flags+=("$line"); done < <(fugaro_flags)
  say "running: ${FUGARO_BIN} image add ${ENTRY} --dockerfile ${DOCKERFILE}${PROJECT:+ --project $PROJECT}  (deadline ${DEADLINE}s; this BUILDS an image, redeploys the repository's jobs and publishes the catalog)"
  run_bounded "$DEADLINE" add "$FUGARO_BIN" image add "$ENTRY" --dockerfile "$DOCKERFILE" ${flags[@]+"${flags[@]}"}
  ADD_STATUS=$RUN_STATUS
  ADD_TIMED_OUT=$RUN_TIMED_OUT
  ADD_SECONDS=$RUN_SECONDS
  ADD_LAST=$(last_line "$SCRATCH/add.err")
  ADD_LAST_FROM=stderr
  if [ -z "$ADD_LAST" ]; then
    ADD_LAST=$(last_line "$SCRATCH/add.out")
    ADD_LAST_FROM=stdout
  fi
  if [ "$ADD_TIMED_OUT" = yes ]; then
    fact add FAIL "fugaro image add was still running after ${DEADLINE}s and this check killed it (TERM, then KILL). Its last line ($ADD_LAST_FROM): ${ADD_LAST:-<nothing>}. The whole transcript is in $SCRATCH/add.out and add.err (set CHECK390_KEEP_SCRATCH=1 to keep the directory)"
    return 0
  fi
  if [ "$ADD_STATUS" != 0 ]; then
    fact add FAIL "fugaro image add ended after ${ADD_SECONDS}s with exit ${ADD_STATUS}, so the entry was not made live by its own command: this is exactly what spec rule 1 forbids (D1561). Its last line ($ADD_LAST_FROM): ${ADD_LAST:-<nothing>}. The whole transcript is in $SCRATCH/add.out and add.err (set CHECK390_KEEP_SCRATCH=1 to keep the directory)"
    return 0
  fi
  local said
  said=$(grep -c "Image ${ENTRY} added and now available to your jobs." "$SCRATCH/add.out" 2>/dev/null || true)
  if [ "${said:-0}" = 0 ]; then
    fact add FAIL "fugaro image add exited 0 after ${ADD_SECONDS}s but printed none of its own success line ('Image ${ENTRY} added and now available to your jobs.'): a run that answers nothing has not said the entry is live. Its last line ($ADD_LAST_FROM): ${ADD_LAST:-<nothing>}. The whole transcript is in $SCRATCH/add.out (set CHECK390_KEEP_SCRATCH=1 to keep it)"
    return 0
  fi
  ADD_OK=yes
  fact add PASS "fugaro image add ended after ${ADD_SECONDS}s with exit 0 and its own success line: one command added the entry, built it, redeployed the checkout's repository and published the catalog (D1561, D1574, D1577). What the rows below read is whether those steps really landed"
}

# ------------------------------------------------------------- the readings
step_record() {
  image_ls
  if [ "$(field "$SCRATCH/entry.txt" read)" != yes ]; then
    unmeasured record "fugaro image ls --json did not answer a catalog after the add (exit $IMAGE_LS_STATUS: $(last_line "$SCRATCH/entries.err")), so the digest the build recorded could not be read"
    return 0
  fi
  if [ "$(field "$SCRATCH/entry.txt" present)" != yes ]; then
    fact record FAIL "the entry ${ENTRY} is NOT in the catalog after 'fugaro image add' exited 0: the command said the entry was added and the project's catalog does not hold it. The catalog is read with fugaro image ls --json; this is the change itself missing"
    return 0
  fi
  RECORD_DIGEST=$(field "$SCRATCH/entry.txt" digest)
  RECORD_SOURCE=$(field "$SCRATCH/entry.txt" source)
  if [ -z "$RECORD_DIGEST" ]; then
    unmeasured record "the entry ${ENTRY} is in the catalog but carries no digest (fugaro image ls --json), so the build's own record was not written or not read: source is ${RECORD_SOURCE}. A run that built the entry writes the record the digest is read from (gcp.CatalogRecordKey); settle that and run the check again"
    return 0
  fi
  fact record PASS "the catalog holds ${ENTRY} (source: ${RECORD_SOURCE}) and the build recorded the digest ${RECORD_DIGEST}: this is what the jobs below have to be running"
}

step_jobs() {
  : >"$SCRATCH/jobs.json"
  local rc=0
  (trap - ERR; exec "$GCLOUD_BIN" run jobs list --project="$PROJECT_GCP" --region="$PROJECT_REGION" --format=json) \
    >"$SCRATCH/jobs.json" 2>"$SCRATCH/jobs.err" || rc=$?
  if [ "$rc" != 0 ] || [ ! -s "$SCRATCH/jobs.json" ]; then
    unmeasured job "gcloud run jobs list exited $rc: $(last_line "$SCRATCH/jobs.err"). The deployed jobs are what this row reads, and without the listing nothing here is settled about them"
    return 0
  fi
  read_json "$SCRATCH/jobs.txt" jobs "$SCRATCH/jobs.json" "$REPO_NAME"
  if [ "$(field "$SCRATCH/jobs.txt" read)" != yes ]; then
    unmeasured job "gcloud run jobs list answered a document this check could not read as a list of jobs: $(last_line "$SCRATCH/jobs.err")"
    return 0
  fi
  JOB_COUNT=$(field "$SCRATCH/jobs.txt" count)
  if [ "${JOB_COUNT:-0}" = 0 ]; then
    unmeasured job "no job of ${REPO_NAME} was found in 'gcloud run jobs list' (matched by the job's own FUGARO_REMOTE and by its fugaro_repo label): either this checkout's repository is not deployed in this project, or its jobs carry neither. Run this check in a checkout of a repository this project onboards, and after 'fugaro update --repo' has deployed it"
    return 0
  fi
  JOB_LINES=$(awk '{ printf "%s%s", (n++ ? "; " : ""), $0 }' "$SCRATCH/jobs.txt" | cut -c1-600)
  # Which entry a job's image came from is the deploy's own stamp on the job
  # (FUGARO_IMAGE_ENTRY, written on every job and empty on one the catalog chose
  # no image for — D1371), and the stamp is what this row holds to the catalog.
  # The entry this check added is deliberately NOT what it looks for: a catalog
  # gives a job an image only through a workflow's `image_name:`, its `needs:`,
  # the project's map or the catalog's default (catalog.JobImage), and an entry
  # a job selects is one `fugaro image rm` REFUSES to remove (image_catalog.go's
  # catalogPointedAt) — so an entry this check adds and removes again is one no
  # job can be running, and a row that demanded the jobs run it would FAIL every
  # correct installation. What the redeploy the add owed owes the repository's
  # jobs is that each of them is left on an image the catalog describes.
  read_json "$SCRATCH/catalog.txt" catalog "$SCRATCH/entries.out"
  if [ "$(field "$SCRATCH/catalog.txt" read)" != yes ]; then
    unmeasured job "the project's catalog could not be read after the add (the record row says why), so whether the entry a job's image came from is one the catalog holds could not be told: FUGARO_IMAGE_ENTRY is the deploy's stamp of the entry each job's image was deployed from, and a stamp with no catalog to hold it to settles nothing"
    return 0
  fi
  local line stamp chosen=0 stray=""
  while IFS= read -r line; do
    case "$line" in job=*) ;; *) continue ;; esac
    stamp=${line##*entry=}
    if [ -z "$stamp" ] || [ "$stamp" = - ]; then continue; fi
    chosen=$((chosen + 1))
    if ! grep -q "^row=${stamp} " "$SCRATCH/catalog.txt"; then
      stray="${stray}${stray:+, }${stamp}"
    fi
  done <"$SCRATCH/jobs.txt"
  JOB_CHOSEN=$chosen
  if [ -n "$stray" ]; then
    fact job FAIL "a job of ${REPO_NAME} was deployed from the catalog entry ${stray}, and the project's catalog does NOT hold it: the redeploy left that job on an image no catalog describes. The jobs read: ${JOB_LINES}"
    return 0
  fi
  fact job PASS "${chosen} of the ${JOB_COUNT} job(s) of ${REPO_NAME} carries the catalog's own stamp (FUGARO_IMAGE_ENTRY, the entry the deploy gave its image) and every stamp names an entry the project's catalog holds; the rest the catalog chose no image for and run the image this project builds for the repository itself (D1371, D1574). The jobs read: ${JOB_LINES}"
}

step_registry() {
  local line
  line=$(grep -m1 ' entry=[^-]' "$SCRATCH/jobs.txt" 2>/dev/null || true)
  if [ -z "$line" ]; then
    unmeasured registry "no job of ${REPO_NAME} carries a catalog entry's stamp (the job row says so): the project's catalog gives none of this repository's jobs an image, so there is no deployed reference whose digest could be resolved"
    return 0
  fi
  JOB_ENTRY=${line##*entry=}
  JOB_IMAGE=$(printf '%s\n' "$line" | awk -F'image=' '{ print $2 }' | awk '{ print $1 }')
  local ref rc=0
  case "$JOB_IMAGE" in
  *@sha256:*) ref=$JOB_IMAGE ;;
  *:latest) ref=$JOB_IMAGE ;;
  *) ref="${JOB_IMAGE}:latest" ;;
  esac
  : >"$SCRATCH/registry.json"
  (trap - ERR; exec "$GCLOUD_BIN" artifacts docker images describe "$ref" --project="$PROJECT_GCP" --format=json) \
    >"$SCRATCH/registry.json" 2>"$SCRATCH/registry.err" || rc=$?
  if [ "$rc" != 0 ] || [ ! -s "$SCRATCH/registry.json" ]; then
    unmeasured registry "gcloud artifacts docker images describe ${ref} exited $rc: $(last_line "$SCRATCH/registry.err"). The digest the deployed image resolves to is read from the registry, and without it nothing here is settled about the digest the jobs will run"
    return 0
  fi
  read_json "$SCRATCH/registry.txt" sha "$SCRATCH/registry.json"
  LIVE_DIGEST=$(field "$SCRATCH/registry.txt" digest)
  if [ -z "$LIVE_DIGEST" ]; then
    unmeasured registry "gcloud artifacts docker images describe ${ref} answered a document carrying no sha256: $(last_line "$SCRATCH/registry.err"). The image the job is deployed with is read back as ${JOB_IMAGE}"
    return 0
  fi
  fact registry PASS "the image the job is deployed with (${JOB_IMAGE}) resolves now to ${LIVE_DIGEST} (gcloud artifacts docker images describe), and it is the image of the catalog entry ${JOB_ENTRY} the deploy stamped on that job: a job deployed with the moving tag pulls this digest when it starts (D1152)"
}

step_digest() {
  if [ -z "${JOB_ENTRY:-}" ]; then
    unmeasured digest "no job of ${REPO_NAME} carries a catalog entry's stamp (the job row says so), so there is no deployed image whose digest could be held to the catalog's record"
    return 0
  fi
  JOB_RECORD_DIGEST=$(sed -n "s/^row=${JOB_ENTRY} digest=//p" "$SCRATCH/catalog.txt" 2>/dev/null | head -n 1)
  if [ -z "${LIVE_DIGEST:-}" ] || [ -z "${JOB_RECORD_DIGEST:-}" ]; then
    unmeasured digest "the two digests this row compares were not both read (the registry row says which, and the other is the catalog's own record for ${JOB_ENTRY}): the claim is that the digest the deployed image resolves to is the digest the catalog records for the entry that job's image came from"
    return 0
  fi
  if [ "$JOB_RECORD_DIGEST" != "$LIVE_DIGEST" ]; then
    fact digest FAIL "the image the job of ${REPO_NAME} is deployed with resolves to ${LIVE_DIGEST}, and the project's catalog records ${JOB_RECORD_DIGEST} for ${JOB_ENTRY}, the entry that job's image was deployed from: the redeploy did not pin the digest the build produced. The record is what the daily check and every reader of the entry go by, so the two disagreeing is the finding"
    return 0
  fi
  fact digest PASS "the image the job of ${REPO_NAME} is deployed with resolves to ${LIVE_DIGEST}, which is exactly the digest the catalog records for ${JOB_ENTRY}, the entry its image was deployed from: the redeploy pinned the build's digest on the repository's jobs (D1561, D1574)"
}

step_status() {
  local flags=()
  while IFS= read -r line; do flags+=("$line"); done < <(fugaro_flags)
  fugaro_call status apply --status --json ${flags[@]+"${flags[@]}"}
  STATUS_STATUS=$FUGARO_STATUS
  read_json "$SCRATCH/status.txt" pending "$SCRATCH/status.out"
  if [ "$(field "$SCRATCH/status.txt" read)" != yes ]; then
    unmeasured status "fugaro apply --status --json did not answer its document (exit $STATUS_STATUS: $(last_line "$SCRATCH/status.err")), so what is still owed could not be read"
    return 0
  fi
  OWED=$(field "$SCRATCH/status.txt" owed)
  if [ "${OWED:-0}" != 0 ]; then
    local steps
    steps=$(sed -n 's/^step=//p' "$SCRATCH/status.txt" | awk 'NF { printf "%s%s", (n++ ? ", " : ""), $0 }')
    fact status FAIL "the add ran its own steps and ${OWED} step(s) are still owed: ${steps}. A management command runs the steps that make its change live (D1561), and what it could not run is left for 'fugaro apply' with the command named (D1563) — an entry added and left owed is the failure this check exists to catch. Run 'fugaro apply' and this check again"
    return 0
  fi
  fact status PASS "fugaro apply --status --json owes nothing after the add (recorded=$(field "$SCRATCH/status.txt" recorded)): the entry was made live by the one command that wrote it, with no second command needed (D1561)"
}

# ------------------------------------------------------------------- teardown
step_teardown() {
  local flags=()
  while IFS= read -r line; do flags+=("$line"); done < <(fugaro_flags)
  say "teardown: removing the entry this check added (fugaro image rm ${ENTRY})"
  run_bounded "$DEADLINE" rm "$FUGARO_BIN" image rm "$ENTRY" ${flags[@]+"${flags[@]}"}
  TEARDOWN_STATUS=$RUN_STATUS
  TEARDOWN_TIMED_OUT=$RUN_TIMED_OUT
  if [ "$TEARDOWN_TIMED_OUT" = yes ]; then
    fact teardown FAIL "the teardown (fugaro image rm ${ENTRY}) was still running after ${DEADLINE}s and this check killed it: the entry this check added is STILL in the project's catalog and its jobs may still be redeployed to it. Remove it yourself with 'fugaro image rm ${ENTRY}' and run 'fugaro apply'"
    return 0
  fi
  if [ "$TEARDOWN_STATUS" != 0 ]; then
    fact teardown FAIL "the teardown (fugaro image rm ${ENTRY}) exited ${TEARDOWN_STATUS}: $(last_line "$SCRATCH/rm.err"). The entry this check added is STILL in the project's catalog — remove it yourself with 'fugaro image rm ${ENTRY}' and run 'fugaro apply'"
    return 0
  fi
  image_ls
  if [ "$(field "$SCRATCH/entry.txt" present)" = yes ]; then
    fact teardown FAIL "fugaro image rm ${ENTRY} exited 0 and the catalog still holds the entry (fugaro image ls --json): the change this check made is still in the project"
    return 0
  fi
  fact teardown PASS "the entry this check added is out of the project's catalog again (fugaro image rm ${ENTRY}, exit 0, and fugaro image ls --json no longer holds it): the installation is as this check found it, and the scratch directory is removed at the end"
}

# ------------------------------------------------------------------- the verdict
the_verdict() {
  if [ "${ADD_TIMED_OUT:-no}" = yes ] || [ "${ADD_STATUS:-0}" != 0 ] || [ "${ADD_OK:-}" = no ]; then
    fact claim FAIL "the claim does NOT hold as measured: 'fugaro image add' did not make the entry live by itself (the add row says how it ended). Spec rule 1 is that a management command writes its change and runs the steps that make it live (D1561)"
    REACHED_END=yes
    exit 1
  fi
  if [ -n "${OWED:-}" ] && [ "${OWED:-0}" != 0 ]; then
    fact claim FAIL "the claim does NOT hold as measured: ${OWED} step(s) were still owed after the add (the status row lists them), so one command did NOT make the entry live"
    REACHED_END=yes
    exit 1
  fi
  if [ "${TEARDOWN_STATUS:-0}" != 0 ] || [ "${TEARDOWN_TIMED_OUT:-no}" = yes ]; then
    fact claim FAIL "the claim itself held, but the teardown did NOT: the entry this check added may still be in the project's catalog (the teardown row says how it failed). Remove it yourself with 'fugaro image rm ${ENTRY}' and run 'fugaro apply'"
    REACHED_END=yes
    exit 1
  fi
  # Every other row that failed is a finding of its own: the reading it stands
  # for did not hold, and the claim cannot be a PASS while one of them says so.
  local key verdict failed=""
  while IFS='|' read -r key verdict _; do
    [ -n "$key" ] || continue
    case "$verdict" in
    FAIL)
      case " ${UNMEASURED_KEYS:-} " in *" $key "*) ;; *) failed="${failed:+$failed }$key" ;; esac
      ;;
    esac
  done <"$FACTS_FILE"
  if [ -n "$failed" ]; then
    fact claim FAIL "the claim does NOT hold as measured: the row(s) ${failed// /, } above did not hold, and each of them says why. Read them before the claim: the rows are the reading, this line is only their sum"
    REACHED_END=yes
    exit 1
  fi
  if [ -n "$UNMEASURED_KEYS" ]; then
    fact claim INFO "the claim is NOT MEASURED by this run: the row(s) ${UNMEASURED_KEYS// /, } above could not be read, so nothing here is an answer about whether 'fugaro image add' alone makes an entry live. Settle what the row names and run the check again"
    REACHED_END=yes
    exit 2
  fi
  fact claim PASS "'fugaro image add' alone made the entry live on the sandbox installation: ${ENTRY} is in the project's catalog with the digest the build recorded (${RECORD_DIGEST}), the repository's jobs are the catalog's own answer (${JOB_CHOSEN} of ${JOB_COUNT} carries the catalog's stamp and no job of ${REPO_NAME} is left on an image the catalog does not describe), the image the job is deployed with resolves to ${LIVE_DIGEST}, which is the digest the catalog records for ${JOB_ENTRY}, and fugaro apply --status owes nothing. One command, no second one (D1561, D1574, D1577) — and the entry this check added is out of the catalog again"
  REACHED_END=yes
  exit 0
}

# ------------------------------------------------------------- the block
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, and one that was never reached reads
# FAIL -- not reached. That is what makes the block safe to diff between runs.
BLOCK_KEYS=(xtrace agent-session checkout installation belong entry add record job registry digest status teardown claim)

print_block() { # print_block FACTS STATUS -- always on the owner's own stdout
  local facts=$1 status=$2
  local key line printed=$'\n'
  printf '\nCHECK 390 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
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
  while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case "$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done <<<"$facts"
  if [ -n "$SCRATCH" ] && [ "$SCRATCH_REMOVED" != yes ]; then
    printf 'FACT scratch-removed: FAIL — the scratch directory is STILL THERE at %s -- remove it yourself\n' "$SCRATCH" >&3
  else
    printf 'FACT scratch-removed: PASS — the scratch directory is gone, and nothing of this check is left behind\n' >&3
  fi
  printf 'CHECK 390 RESULT END (exit %s)\n' "$status" >&3
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  local facts=$FACTS_CAPTURED
  if [ "$HELPED" = yes ]; then exit 0; fi
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its verdict ends honestly, whatever stopped it.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its verdict (exit $status): the rows that read 'not reached' were NOT measured"
    fi
    status=2
  fi
  local key verdict failed=no
  while IFS='|' read -r key verdict _; do
    case "$verdict" in
    FAIL)
      case " ${UNMEASURED_KEYS:-} " in *" $key "*) ;; *) failed=yes ;; esac
      ;;
    esac
  done <<<"$facts"
  if [ "$status" = 0 ]; then
    if [ "$failed" = yes ]; then
      status=1
    elif [ -n "$UNMEASURED_KEYS" ]; then
      status=2
    fi
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

on_signal() {
  owner "interrupted: printing what was measured and cleaning up"
  exit 2
}

# -------------------------------------------------------------------- the plan
print_plan() {
  say "dry run: NOTHING below was done. A real run would do, in this order:"
  say "  [reads] git rev-parse --show-toplevel and the checkout's origin remote: the repository whose jobs the redeploy is for (D1574)"
  say "  [reads] ${FUGARO_BIN} doctor --json${PROJECT:+ --project $PROJECT}: the project, its GCP project and its region, with a project named belong refused here and in what doctor answers"
  say "  [reads] ${FUGARO_BIN} image ls --json: whether the catalog already holds ${ENTRY} (a leftover is removed first, so running this check twice works)"
  say "  [writes] ${FUGARO_BIN} image add ${ENTRY} --dockerfile ${DOCKERFILE:-<owner/name:path>}: THE COMMAND THIS CHECK MEASURES. It BUILDS an image, REDEPLOYS the repository's jobs and PUBLISHES the catalog (D1561, D1574, D1577), under a ${DEADLINE}s deadline"
  say "  [reads] ${FUGARO_BIN} image ls --json: the digest the build recorded for ${ENTRY}"
  say "  [reads] ${GCLOUD_BIN} run jobs list --project <gcp> --region <region> --format=json: the jobs of the checkout's repository, the image each runs and the catalog entry each image was deployed from (the job's own FUGARO_IMAGE_ENTRY stamp)"
  say "  [reads] ${FUGARO_BIN} image ls --json: the entries the catalog holds, which every one of those stamps is held to"
  say "  [reads] ${GCLOUD_BIN} artifacts docker images describe <the job's image>: the digest that image resolves to now"
  say "  [reads] ${FUGARO_BIN} apply --status --json: what is still owed, which must be nothing"
  say "  [writes] ${FUGARO_BIN} image rm ${ENTRY}: the teardown, the inverse change, which takes the entry back out and redeploys"
  say "  [writes] nothing else: one scratch directory (mode 0700) holding the transcripts, removed at the end"
  say "Nothing is asked of you, and no agent runs any step of it."
}

# -------------------------------------------------------------------- main
parse_args() {
  PROJECT=${CHECK390_PROJECT:-}
  FUGARO_FLAGS=()
  DRY_RUN=${CHECK390_DRY_RUN:-}
  DEADLINE=${CHECK390_DEADLINE:-1800}
  ENTRY=${CHECK390_ENTRY:-chk390-catalog}
  DOCKERFILE=${CHECK390_DOCKERFILE:-}
  while [ $# -gt 0 ]; do
    case "$1" in
    --dockerfile)
      [ $# -ge 2 ] || die "--dockerfile needs an owner/name:path. usage: $USAGE"
      DOCKERFILE=$2
      shift 2
      ;;
    --entry)
      [ $# -ge 2 ] || die "--entry needs a catalog entry name. usage: $USAGE"
      ENTRY=$2
      shift 2
      ;;
    --project)
      [ $# -ge 2 ] || die "--project needs a Fugaro project name. usage: $USAGE"
      PROJECT=$2
      shift 2
      ;;
    --fugaro-flags)
      [ $# -ge 2 ] || die "--fugaro-flags needs a string of flags. usage: $USAGE"
      read -r -a FUGARO_FLAGS <<<"$2"
      shift 2
      ;;
    --deadline)
      [ $# -ge 2 ] || die "--deadline needs a number of seconds. usage: $USAGE"
      DEADLINE=$2
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    -h | --help)
      printf 'usage: %s\n' "$USAGE"
      printf '\nAdds a real catalog entry with `%s image add`, then reads from outside the\nCLI that the entry is in the catalog with the digest the build recorded, that the\njobs of the repository are left on images the catalog describes, that the digest\nthe deployed image resolves to is the one the catalog records, and that nothing\nis left owed — and removes the entry again.\nIt prints one block headed CHECK 390 RESULT and exits 0 when the claim held, 1\nwhen it did not, and 2 when this run could not tell. It is yours to run: no agent\nruns any step of it.\n' "$FUGARO_BIN"
      HELPED=yes
      PRINTED=1
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  case "$DEADLINE" in
  '' | *[!0-9]*) die "--deadline must be a whole number of seconds (got '$DEADLINE'). usage: $USAGE" ;;
  esac
  if [ "$DEADLINE" -lt 5 ]; then
    die "--deadline $DEADLINE is too short to measure anything: give it at least 5 seconds. usage: $USAGE"
  fi
  if [ -z "$DOCKERFILE" ]; then
    die "Check 390 needs --dockerfile: the entry it adds is built from a Dockerfile in one of the project's repositories, named as owner/name:path. usage: $USAGE"
  fi
  case "$DOCKERFILE" in
  */*:*) ;;
  *) die "--dockerfile must name a Dockerfile in one of the project's repositories, as owner/name:path (got '$DOCKERFILE'). usage: $USAGE" ;;
  esac
  case "$ENTRY" in
  '' | *[!a-z0-9-]*) die "--entry must be a lowercase catalog entry name (got '$ENTRY'). usage: $USAGE" ;;
  esac
}

main() {
  need_tools
  parse_args "$@"
  scratch_up
  write_reader
  guard_xtrace
  guard_agent_session
  if [ -n "$DRY_RUN" ]; then
    if [ -n "$PROJECT" ]; then
      guard_belong "$PROJECT" "the project you typed"
      fact belong PASS "the project name you typed ($PROJECT) does not name the owner's own installation (what was typed contains no 'belong')"
    else
      fact belong INFO "no --project was typed, so the guard had nothing to judge: a real run refuses an installation named belong in what doctor answers too, before anything is run"
    fi
    fact checkout INFO "planned: a real run reads git rev-parse --show-toplevel and the checkout's origin remote, which name the repository whose jobs the redeploy is for (D1574)"
    fact installation INFO "planned: a real run asks fugaro doctor --json which installation the config in force names$( [ -n "$PROJECT" ] && printf ', holds it to the --project %s you typed,' ) and refuses an installation named belong in that answer too"
    fact entry INFO "planned: a real run reads fugaro image ls --json and removes an entry named ${ENTRY} it finds there (a leftover of an earlier attempt), so running the check twice works"
    fact add INFO "planned: a real run runs fugaro image add ${ENTRY} --dockerfile ${DOCKERFILE} under a ${DEADLINE}s deadline. This BUILDS an image, redeploys the repository's jobs and publishes the catalog"
    fact record INFO "planned: a real run reads the digest the build recorded for ${ENTRY} out of fugaro image ls --json"
    fact job INFO "planned: a real run reads gcloud run jobs list and holds every job of the checkout's repository to the catalog: each job carries the deploy's stamp of the entry its image came from (FUGARO_IMAGE_ENTRY), and each stamp must name an entry the catalog holds"
    fact registry INFO "planned: a real run reads the digest the deployed image resolves to with gcloud artifacts docker images describe"
    fact digest INFO "planned: a real run holds the two digests to each other: the one the deployed image resolves to and the one the catalog records for the entry that job's image came from"
    fact status INFO "planned: a real run reads fugaro apply --status --json, which must owe nothing after the add"
    fact teardown INFO "planned: a real run removes the entry again with fugaro image rm ${ENTRY} and reads the catalog back"
    fact claim INFO "planned: the verdict is a real run's, and it is the command's own transcript and the cloud's own documents that answer it"
    BLOCK_KEYS=(xtrace agent-session checkout installation belong entry add record job registry digest status teardown claim)
    print_plan
    REACHED_END=yes
    return 0
  fi
  if [ -n "$PROJECT" ]; then guard_belong "$PROJECT" "the project you typed"; fi
  judge_checkout
  judge_project
  guard_belong "$PROJECT_NAME" "the config in force"
  fact belong PASS "neither the project you typed${PROJECT:+ ($PROJECT)} nor the one the config in force answers ($PROJECT_NAME) names the owner's own installation (nothing read contains 'belong')"
  step_entry
  # A catalog this check could not read, or a leftover it could not remove, is a
  # reason NOT to run the command: the entry it adds is one it must be able to
  # read first, and it must not write over a name it does not own.
  if [ "${SKIP_REST:-}" = yes ]; then the_verdict; fi
  step_add
  step_record
  step_jobs
  step_registry
  step_digest
  step_status
  step_teardown
  the_verdict
}

FUGARO_BIN=${CHECK390_FUGARO:-fugaro}
GCLOUD_BIN=${CHECK390_GCLOUD:-gcloud}
FUGARO_FLAGS=()
PROJECT=""
PROJECT_NAME=""
PROJECT_GCP=""
PROJECT_REGION=""
CHECKOUT=""
REPO_NAME=""
ENTRY=chk390-catalog
DOCKERFILE=""
DEADLINE=1800
IMAGE_LS_STATUS=0
ENTRY_PRESENT=""
SKIP_REST=""
ADD_OK=""
ADD_STATUS=0
ADD_TIMED_OUT=no
ADD_SECONDS=0
ADD_LAST=""
ADD_LAST_FROM=stderr
RECORD_DIGEST=""
RECORD_SOURCE=""
JOB_COUNT=""
JOB_CHOSEN=""
JOB_ENTRY=""
JOB_IMAGE=""
JOB_RECORD_DIGEST=""
LIVE_DIGEST=""
OWED=""
STATUS_STATUS=0
TEARDOWN_STATUS=0
TEARDOWN_TIMED_OUT=no

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
