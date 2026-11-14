#!/usr/bin/env bash
# Check 291 — what a catalog image's ENV does in a run, as ONE script YOU run.
#
# It settles the live half of task IE4 of the image-ENV work
# (docs/image-env/implementation-plan.md §6; the reconciliation's D1370, D1371,
# D1372, D1375 and D1381; issue #1755): a catalog entry whose Dockerfile sets an
# ordinary marker and one reserved name, the stamp `fugaro update --repo` writes
# on the job and `fugaro doctor --all` reads back, one trivial run reading the
# marker present and the reserved name absent out of a command the run ran
# through `fugaro verify`, and the refutation — the same workflow on a
# repository-built image, which passes none of its image's own variables
# (D1370). The register's row and section are docs/gcp-live-checklist.md,
# "Check 291"; the rule it reads back is docs/base-image.md's "Bring your own".
#
# Usage:      bash scripts/live-checks/check291.sh --repo <owner/name> --region <region>
#             [--workflow <name>] [--entry <name>] [--yes] [--dry-run]
#             Run it FROM the sandbox repository's own checkout: it edits that
#             checkout's fugaro.yaml, commits and pushes what it edited, and
#             closes the pull requests the two runs open. --workflow names the
#             workflow the runs land on (the only one in fugaro.yaml when it is
#             not named); --entry names the catalog entry this check publishes
#             (fugaro-check291 by default); --region is the Cloud Run region the
#             jobs are deployed in; --yes is the consent below; --dry-run prints
#             the plan and the guards and makes NO cloud call and NO fugaro call
#             at all. Each run it launches is read out of that launch's own
#             --json document and then WAITED FOR until its row is terminal —
#             `fugaro run` returns at launch, and a run's transcript is written
#             as its stages end (CHECK291_RUN_WAIT_SECONDS, 40 minutes by
#             default, bounds the wait; CHECK291_RUN_POLL_SECONDS is how often
#             the listing is read).
# Needs:      bash, mktemp, python3, git, gh, gcloud and fugaro on PATH. Nothing
#             is installed and nothing is fetched; a missing tool stops the run
#             with one line naming it.
# Where:      the SANDBOX installation and the SANDBOX repository only, and never
#             `belong`, `edge-devel-dimi` or `edgeappinc`: the repository you
#             name, the origin this checkout carries and the installation
#             `fugaro doctor` answers are each refused by name before anything
#             is run.
# Creates:    one catalog entry (published by `fugaro update`), one Cloud Build
#             for it and one for the repository-built image of the refutation,
#             two trivial sandbox runs and the branches and pull requests they
#             open, and two commits on the sandbox repository's default branch.
#             Every one of them is undone by the Restore, which closes the pull
#             requests, deletes their branches, takes the commits' files back
#             and removes the catalog entry again.
# Costs:      two Cloud Builds and two real runs of the sandbox repository. It
#             spends model money, so it asks: without --yes it prints the bound,
#             records a FAILed `consent` fact and stops with exit 2 before
#             anything is created.
# Runs:       by YOU. No agent runs any step of it.
# Human:      no step of this check needs a browser, a second person or a device.
#             Two commands it runs ask YOU something at the terminal and this
#             script never answers them: `fugaro image build` asks for the
#             project's name to be typed (unless the installation is managed, in
#             which case it asks nothing and its plan is the confirmation), and
#             `fugaro update` asks its own confirmations when it has any. The
#             script prints a prompt for each and waits; type the answer
#             yourself, or Ctrl-C and run the command by hand.
# Cleanup:    always, including on a Ctrl-C: the Restore of step 6 runs from the
#             exit trap. The `restore` row says what it managed to undo and
#             names anything still there with the command to settle it.
# Output:     progress on stderr, ONE block on stdout headed CHECK 291 RESULT, to
#             copy back. Exit 0 when every fact passed, 1 when one FAILed, 2
#             when the run could not tell (a guard stopped it, a tool is
#             missing, doctor did not answer).
#
# No step types, prints or pastes a credential, and none is on the image: the
# entry's two ENV lines are an example URL that resolves nowhere and
# GOOGLE_APPLICATION_CREDENTIALS=/dev/null, which is a pointer at an empty file
# and not a credential (the guard at internal/catalog's pathValue lets a
# credential-shaped name through when the line gives it a PATH, which is what
# makes this definition buildable at all). Every reading below prints presence
# or a name and never a value, and one row of the block is the transcript
# carrying no value of the image's own marker.
set -Eeuo pipefail

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is
# written to the saved descriptors rather than to whatever fd 1 is doing then.
exec 3>&1 4>&2

PROG=${0##*/}
USAGE="bash scripts/live-checks/check291.sh --repo <owner/name> --region <region> [--workflow <name>] [--entry <name>] [--from <image>] [--tags <a,b>] [--yes] [--dry-run]"

# This check's own names. The marker is an example URL that resolves nowhere and
# the reserved one points at /dev/null: the whole point of the feature is that an
# image is not a secret store, and neither of these two is a credential.
MARKER_NAME=NEXUS_BASEURL
MARKER_VALUE=https://example.invalid/repository/maven-public/
RESERVED_NAME=GOOGLE_APPLICATION_CREDENTIALS
RESERVED_VALUE=/dev/null

# How long a run this check launched is waited for, and how often the listing is
# read while it goes: `fugaro run` returns AT LAUNCH, so the transcript of step 4
# is only there once the run has ended.
RUN_WAIT_SECONDS=${CHECK291_RUN_WAIT_SECONDS:-2400}
RUN_POLL_SECONDS=${CHECK291_RUN_POLL_SECONDS:-20}

# What the Restore has to undo, and what it managed: set as the steps go, so a
# run that stopped early says what it never created instead of claiming it.
RESTORE_STATE=untouched
RESTORE_DETAIL=""
YAML_CHANGED=no
ENTRY_ADDED=no
DOCKERFILE_ADDED=no
RUNS_LAUNCHED=()
TAGS=""
FROM_IMAGE=""
CHECKOUT=""
BRANCH=""
JOB=""
ENTRY_ADDED=no
HELD_ON_JOB=no
INSTALLATION=""
GCP_PROJECT=""
CFG=""
BUCKET=""
BEFORE_ENTRIES=""
BUILT_ENTRY=""
BUILT_WORKFLOW=""
ENTRY_LINE=""
RUN1=""
RUN2=""
BRANCH1=""
BRANCH2=""
RUN_FOUND=""
RUN_STATUS=""
RESTORE_FAIL=""
FACTS_CAPTURED=""
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
REACHED_END=no
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block and cleans up, so a stop still gives you something to paste back.
stop() {
  fact aborted FAIL "$1"
  ABORT_REASON=$1
  owner "stopped: $1"
  exit 2
}

# fail_stop REASON: a reading that came out WRONG is an answer about the
# installation — the feature does not do what the rule in docs/base-image.md says
# — and this ends the run with 1, not 2. `stop` is for a guard or a tool that
# could not answer at all, which says nothing about the feature.
ANSWER=no
fail_stop() {
  ANSWER=yes
  ABORT_REASON=$1
  owner "stopped on a reading that FAILED: $1"
  exit 1
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# on_error: an INTERNAL error is a fact, never a verdict. Under `set -e` the
# first command that fails outside a guard would end the run with no fact at
# all, and the block would then read as a clean run for a run that measured
# nothing. The ERR trap records the line and exits 2.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the image's ENV" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 291 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 291 needs bash"
  need mktemp
  need python3 "the reader that parses doctor's answer and edits fugaro.yaml is one python3 file"
  need git "this check edits the sandbox checkout and commits what it edited"
  need gh "the Restore closes the pull requests the two runs open"
  need gcloud "the reserved name of step 3 is set on the job with gcloud, and the transcripts are read out of the runs bucket with it"
  need fugaro "doctor names the job's stamp, update converges both configs and run launches the two runs"
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk291-mine"
SCRATCH_REMOVED=no
PRINTED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk291.XXXXXXXX") || die "Check 291 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 291 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

# cleanup runs IN THIS SHELL, never in a subshell: restore_run records what it
# managed to do in RESTORE_STATE, and a `$(cleanup)` would throw that answer
# away with the subshell.
cleanup() {
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  restore_run || true
  remove_scratch
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  / | "$HOME" | "$HOME"/)
    say "refusing to remove a scratch directory of '$SCRATCH'"
    return 0
    ;;
  esac
  if [ ! -f "$SCRATCH/$SCRATCH_MARKER" ]; then
    say "refusing to remove $SCRATCH: it carries no marker from this check"
    return 0
  fi
  # Guarded, and its outcome recorded rather than assumed: this runs from the
  # exit trap, where a failing `rm` under `set -e` would end the shell before
  # print_block and lose the whole block.
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its last step ends honestly, whatever stopped it: a
  # block that read as a finished run would offer FAILs as a verdict about the
  # image's ENV when the steps that would have filled them never ran.
  if [ "$REACHED_END" = no ]; then
    if [ "$ANSWER" = yes ]; then
      # A reading that FAILED is an answer, and the block's own row says which
      # one: it ends the run at 1 rather than telling the owner nothing.
      if [ "$status" -eq 0 ]; then status=1; fi
    else
      if ! printf '%s' "$facts" | grep -q '^aborted|'; then
        facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured"
      fi
      if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
    fi
  fi
  local verdict=PASS
  case "$RESTORE_STATE" in
  gone | done | untouched) ;;
  *) verdict=FAIL ;;
  esac
  # The scratch sentence is composed HERE and not in restore_run: that runs
  # before cleanup, so a sentence composed there would report the directory as
  # present on every run, including the ones whose cleanup then removes it.
  if [ "$SCRATCH_REMOVED" = yes ]; then
    RESTORE_DETAIL="$RESTORE_DETAIL; the scratch directory is gone"
  elif [ -n "$SCRATCH" ]; then
    RESTORE_DETAIL="$RESTORE_DETAIL; the scratch directory is STILL THERE, at $SCRATCH — remove it yourself"
  fi
  # The Restore's verdict is composed after main returned, so it is not in the
  # count main made: a run whose only FAIL is the Restore still has to exit 1, or
  # an entry this check left published would read as a clean run.
  if [ "$verdict" = FAIL ] && [ "$status" -eq 0 ]; then
    status=1
  fi
  facts="${facts}"$'\n'"restore|$verdict|$RESTORE_DETAIL"
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

# --------------------------------------------------------------- the guards
# guard_belong NAME KIND KEY: the three names an agent's run may never touch.
# `belong` is the owner's own installation, `edge-devel-dimi` and `edgeappinc`
# are refused by check96.sh for the same reason (a workspace whose jobs, grants
# and runs are not a sandbox), and this check publishes a catalog entry,
# deploys two jobs and launches two runs, so it names all three.
BELONG_NAMES="belong edge-devel-dimi edgeappinc"
guard_belong() {
  local value=$1 kind=$2 key=$3 name
  [ -n "$value" ] || return 0
  for name in $BELONG_NAMES; do
    case "$value" in
    *"$name"*)
      fact "$key" FAIL "$kind is $value, which names $name: Check 291 publishes a catalog entry, converges two jobs and launches two runs of it, and this check is for a sandbox only"
      stop "Check 291 never runs against $name. Name a sandbox $kind: $USAGE"
      ;;
    esac
  done
  fact "$key" PASS "$kind is $value, which is none of belong, edge-devel-dimi or edgeappinc"
}

# guard_workflow_arg keeps --workflow to a workflow name: everything below hands
# it to a line editor and to `fugaro run --workflow`, and a name carrying a
# space, a colon or a slash would edit a different key than the one it names.
guard_workflow_arg() {
  case "$1" in
  "" | *[!A-Za-z0-9._-]*)
    die "--workflow takes a workflow name (letters, digits, dot, dash, underscore) and '$1' is not one. usage: $USAGE"
    ;;
  esac
}

guard_entry_arg() {
  case "$1" in
  "" | *[!A-Za-z0-9._-]*)
    die "--entry takes a catalog entry name (letters, digits, dot, dash, underscore) and '$1' is not one. usage: $USAGE"
    ;;
  esac
}

parse_args() {
  REPO=""
  REGION=""
  WORKFLOW=""
  ENTRY="fugaro-check291"
  FROM_IMAGE=""
  TAGS=""
  YES=no
  DRY_RUN=no
  while [ $# -gt 0 ]; do
    case "$1" in
    --repo)
      [ $# -ge 2 ] || die "--repo needs owner/name. usage: $USAGE"
      REPO=$2
      shift 2
      ;;
    --region)
      [ $# -ge 2 ] || die "--region needs a Cloud Run region. usage: $USAGE"
      REGION=$2
      shift 2
      ;;
    --workflow)
      [ $# -ge 2 ] || die "--workflow needs a name. usage: $USAGE"
      WORKFLOW=$2
      guard_workflow_arg "$WORKFLOW"
      shift 2
      ;;
    --entry)
      [ $# -ge 2 ] || die "--entry needs a name. usage: $USAGE"
      ENTRY=$2
      guard_entry_arg "$ENTRY"
      shift 2
      ;;
    --from)
      [ $# -ge 2 ] || die "--from needs an image reference. usage: $USAGE"
      FROM_IMAGE=$2
      shift 2
      ;;
    --tags)
      [ $# -ge 2 ] || die "--tags needs a comma-separated list. usage: $USAGE"
      TAGS=$2
      shift 2
      ;;
    --yes)
      YES=yes
      shift
      ;;
    --dry-run)
      DRY_RUN=yes
      shift
      ;;
    -h | --help)
      # The block below is a run's answer and --help is not a run: the trap that
      # prints it is off before the usage goes out.
      trap - EXIT ERR
      printf 'usage: %s\n' "$USAGE"
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  [ -n "$REPO" ] || die "--repo is required. usage: $USAGE"
  [ -n "$REGION" ] || die "--region is required (the region the sandbox's jobs are deployed in: gcloud run jobs list --region <region>). usage: $USAGE"
}

# ------------------------------------------------------------------- the reader
# One python3 file, written once, printing a fixed vocabulary for every read and
# making every edit of fugaro.yaml. Nothing here prints a value of anything: the
# readings are a fact per line and never the text they were read out of.
reader_up() {
  cat >"$SCRATCH/reader.py" <<'PY'
import json, re, sys

def die(msg):
    sys.stderr.write(msg + "\n")
    sys.exit(3)

def load_json(path):
    try:
        with open(path, "r", encoding="utf-8", errors="replace") as fh:
            return json.load(fh)
    except Exception as exc:
        die("not JSON: %s" % exc)

def rd(path):
    with open(path, "r", encoding="utf-8", errors="replace") as fh:
        return fh.read().splitlines()

def wr(path, lines):
    with open(path, "w", encoding="utf-8") as fh:
        fh.write("\n".join(lines) + "\n")

def scalar(lines, key):
    for line in lines:
        m = re.match(r"^\s*%s:\s*(.*)$" % re.escape(key), line)
        if m:
            return m.group(1).strip().strip("'\"")
    return ""

def indent_of(line):
    return len(line) - len(line.lstrip(" "))

def workflow_span(lines, name):
    """(index of the workflow's own line, one past its last line).

    The first line whose stripped text is `<name>:` inside the workflows: block
    wins; the span runs to the next non-blank line at that indent or less.
    """
    start = -1
    for i, line in enumerate(lines):
        if line.strip() == name + ":":
            start = i
            break
    if start < 0:
        return (-1, -1)
    ind = indent_of(lines[start])
    end = len(lines)
    for j in range(start + 1, len(lines)):
        if not lines[j].strip():
            continue
        if indent_of(lines[j]) <= ind:
            end = j
            break
    return (start, end)

def key_span(lines, start, end, key, ind):
    """(index, one past the last line) of `key:` at exactly indent ind."""
    for i in range(start + 1, end):
        if indent_of(lines[i]) == ind and re.match(r"^\s*%s:" % re.escape(key), lines[i]):
            j = i + 1
            while j < end and (not lines[j].strip() or indent_of(lines[j]) > ind):
                j += 1
            return (i, j)
    return (-1, -1)

def drop_key(lines, start, end, key, ind):
    i, j = key_span(lines, start, end, key, ind)
    if i < 0:
        return (lines, 0)
    return (lines[:i] + lines[j:], 1)

def put_scalar_after(lines, at, ind, key, value):
    return lines[:at + 1] + [" " * ind + "%s: %s" % (key, value)] + lines[at + 1:]

def cmd_installation(path):
    doc = load_json(path)
    proj = doc.get("project") or {}
    if not isinstance(proj, dict):
        die("doctor's project member is not an object")
    name = proj.get("name") or ""
    gcp = proj.get("gcp_project") or ""
    print("name=" + str(name))
    print("project=" + str(gcp))

def cmd_run(path):
    doc = load_json(path)
    run = str(doc.get("run") or "")
    if "/" not in run:
        die("runs show names no <slug>/<run id>")
    slug, runid = run.split("/", 1)
    print("slug=" + slug)
    print("run=" + runid)
    print("repo=" + str(doc.get("repo") or ""))

def flat(v):
    """One vocabulary line's worth of a value: a value carrying a newline would
    be read as the next line of the vocabulary otherwise."""
    return re.sub(r"\s+", " ", str(v if v is not None else "")).strip()

def cmd_launch(path):
    doc = load_json(path)
    if not isinstance(doc, dict):
        die("the launch's document is not an object")
    print("run_id=" + flat(doc.get("run_id")))
    print("run=" + flat(doc.get("run")))
    print("branch=" + flat(doc.get("branch")))
    print("status=" + flat(doc.get("status")))

def cmd_row(path, want):
    """One run's own line out of `runs ls --json`, and never another run's."""
    doc = load_json(path)
    rows = doc.get("runs") if isinstance(doc, dict) else doc
    found = "no"
    for row in rows or []:
        if not isinstance(row, dict) or flat(row.get("run_id")) != want:
            continue
        found = "yes"
        print("status=" + flat(row.get("status")))
        print("terminal=" + ("yes" if row.get("terminal") else "no"))
        print("branch=" + flat(row.get("branch")))
        break
    print("found=" + found)

def cmd_workflow(path, want):
    lines = rd(path)
    if not any(re.match(r"^workflows:", l) for l in lines):
        die("no workflows: block")
    names = []
    for line in lines:
        m = re.match(r"^(\s*)([A-Za-z0-9._-]+):\s*$", line)
        if m and indent_of(line) == 2:
            names.append(m.group(2))
    print("workflows=%d" % len(names))
    print("names=" + ",".join(names))
    if not want:
        if len(names) != 1:
            print("chosen=none")
            print("key=none")
            return
        want = names[0]
    print("chosen=" + want)
    start, end = workflow_span(lines, want)
    if start < 0:
        print("key=missing")
        return
    keys = {}
    for key in ("image_name", "dockerfile", "image", "needs"):
        keys[key] = scalar(lines[start:end], key)
    if keys["image_name"]:
        print("key=image_name")
        print("value=" + keys["image_name"])
    elif keys["dockerfile"]:
        print("key=dockerfile")
        print("value=" + keys["dockerfile"])
    elif keys["image"]:
        print("key=image")
        print("value=" + keys["image"])
    elif keys["needs"]:
        print("key=needs")
        print("value=" + keys["needs"].replace(" ", ""))
    else:
        print("key=none")
    ind = indent_of(lines[start]) + 2
    i, j = key_span(lines, start, end, "commands", ind)
    has_test = "no"
    if i >= 0:
        ti, tj = key_span(lines, i, j, "test", ind + 2)
        if ti >= 0:
            has_test = "yes"
    print("commands_test=" + has_test)

def cmd_imageenv(path, entry):
    """One line per reading of doctor's own per-job line, never the line."""
    text = rd(path)
    found = 0
    for line in text:
        m = re.search(r"(?:^|\s)image-env-([^:\s]+):\s?(.*)$", line)
        if not m:
            continue
        found += 1
        job = m.group(1)
        body = m.group(2)
        print("line=%s" % job)
        if "is not catalog-stamped" in body:
            print("%s:stamped=no" % job)
        else:
            print("%s:stamped=yes" % job)
        m2 = re.search(r"catalog entry ([^\s,]+)", body)
        print("%s:entry=%s" % (job, m2.group(1) if m2 else ""))
        m3 = re.search(r"a run drops (\d+) name\(s\) the job sets itself: ([^—]*)", body)
        if m3:
            print("%s:drops=%s" % (job, m3.group(2).strip().replace(", ", ",")))
        elif "a run drops none of the names" in body:
            print("%s:drops=none" % job)
        else:
            print("%s:drops=unknown" % job)
        caveat = "yes" if "an image's own ENV lives in the image and not in the job" in body else "no"
        print("%s:caveat=%s" % (job, caveat))
        # A value would sit right after a name and an `=`; every reading above
        # prints a name, so an `=` in the body is a value that came along.
        print("%s:valued=%s" % (job, "yes" if "=" in body else "no"))
    if found == 0:
        print("line=none")

def cmd_set_image_name(path, want, entry):
    lines = rd(path)
    start, end = workflow_span(lines, want)
    if start < 0:
        die("no workflow named %s" % want)
    ind = indent_of(lines[start]) + 2
    for key in ("image", "dockerfile"):
        lines, _ = drop_key(lines, start, end, key, ind)
        start, end = workflow_span(lines, want)
    i, _ = key_span(lines, start, end, "image_name", ind)
    if i >= 0:
        lines[i] = " " * ind + "image_name: " + entry
    else:
        lines = put_scalar_after(lines, start, ind, "image_name", entry)
    wr(path, lines)

def cmd_swap_dockerfile(path, want, dockerfile):
    """dockerfile: in place of the covering key, both edits in one write.

    validateCatalogRef refuses `dockerfile:` beside `image_name:`/`needs:`, so a
    file that kept both would be refused where it is parsed and neither the
    build nor the converge would run at all.
    """
    lines = rd(path)
    start, end = workflow_span(lines, want)
    if start < 0:
        die("no workflow named %s" % want)
    ind = indent_of(lines[start]) + 2
    for key in ("image_name", "needs", "image"):
        lines, _ = drop_key(lines, start, end, key, ind)
        start, end = workflow_span(lines, want)
    lines = put_scalar_after(lines, start, ind, "dockerfile", dockerfile)
    wr(path, lines)

def cmd_set_commands_test(path, want, body):
    lines = rd(path)
    start, end = workflow_span(lines, want)
    if start < 0:
        die("no workflow named %s" % want)
    ind = indent_of(lines[start]) + 2
    bodylines = [l for l in rd(body)]
    i, j = key_span(lines, start, end, "commands", ind)
    if i < 0:
        block = [" " * ind + "commands:",
                 " " * (ind + 2) + "test: |"] + [" " * (ind + 4) + l for l in bodylines]
        lines = lines[:end] + block + lines[end:]
    else:
        ti, tj = key_span(lines, i, j, "test", ind + 2)
        block = [" " * (ind + 2) + "test: |"] + [" " * (ind + 4) + l for l in bodylines]
        if ti >= 0:
            lines = lines[:ti] + block + lines[tj:]
        else:
            lines = lines[:i + 1] + block + lines[i + 1:]
    wr(path, lines)

def main():
    what = sys.argv[1]
    if what == "installation":
        cmd_installation(sys.argv[2])
    elif what == "run":
        cmd_run(sys.argv[2])
    elif what == "launch":
        cmd_launch(sys.argv[2])
    elif what == "row":
        cmd_row(sys.argv[2], sys.argv[3])
    elif what == "workflow":
        cmd_workflow(sys.argv[2], sys.argv[3] if len(sys.argv) > 3 else "")
    elif what == "imageenv":
        cmd_imageenv(sys.argv[2], sys.argv[3])
    elif what == "set-image-name":
        cmd_set_image_name(sys.argv[2], sys.argv[3], sys.argv[4])
    elif what == "swap-dockerfile":
        cmd_swap_dockerfile(sys.argv[2], sys.argv[3], sys.argv[4])
    elif what == "set-commands-test":
        cmd_set_commands_test(sys.argv[2], sys.argv[3], sys.argv[4])
    else:
        die("unknown reader command %s" % what)

main()
PY
  [ -s "$SCRATCH/reader.py" ] || die "Check 291 could not write its reader"
  python3 -c 'import ast,sys; ast.parse(open(sys.argv[1]).read())' "$SCRATCH/reader.py" \
    >/dev/null 2>&1 || die "Check 291's reader is not valid python3"
}

# field NAME FILE: one value out of the reader's fixed vocabulary.
field() { sed -n "s/^$1=//p" "$2" | head -1; }
# jfield NAME JOB FILE: one reading of one job's own line, same vocabulary.
jfield() { sed -n "s/^$2:$1=//p" "$3" | head -1; }
# ------------------------------------------------------------ running things
# capture OUT CMD...: run a command with its output in OUT and print its exit
# status. The `if` is what keeps `set -e` from ending the run at a read that
# failed — a failed read is a reading, and the fact composed from it says so.
capture() {
  local out=$1
  shift
  local st=0
  if ! "$@" >"$out" 2>&1; then st=${PIPESTATUS[0]:-1}; fi
  printf '%s' "$st"
}

# run_loud CMD...: run a command whose own progress you should see, keeping its
# output in $SCRATCH/last.out and returning the command's own status rather than
# tee's.
run_loud() {
  local st=0
  if ! "$@" 2>&1 | tee "$SCRATCH/last.out" >&4; then st=${PIPESTATUS[0]:-1}; fi
  return "$st"
}

# last_line PATTERN: the last line of the last loud command matching PATTERN.
last_line() { sed -n "s/^$1//p" "$SCRATCH/last.out" 2>/dev/null | tail -1; }

# yaml_scalar FILE KEY: one scalar out of a flat yaml file, for the project
# config's own fields (region, runs_bucket) — those are read, never guessed.
yaml_scalar() { sed -n "s|^ *$2: *||p" "$1" 2>/dev/null | tr -d "\"'" | head -1; }

# repo_from_origin URL: the owner/name a git remote URL names, for the three
# shapes the sandbox repository can be reached by.
repo_from_origin() {
  local url=$1
  url=${url%.git}
  case "$url" in
  git@*:*) url=${url#*:} ;;
  ssh://git@*) url=${url#ssh://git@}; url=${url#*/} ;;
  https://*) url=${url#https://}; url=${url#*/} ;;
  http://*) url=${url#http://}; url=${url#*/} ;;
  esac
  printf '%s' "$url"
}

# commit_and_push MSG PATH...: commit exactly the paths this check edited — a
# `git add -A` would carry whatever else you had in the checkout — and push them
# to the branch that was there. Never a force push: the branch is a shared one.
commit_and_push() {
  local msg=$1
  shift
  # A failed `git add` stages nothing, and the "nothing to commit" below would
  # then read as "the checkout already carries it": the caller's own success
  # would be a claim about a push that never happened.
  if ! git -C "$CHECKOUT" add -- "$@"; then
    say "git add refused the paths '$*' in $CHECKOUT (its own words are above): nothing was staged"
    return 1
  fi
  if git -C "$CHECKOUT" diff --cached --quiet; then
    say "nothing to commit for '$msg' (the checkout already carries it)"
    return 0
  fi
  git -C "$CHECKOUT" -c user.name="fugaro check291" -c user.email="check291@fugaro.invalid" \
    commit -q -m "$msg" -m "Co-Authored-By: Claude Code <noreply@anthropic.com>"
  git -C "$CHECKOUT" push -q origin "$BRANCH"
}

# doctor_all FILE: `fugaro doctor --all` into FILE, with its status.
doctor_all() { capture "$1" fugaro doctor --all; }

# transcript_of RUN OUT: the stored copy of the run's own transcript, out of the
# runs bucket. The bucket is the installation's own field, and the run's storage
# slug comes off `runs show --json` rather than being computed here.
transcript_of() {
  local run=$1 out=$2
  local st
  st=$(capture "$SCRATCH/show.json" fugaro runs show --repo "$REPO" "$run" --json)
  [ "$st" = 0 ] || return 1
  st=$(capture "$SCRATCH/show.vocab" python3 "$SCRATCH/reader.py" run "$SCRATCH/show.json")
  [ "$st" = 0 ] || return 1
  local slug runid
  slug=$(field slug "$SCRATCH/show.vocab")
  runid=$(field run "$SCRATCH/show.vocab")
  [ -n "$slug" ] && [ -n "$runid" ] || return 1
  [ -n "$BUCKET" ] || return 1
  local uris
  uris=$(gcloud storage ls --recursive "gs://$BUCKET/runs/$slug/$runid/transcripts/" 2>/dev/null |
    grep -E '/implement-[0-9]+\.jsonl$' | sort | tr '\n' ' ')
  [ -n "$uris" ] || return 1
  # shellcheck disable=SC2086
  gcloud storage cat $uris >"$out" 2>/dev/null || return 1
  [ -s "$out" ]
}

# launch_run WHAT: one trivial task on the workflow. The run and the branch it
# cut are read out of the launch's own --json document: `fugaro run` puts the run
# it launched in a reporter step for a person (on stderr, with a glyph and the
# step's own name in front of it), and the document is the one shape it prints
# that a reader can take a run id from. Sets RUN_ID and RUN_BRANCH.
launch_run() {
  local what=$1
  RUN_ID=""
  RUN_BRANCH=""
  say "launching the $what run"
  local st=0
  if ! fugaro run --workflow "$WORKFLOW" --json "$TASK_TEXT" \
    >"$SCRATCH/launch.json" 2>"$SCRATCH/launch.err"; then st=1; fi
  # Both halves reach the owner, the step lines first: what the launch printed is
  # what a refusal is read from, and it is printed whether it exited 0 or not.
  cat "$SCRATCH/launch.err" >&4 2>/dev/null || true
  cat "$SCRATCH/launch.json" >&4 2>/dev/null || true
  [ "$st" = 0 ] || return 1
  st=$(capture "$SCRATCH/launch.vocab" python3 "$SCRATCH/reader.py" launch "$SCRATCH/launch.json")
  [ "$st" = 0 ] || return 1
  RUN_ID=$(field run_id "$SCRATCH/launch.vocab")
  RUN_BRANCH=$(field branch "$SCRATCH/launch.vocab")
  [ -n "$RUN_ID" ] && [ -n "$RUN_BRANCH" ]
}

# await_run RUN: the run has to END before its transcript is read. `fugaro run`
# returns at launch and a run's transcripts are written as each stage of it ends,
# so a read taken straight after a launch looks into a run that has not run yet —
# and a row still in flight is not an answer about the image either. The
# listing's own `terminal` word is waited for, the same wait every other check
# that reads a run's own bytes makes. Returns 1 on a deadline, leaving
# RUN_FOUND/RUN_STATUS for the fact that says why.
await_run() {
  local run=$1 waited=0 st
  RUN_FOUND=""
  RUN_STATUS=""
  say "waiting for run $run to end (up to ${RUN_WAIT_SECONDS}s; fugaro runs ls --repo $REPO --json reads its own row)"
  while :; do
    st=$(capture "$SCRATCH/rows.json" fugaro runs ls --repo "$REPO" --json)
    RUN_FOUND=""
    RUN_STATUS=""
    if [ "$st" = 0 ]; then
      st=$(capture "$SCRATCH/rows.vocab" python3 "$SCRATCH/reader.py" row "$SCRATCH/rows.json" "$run")
      if [ "$st" = 0 ]; then
        RUN_FOUND=$(field found "$SCRATCH/rows.vocab")
        RUN_STATUS=$(field status "$SCRATCH/rows.vocab")
        if [ "$RUN_FOUND" = yes ] && [ "$(field terminal "$SCRATCH/rows.vocab")" = yes ]; then
          return 0
        fi
      fi
    fi
    if [ "$waited" -ge "$RUN_WAIT_SECONDS" ]; then return 1; fi
    sleep "$RUN_POLL_SECONDS"
    waited=$((waited + RUN_POLL_SECONDS))
  done
}

TASK_TEXT='create CHECK291.md at the repository root with the single line "check 291", change no other file, and run `fugaro verify test` in the foreground before you finish'

# ------------------------------------------------------------------ the steps
# step_installation asks the local config which installation it is looking at.
# Its name is what the runs bucket and the region are read out of, and `belong`
# is refused here as well as on the command line: the name the local config in
# force answers and the one a checkout names are two things, and both have to be
# a sandbox.
step_installation() {
  local st
  st=$(capture "$SCRATCH/doctor.json" fugaro doctor --json)
  if [ "$st" != 0 ]; then
    sed -n '1,5p' "$SCRATCH/doctor.json" >&4 || true
    stop "fugaro doctor --json did not answer, so this run cannot tell which installation it is looking at. Run it yourself and fix what it reports: the check needs your own credentials and a local config for the installation"
  fi
  if ! python3 "$SCRATCH/reader.py" installation "$SCRATCH/doctor.json" >"$SCRATCH/doctor.vocab" 2>"$SCRATCH/doctor.err"; then
    sed -n '1,3p' "$SCRATCH/doctor.err" >&4 || true
    stop "the answer to fugaro doctor --json is not the document this check reads (is this fugaro too old to print --json?)"
  fi
  INSTALLATION=$(field name "$SCRATCH/doctor.vocab")
  GCP_PROJECT=$(field project "$SCRATCH/doctor.vocab")
  [ -n "$INSTALLATION" ] || stop "fugaro doctor --json named no project, so this check cannot tell which installation it is looking at"
  guard_belong "$INSTALLATION" "installation" installation
  CFG="${XDG_CONFIG_HOME:-$HOME/.config}/fugaro/projects/$INSTALLATION.yaml"
  [ -r "$CFG" ] || stop "no project config at $CFG: this check reads the installation's own runs bucket and region out of that file (run 'fugaro doctor --project $INSTALLATION' and read its project line for the path)"
  BUCKET=$(yaml_scalar "$CFG" runs_bucket)
  REGION=$(yaml_scalar "$CFG" region)
  fact installation PASS "the local config in force is installation $INSTALLATION on GCP project $GCP_PROJECT, it is not belong, edge-devel-dimi or edgeappinc, and its own config names runs bucket ${BUCKET:-none} and region ${REGION:-none}"
}

# step_checkout: everything below edits the checkout it was started in, so the
# checkout has to be the repository that was named, on a branch that can be
# pushed to, with a clean tree — the check commits its own edits and a dirty tree
# would ride along in them.
step_checkout() {
  [ -f fugaro.yaml ] || stop "there is no fugaro.yaml here: run Check 291 FROM the sandbox repository's own checkout (it edits that repository's fugaro.yaml, commits and pushes what it edited, and closes the pull requests the two runs open)"
  CHECKOUT=$(git rev-parse --show-toplevel 2>/dev/null) || stop "this directory is not a git checkout, and Check 291 commits and pushes its edits"
  cd "$CHECKOUT" || stop "cannot enter $CHECKOUT"
  local origin found
  origin=$(git -C "$CHECKOUT" config --get remote.origin.url || true)
  [ -n "$origin" ] || stop "the checkout at $CHECKOUT carries no origin, so this check cannot tell which repository it would push to"
  found=$(repo_from_origin "$origin")
  guard_belong "$found" "the checkout's origin" belong
  if [ "$found" != "$REPO" ]; then
    fact checkout FAIL "the checkout at $CHECKOUT is $found and the repository named on the command line is $REPO"
    stop "the checkout and --repo are two different repositories: run from $REPO's checkout, or pass --repo $found"
  fi
  BRANCH=$(git -C "$CHECKOUT" rev-parse --abbrev-ref HEAD)
  if [ -n "$(git -C "$CHECKOUT" status --porcelain)" ]; then
    fact checkout FAIL "the checkout at $CHECKOUT has uncommitted changes"
    stop "Check 291 commits the edits it makes to $REPO's fugaro.yaml and pushes them, so an uncommitted change here would ride along in its commits: commit or stash, then run again"
  fi
  if ! git -C "$CHECKOUT" ls-remote --exit-code origin "refs/heads/$BRANCH" >/dev/null 2>&1; then
    fact checkout FAIL "the branch $BRANCH is not on origin"
    stop "Check 291 pushes to $BRANCH and the run fetches the ref from the remote, so a branch that is not on origin is not the configuration the run would read: check out a branch of $REPO that is pushed"
  fi
  # The byte-for-byte backup the Restore puts back: the check edits this file in
  # three steps and an edit it made is not something to reconstruct at the end.
  cp "$CHECKOUT/fugaro.yaml" "$SCRATCH/fugaro.yaml.bak"
  YAML_CHANGED=no
  fact checkout PASS "the checkout at $CHECKOUT is $found on branch $BRANCH with a clean tree, and its fugaro.yaml is backed up for the Restore"
}

# step_workflow: which workflow the two runs land on, and the key it uses today.
# The entry covers a workflow either because the workflow's image_name: names it
# (which this check writes in step 3) or because every tag in its needs: is one
# of the entry's tags; a workflow that already carries an image of its own is a
# stop, because this check would then be reconciling somebody's derived image and
# not reading the stamp.
step_workflow() {
  local st
  st=$(capture "$SCRATCH/wf.txt" python3 "$SCRATCH/reader.py" workflow "$CHECKOUT/fugaro.yaml" "$WORKFLOW")
  [ "$st" = 0 ] || stop "this check cannot read $CHECKOUT/fugaro.yaml's workflows: block, and which workflow the runs land on is what the whole check is about"
  local n chosen key value
  n=$(field workflows "$SCRATCH/wf.txt")
  chosen=$(field chosen "$SCRATCH/wf.txt")
  key=$(field key "$SCRATCH/wf.txt")
  value=$(field value "$SCRATCH/wf.txt")
  [ "$chosen" != none ] || stop "fugaro.yaml holds $n workflow(s) and this check needs exactly one: name the one the two runs land on with --workflow <name>. They are: $(field names "$SCRATCH/wf.txt")"
  WORKFLOW=$chosen
  if [ "$key" = missing ]; then
    fact workflow FAIL "fugaro.yaml holds no workflow named $WORKFLOW"
    stop "fugaro.yaml holds no workflow named $WORKFLOW. It holds: $(field names "$SCRATCH/wf.txt")"
  fi
  local covered=""
  case "$key" in
  image_name)
    if [ "$value" != "$ENTRY" ]; then
      fact workflow FAIL "the workflow $WORKFLOW already names image_name: $value"
      stop "the workflow $WORKFLOW already runs on $value, and this check gives it image_name: $ENTRY and then takes that key out again: name another workflow with --workflow, or take that key out yourself first"
    fi
    covered="it already names image_name: $ENTRY, which is this check's own entry"
    ;;
  dockerfile | image)
    fact workflow FAIL "the workflow $WORKFLOW already names $key: $value"
    stop "the workflow $WORKFLOW already carries $key, so it does not run on the project's catalog image and this check would be reading the stamp off a job somebody else chose the image for: name another workflow with --workflow, or take that key out yourself first"
    ;;
  needs)
    # A workflow covered by tags is a legitimate one (step 2 of the checklist):
    # the entry's tags are what covers it, and image_name: may sit beside needs:
    # without either being refused (validateCatalogRef, D1155).
    [ -n "$TAGS" ] || TAGS=$value
    covered="its needs: $value are the tags the entry is published with"
    ;;
  none) covered="it carries no image key of its own, so image_name: $ENTRY is what covers it" ;;
  esac
  local has_test
  has_test=$(field commands_test "$SCRATCH/wf.txt")
  fact workflow PASS "the two runs land on workflow $WORKFLOW: $covered. Step 3 gives it image_name: $ENTRY, step 5 replaces that key with a dockerfile: of the repository's own, step 4 adds the presence-only commands.test (the workflow has one today: $has_test), and the Restore puts all of it back"
}

# step_consent: two Cloud Builds and two real runs of the sandbox repository are
# what this check spends, and the model money is the part no reading is worth
# without your word.
step_consent() {
  local bound="one catalog entry published and built (one Cloud Build) on $INSTALLATION, two fugaro update --repo convergences of $REPO, two trivial runs of $REPO on $WORKFLOW (model spend), and the branches and pull requests those runs open — the Restore closes them, deletes their branches and takes the commits back"
  if [ "$YES" != yes ]; then
    owner "Check 291 spends: $bound."
    owner "Nothing has been created yet. Run it again with --yes to consent, or --dry-run to read the plan and call nothing at all."
    fact consent FAIL "this run was not consented to: it would spend $bound"
    stop "Check 291 builds two images and launches two runs of $REPO. Say --yes to consent (or --dry-run to read the plan and call nothing): $USAGE"
  fi
  fact consent PASS "consented with --yes: this run spends $bound"
}

# step_entry: the catalog entry, its two ENV lines and the build. The FROM is the
# base the sandbox's own jobs run today, read off `fugaro image render
# --workflow` — the derived Dockerfile the deployment would build for this very
# workflow — rather than remembered here.
step_entry() {
  local from=$FROM_IMAGE
  if [ -z "$from" ]; then
    local st
    st=$(capture "$SCRATCH/rendered.Dockerfile" fugaro image render --workflow "$WORKFLOW")
    if [ "$st" != 0 ]; then
      stop "fugaro image render --workflow $WORKFLOW did not answer, so this check cannot read the base image the sandbox's own jobs run today: run it yourself, or name the base with --from <image>"
    fi
    from=$(sed -n 's/^FROM //p' "$SCRATCH/rendered.Dockerfile" | head -1)
  fi
  [ -n "$from" ] || stop "this check could not read a FROM line for the base image: name it with --from <image> (the base the sandbox's own jobs run today, 'fugaro image ls' or the job's own image)"
  local st
  st=$(capture "$SCRATCH/ls.before" fugaro image ls)
  [ "$st" = 0 ] || stop "fugaro image ls did not answer, so this check cannot record the catalog's entries before it adds one"
  BEFORE_ENTRIES=$(grep -c . "$SCRATCH/ls.before" || true)
  cat >"$SCRATCH/check291.Dockerfile" <<EOF
FROM $from
ENV $MARKER_NAME=$MARKER_VALUE
ENV $RESERVED_NAME=$RESERVED_VALUE
EOF
  say "the entry's Dockerfile (no credential in it, and no secret-shaped value):"
  sed -n '1,5p' "$SCRATCH/check291.Dockerfile" >&4
  local tags=()
  if [ -n "$TAGS" ]; then tags=(--tags "$TAGS"); fi
  if ! run_loud fugaro image add "$ENTRY" --inline "$SCRATCH/check291.Dockerfile" ${tags[@]+"${tags[@]}"}; then
    fact entry FAIL "fugaro image add $ENTRY did not exit 0"
    stop "fugaro image add $ENTRY failed (its last lines are above). It writes the project's configuration only and builds nothing, so nothing has been built yet: fix what it reported and run again"
  fi
  ENTRY_ADDED=yes
  owner "next: fugaro image build $ENTRY asks YOU for the project's name to be typed at this terminal (unless $INSTALLATION's marks say it is managed, in which case it asks nothing and its plan is the confirmation). This script never answers it."
  if ! run_loud fugaro image build "$ENTRY"; then
    fact entry FAIL "fugaro image build $ENTRY did not exit 0"
    stop "fugaro image build $ENTRY failed (its last lines are above). The entry is in the project's configuration and the Restore removes it again, so nothing is left behind: fix what it reported and run again"
  fi
  BUILT_ENTRY=$(last_line 'built ')
  owner "next: fugaro update publishes the catalog. Answer its own confirmations at this terminal yourself; this script never answers one."
  if ! run_loud fugaro update; then
    fact entry FAIL "fugaro update did not exit 0, so the catalog entry was not published"
    stop "fugaro update failed (its last lines are above): the entry is written in the project's configuration and not published, and the Restore removes it again"
  fi
  st=$(capture "$SCRATCH/ls.after" fugaro image ls)
  [ "$st" = 0 ] || stop "fugaro image ls did not answer after the build"
  ENTRY_LINE=$(grep -m1 -E "(^|[[:space:]])$ENTRY([[:space:]]|$)" "$SCRATCH/ls.after" || true)
  if [ -z "$ENTRY_LINE" ]; then
    fact entry FAIL "fugaro image ls does not carry a line for $ENTRY"
    stop "fugaro image ls carries no line for $ENTRY after the build, so the catalog does not hold the entry this check is about (the catalog had $BEFORE_ENTRIES entries before it added one)"
  fi
  fact entry PASS "catalog entry $ENTRY is published and built: $ENTRY_LINE (the catalog had $BEFORE_ENTRIES entries before this check added one); the build printed ${BUILT_ENTRY:-nothing this reader recognises}"
  if [ -z "$BUILT_ENTRY" ]; then
    fact entry FAIL "the build printed no 'built <image>@<digest>' line this check recognises"
    say "the build's last lines are in $SCRATCH/last.out"
  fi
}

# step_stamp: image_name: on the workflow, the commit and push it takes (the run
# fetches the ref from the remote, so an uncommitted edit is not the
# configuration the run reads), the converge, and doctor's own per-job line.
step_stamp() {
  local st
  st=$(capture "$SCRATCH/edit.err" python3 "$SCRATCH/reader.py" set-image-name "$CHECKOUT/fugaro.yaml" "$WORKFLOW" "$ENTRY")
  [ "$st" = 0 ] || stop "this check could not write image_name: $ENTRY into $WORKFLOW in $CHECKOUT/fugaro.yaml (its reason is above)"
  YAML_CHANGED=yes
  if ! commit_and_push "check291: run $WORKFLOW on catalog entry $ENTRY" fugaro.yaml; then
    fact stamp FAIL "the commit or the push of image_name: $ENTRY did not succeed"
    stop "the commit or the push of the image_name: edit did not succeed (git's own words are above): the run reads the configuration off the remote, so nothing below would read what this step wrote"
  fi
  owner "next: fugaro update --repo $REPO converges the repository's resources, which is what writes the stamp on its workflow job."
  if ! run_loud fugaro update --repo "$REPO"; then
    fact stamp FAIL "fugaro update --repo $REPO did not exit 0, so the job was not converged from the configuration that names $ENTRY"
    stop "fugaro update --repo $REPO failed (its last lines are above). The run below would then land on a job that still carries the previous image, so nothing would be read about the stamp"
  fi
  read_doctor
  JOB=""
  local entry
  while IFS= read -r job; do
    entry=$(jfield entry "$job" "$SCRATCH/doctor.vocab")
    [ "$entry" = "$ENTRY" ] || continue
    JOB=$job
    break
  done < <(sed -n 's/^line=//p' "$SCRATCH/doctor.vocab")
  if [ -z "$JOB" ]; then
    fact stamp FAIL "no job of this installation carries the stamp for catalog entry $ENTRY"
    stop "fugaro doctor --all has no line naming catalog entry $ENTRY, so the job this workflow runs on was not converged from the configuration that names it. Read the whole report yourself: 'fugaro doctor --all'"
  fi
  local caveat valued drops
  caveat=$(jfield caveat "$JOB" "$SCRATCH/doctor.vocab")
  valued=$(jfield valued "$JOB" "$SCRATCH/doctor.vocab")
  drops=$(jfield drops "$JOB" "$SCRATCH/doctor.vocab")
  # One row per key, never two: the block prints the FIRST line for a key, so a
  # FAIL written beside a PASS would be dropped and the run would read as clean.
  local verdict=PASS why=""
  if [ "$caveat" != yes ]; then
    verdict=FAIL
    why="$why; the line carries no caveat that an image's own ENV is not in it, and without it 'a run drops none of the names the job sets itself' reads as 'everything the image declares reaches a run'"
  fi
  if [ "$valued" = yes ]; then
    verdict=FAIL
    why="$why; the line carries a 'name=value' pair, and every reading of this feature prints a name and never a value (D1375)"
  fi
  fact stamp "$verdict" "the job's own line is image-env-$JOB: the stamp names catalog entry $ENTRY, a run drops $drops of the names the job sets itself, the caveat that an image's own ENV is not in the line is there: $caveat, and the line prints no value: no-assignment=$([ "$valued" = no ] && echo yes || echo no)${why}"
}

# read_doctor: `fugaro doctor --all` into the reader's own vocabulary.
read_doctor() {
  local st
  st=$(doctor_all "$SCRATCH/doctor-all.txt")
  [ "$st" = 0 ] || stop "fugaro doctor --all did not answer, and this check reads the job's own line out of it"
  st=$(capture "$SCRATCH/doctor.vocab" python3 "$SCRATCH/reader.py" imageenv "$SCRATCH/doctor-all.txt" "$ENTRY")
  [ "$st" = 0 ] || stop "this check cannot read fugaro doctor --all's own lines"
  case "$(field line "$SCRATCH/doctor.vocab")" in
  none) stop "fugaro doctor --all printed no 'image-env-<job>:' line at all, so this installation's doctor does not report the stamp (is this fugaro too old for it?)" ;;
  esac
}

# step_reserved: one reserved name set ON THE JOB by hand — the merge form,
# because the replace form would take Fugaro's own variables off the job — and
# doctor's line read again. This is the reading the checklist's step 3 makes,
# and the reason the report can say anything about the reservation at all: an
# image's own ENV is in the image and no read of a job can list it, so the
# reservation shows up in the line only where a reserved name sits on the job.
step_reserved() {
  if ! run_loud gcloud run jobs update "$JOB" --region "$REGION" \
    --update-env-vars "$RESERVED_NAME=$RESERVED_VALUE"; then
    fact reserved FAIL "gcloud run jobs update $JOB did not exit 0, so the reserved name was not set on the job"
    stop "gcloud run jobs update $JOB --region $REGION --update-env-vars $RESERVED_NAME=$RESERVED_VALUE failed (its last lines are above). The step exists to read the reservation out of doctor's line with a reserved name in the job's own environment"
  fi
  HELD_ON_JOB=yes
  read_doctor
  local entry drops valued
  entry=$(jfield entry "$JOB" "$SCRATCH/doctor.vocab")
  drops=$(jfield drops "$JOB" "$SCRATCH/doctor.vocab")
  valued=$(jfield valued "$JOB" "$SCRATCH/doctor.vocab")
  local verdict=PASS why=""
  if [ "$entry" != "$ENTRY" ] || [ "$drops" = none ] || [ "${drops#*$RESERVED_NAME}" = "$drops" ]; then
    verdict=FAIL
    why="the line did not name $RESERVED_NAME among the names a run drops (it reads: entry $entry, drops $drops), although the name is set on the job by hand"
  fi
  if [ "$valued" = yes ]; then
    verdict=FAIL
    why="$why; the line carries a 'name=value' pair, and no reading of this feature prints a value (D1375)"
  fi
  if [ "$verdict" = FAIL ]; then
    fact reserved FAIL "the line for job $JOB reads: $why"
    fail_stop "fugaro doctor --all did not read the reservation the way this check needs it: $why. Read the report yourself: 'fugaro doctor --all'"
  fi
  fact reserved PASS "with $RESERVED_NAME set on job $JOB by hand, its line names it among the names a run drops: $drops — and the line still prints no value: no-assignment=$([ "$valued" = no ] && echo yes || echo no)"
}

# step_converged: the same converge again, which redeploys the job from the
# deployment's own environment and so takes the hand-set name off it.
step_converged() {
  if ! run_loud fugaro update --repo "$REPO"; then
    fact converged FAIL "the second fugaro update --repo $REPO did not exit 0"
    stop "the second fugaro update --repo $REPO failed (its last lines are above), so the reserved name set on $JOB by hand was not converged away. Take it off yourself with 'gcloud run jobs update $JOB --region $REGION --remove-env-vars $RESERVED_NAME'"
  fi
  read_doctor
  local drops
  drops=$(jfield drops "$JOB" "$SCRATCH/doctor.vocab")
  if [ "${drops#*$RESERVED_NAME}" != "$drops" ]; then
    fact converged FAIL "the line for $JOB still names $RESERVED_NAME among the names a run drops after the converge: $drops"
    fail_stop "the converge did not take $RESERVED_NAME off job $JOB: take it off yourself with 'gcloud run jobs update $JOB --region $REGION --remove-env-vars $RESERVED_NAME'"
  fi
  HELD_ON_JOB=no
  fact converged PASS "the converge redeployed job $JOB from the deployment's own environment and the hand-set $RESERVED_NAME is gone from its line: drops $drops"
}

# step_run_marker: one trivial run whose own commands.test prints presence and
# never a value, read out of the stored transcript. Both lines are committed and
# pushed first: the run fetches the ref from the remote, so an edit sitting in
# this checkout is not the configuration the run reads.
step_run_marker() {
  cat >"$SCRATCH/commands-test.body" <<'EOF'
set -eu
if [ -n "${NEXUS_BASEURL:-}" ]; then echo "marker: present"; else echo "marker: absent"; fi
if [ -n "${GOOGLE_APPLICATION_CREDENTIALS:-}" ]; then echo "reserved: present"; else echo "reserved: absent"; fi
EOF
  local st
  st=$(capture "$SCRATCH/edit.err" python3 "$SCRATCH/reader.py" set-commands-test "$CHECKOUT/fugaro.yaml" "$WORKFLOW" "$SCRATCH/commands-test.body")
  [ "$st" = 0 ] || stop "this check could not write $WORKFLOW's commands.test into $CHECKOUT/fugaro.yaml (its reason is above)"
  YAML_CHANGED=yes
  if ! commit_and_push "check291: print the marker's presence from $WORKFLOW's test command" fugaro.yaml; then
    fact run-marker FAIL "the commit or the push of commands.test did not succeed"
    stop "the commit or the push of the commands.test edit did not succeed (git's own words are above): the run reads its configuration off the remote, and a test command that never arrived would print nothing at all"
  fi
  if ! launch_run "first"; then
    fact run-marker FAIL "fugaro run --workflow $WORKFLOW did not name the run it launched"
    stop "fugaro run --workflow $WORKFLOW did not print a --json document naming the run it launched and the branch it cut (its own output is above), so this check has no run to read the transcript of"
  fi
  RUN1=$RUN_ID
  BRANCH1=$RUN_BRANCH
  RUNS_LAUNCHED+=("$BRANCH1")
  if ! await_run "$RUN1"; then
    fact run-marker FAIL "run $RUN1 had not ended within ${RUN_WAIT_SECONDS}s (its row reads found=${RUN_FOUND:-nothing}, status=${RUN_STATUS:-nothing})"
    stop "run $RUN1 had not ended after ${RUN_WAIT_SECONDS}s, and the transcript this step reads is written as the run's stages end: a reading taken now would be a reading of a run that has not run yet. Watch it with 'fugaro watch $RUN1' and run this check again when it has ended — the run is a real one and is not cancelled by stopping here. Raise CHECK291_RUN_WAIT_SECONDS if it needs longer"
  fi
  if ! transcript_of "$RUN1" "$SCRATCH/run1.jsonl"; then
    fact run-marker FAIL "the transcript of run $RUN1 could not be read out of the runs bucket"
    stop "this check could not read run $RUN1's stored transcript (transcripts/implement-<n>.jsonl under its prefix in gs://${BUCKET:-<bucket>}/runs/...). Read it yourself with 'fugaro logs $RUN1' and 'gcloud storage cat', and check the run with 'fugaro runs show --repo $REPO $RUN1'"
  fi
  grep -q 'marker: present' "$SCRATCH/run1.jsonl" || {
    fact run-marker FAIL "run $RUN1's transcript does not carry 'marker: present'"
    fail_stop "run $RUN1's transcript carries no 'marker: present', so the catalog entry's own ENV did not reach the environment of a command the run ran through fugaro verify. Read it yourself: 'fugaro logs $RUN1'"
  }
  grep -q 'reserved: absent' "$SCRATCH/run1.jsonl" || {
    fact run-marker FAIL "run $RUN1's transcript does not carry 'reserved: absent'"
    fail_stop "run $RUN1's transcript carries no 'reserved: absent', although the entry's Dockerfile sets $RESERVED_NAME: it reached the agent's environment, which is the one thing the passthrough exists to prevent. Read it yourself: 'fugaro logs $RUN1'"
  }
  fact run-marker PASS "run $RUN1 of workflow $WORKFLOW read 'marker: present' — the entry's own ENV reached the environment of a command the run ran through fugaro verify — and 'reserved: absent', although the entry's Dockerfile sets $RESERVED_NAME"
}

# step_run_value: the image's value appears nowhere in the transcript. Fugaro
# prints no value of an image's ENV (D1375) and the check's own commands print
# presence and never a value, which is the only way this reading can be true of a
# value that is there.
step_run_value() {
  if grep -q 'example.invalid' "$SCRATCH/run1.jsonl"; then
    fact run-value FAIL "run $RUN1's transcript carries the entry's own marker value"
    fail_stop "run $RUN1's transcript carries 'example.invalid', which is the value the entry's Dockerfile sets: something printed a value of the image's ENV, and D1375 says no reading of it ever does"
  fi
  if grep -qE "$RESERVED_NAME=[^ ]" "$SCRATCH/run1.jsonl"; then
    fact run-value FAIL "run $RUN1's transcript carries an assignment to $RESERVED_NAME"
    fail_stop "run $RUN1's transcript carries an assignment to $RESERVED_NAME, and the line this check reads off the agent's environment names it without a value"
  fi
  fact run-value PASS "run $RUN1's transcript carries no 'example.invalid' anywhere and no $RESERVED_NAME=value pair: the image's value reached nothing that prints"
}

# step_refutation: the same workflow on a repository-built image, and the half a
# code review cannot settle (D1370). The covering key comes out in the same edit
# the dockerfile: goes in — validateCatalogRef refuses the two together — and
# `fugaro image build --workflow` is load-bearing: a bare `fugaro image build`
# builds CATALOG entries, so it would rebuild this check's own entry and leave
# .fugaro/<workflow>.Dockerfile unbuilt, and the 'marker: absent' below could
# then be read off a stale image that never carried the marker.
step_refutation() {
  local st
  st=$(capture "$SCRATCH/rendered.Dockerfile" fugaro image render --workflow "$WORKFLOW")
  [ "$st" = 0 ] || stop "fugaro image render --workflow $WORKFLOW did not answer, and the repository-built Dockerfile of the refutation is what it prints"
  mkdir -p "$CHECKOUT/.fugaro"
  cp "$SCRATCH/rendered.Dockerfile" "$CHECKOUT/.fugaro/$WORKFLOW.Dockerfile"
  cat >>"$CHECKOUT/.fugaro/$WORKFLOW.Dockerfile" <<EOF
ENV $MARKER_NAME=$MARKER_VALUE
ENV $RESERVED_NAME=$RESERVED_VALUE
EOF
  DOCKERFILE_ADDED=yes
  st=$(capture "$SCRATCH/edit.err" python3 "$SCRATCH/reader.py" swap-dockerfile "$CHECKOUT/fugaro.yaml" "$WORKFLOW" ".fugaro/$WORKFLOW.Dockerfile")
  [ "$st" = 0 ] || stop "this check could not give $WORKFLOW a dockerfile: of its own in $CHECKOUT/fugaro.yaml (its reason is above)"
  YAML_CHANGED=yes
  if ! commit_and_push "check291: build $WORKFLOW's image from the repository's own Dockerfile" fugaro.yaml ".fugaro/$WORKFLOW.Dockerfile"; then
    fact refutation FAIL "the commit or the push of the repository-built Dockerfile did not succeed"
    stop "the commit or the push of the refutation's Dockerfile did not succeed (git's own words are above): the build below reads the definition off the remote"
  fi
  owner "next: fugaro image build --workflow $WORKFLOW builds the repository's own image (the --workflow is what makes it the workflow's image and not this check's catalog entry). Answer its own confirmations at this terminal yourself."
  if ! run_loud fugaro image build --workflow "$WORKFLOW"; then
    fact refutation FAIL "fugaro image build --workflow $WORKFLOW did not exit 0"
    stop "fugaro image build --workflow $WORKFLOW failed (its last lines are above), so there is no repository-built image to read the refutation off"
  fi
  BUILT_WORKFLOW=$(last_line 'built ')
  if ! run_loud fugaro update --repo "$REPO"; then
    fact refutation FAIL "fugaro update --repo $REPO did not exit 0 after the repository-built image was built"
    stop "fugaro update --repo $REPO failed (its last lines are above), so the workflow's job was not converged onto the repository-built image"
  fi
  local image
  image=$(gcloud run jobs describe "$JOB" --region "$REGION" --format='value(spec.template.template.containers[0].image)' 2>/dev/null | head -1 || true)
  if [ -z "$image" ]; then
    fact refutation FAIL "gcloud run jobs describe $JOB answered no image"
    stop "gcloud run jobs describe $JOB --region $REGION answered no image for the job, so this check cannot say whether it carries the repository-built one and not the catalog entry's (is the region $REGION the one the job is in?)"
  fi
  case "$image" in
  *"catalog-$ENTRY"*)
    fact refutation FAIL "job $JOB still carries the catalog entry's image ($image)"
    fail_stop "job $JOB carries the catalog entry's own image after the converge, so the refutation below would be read off the wrong image: the --workflow of 'fugaro image build --workflow $WORKFLOW' is what builds the repository's, and D1144 says a bare 'fugaro image build' builds catalog entries"
    ;;
  esac
  read_doctor
  local stamped entry
  stamped=$(jfield stamped "$JOB" "$SCRATCH/doctor.vocab")
  entry=$(jfield entry "$JOB" "$SCRATCH/doctor.vocab")
  if [ "$stamped" != no ]; then
    fact refutation FAIL "doctor's line for $JOB still reads catalog-stamped (entry $entry) after the converge onto the repository-built image"
    fail_stop "fugaro doctor --all still reports job $JOB as catalog-stamped after it was converged onto the repository's own image, and the stamp is what the whole check rests on: read it yourself with 'fugaro doctor --all'"
  fi
  local why=""
  if [ "$entry" = "$ENTRY" ]; then
    why="; the line still names $ENTRY although it reads not catalog-stamped, and the two cannot both be true of one job"
  fi
  fact refutation "${why:+FAIL}${why:-PASS}" "the workflow now runs on the repository's own image ($image, its own build printed ${BUILT_WORKFLOW:-nothing this reader recognises}), which is not the entry's catalog-$ENTRY one, and doctor's line for $JOB reads not catalog-stamped: no project image_catalog entry chose it${why}"
}

# step_refutation_run: the second run, on the repository-built image, reading the
# same marker absent. That is D1370's scope: a repository-built image passes none
# of its image's own variables.
step_refutation_run() {
  if ! launch_run "refutation"; then
    fact refutation-run FAIL "fugaro run --workflow $WORKFLOW did not name the run it launched"
    stop "the second fugaro run --workflow $WORKFLOW did not print a --json document naming the run it launched and the branch it cut (its own output is above)"
  fi
  RUN2=$RUN_ID
  BRANCH2=$RUN_BRANCH
  RUNS_LAUNCHED+=("$BRANCH2")
  if ! await_run "$RUN2"; then
    fact refutation-run FAIL "run $RUN2 had not ended within ${RUN_WAIT_SECONDS}s (its row reads found=${RUN_FOUND:-nothing}, status=${RUN_STATUS:-nothing})"
    stop "run $RUN2 had not ended after ${RUN_WAIT_SECONDS}s, and the transcript this step reads is written as the run's stages end. Watch it with 'fugaro watch $RUN2' and run this check again when it has ended — the run is a real one and is not cancelled by stopping here. Raise CHECK291_RUN_WAIT_SECONDS if it needs longer"
  fi
  if ! transcript_of "$RUN2" "$SCRATCH/run2.jsonl"; then
    fact refutation-run FAIL "the transcript of run $RUN2 could not be read out of the runs bucket"
    stop "this check could not read run $RUN2's stored transcript. Read it yourself: 'fugaro logs $RUN2'"
  fi
  grep -q 'marker: absent' "$SCRATCH/run2.jsonl" || {
    fact refutation-run FAIL "run $RUN2's transcript does not carry 'marker: absent'"
    fail_stop "run $RUN2's transcript carries no 'marker: absent', although its image is the repository's own and D1370 says such an image passes none of its own variables: read it yourself with 'fugaro logs $RUN2'"
  }
  grep -q 'reserved: absent' "$SCRATCH/run2.jsonl" || {
    fact refutation-run FAIL "run $RUN2's transcript does not carry 'reserved: absent'"
    fail_stop "run $RUN2's transcript carries no 'reserved: absent': read it yourself with 'fugaro logs $RUN2'"
  }
  fact refutation-run PASS "run $RUN2 of the same workflow on the repository-built image read 'marker: absent' and 'reserved: absent' — the same marker, set the same way in the repository's own Dockerfile, reached no run at all"
}

# --------------------------------------------------------------- the Restore
# restore_run undoes what this check created, in the order the commands allow,
# and runs from the exit trap on EVERY ending — a failed assertion or a Ctrl-C
# included. Every call is guarded and its outcome recorded: this runs under
# `set -e` from a trap, where an unguarded failure would end the shell before
# print_block and lose the whole block. It uses no `set -e` behaviour it cannot
# survive, and it never force-pushes: the branch is the sandbox repository's own.
restore_run() {
  RESTORE_FAIL=""
  if [ -z "$CHECKOUT" ]; then
    RESTORE_STATE=untouched
    RESTORE_DETAIL="the run stopped before it entered a checkout, so it created nothing to undo"
    return 0
  fi
  if [ "$YAML_CHANGED" = no ] && [ "$DOCKERFILE_ADDED" = no ] && [ "$ENTRY_ADDED" = no ] && [ "${#RUNS_LAUNCHED[@]}" -eq 0 ]; then
    RESTORE_STATE=untouched
    RESTORE_DETAIL="this run created nothing — no catalog entry, no commit, no run — so there is nothing to restore"
    return 0
  fi
  local detail=""
  # 1. The repository's own configuration: the byte-for-byte backup, and the
  #    Dockerfile step 5 added. Both go back in one commit, and `fugaro image rm`
  #    below is refused while a workflow still names the entry, which is why this
  #    part comes first.
  if [ "$YAML_CHANGED" = yes ] && [ -f "$SCRATCH/fugaro.yaml.bak" ]; then
    if cp "$SCRATCH/fugaro.yaml.bak" "$CHECKOUT/fugaro.yaml" 2>/dev/null; then
      detail="$detail; $REPO's fugaro.yaml is back byte-for-byte"
    else
      RESTORE_FAIL="$RESTORE_FAIL put $SCRATCH/fugaro.yaml.bak back at $CHECKOUT/fugaro.yaml;"
    fi
  fi
  if [ "$DOCKERFILE_ADDED" = yes ]; then
    rm -f "$CHECKOUT/.fugaro/$WORKFLOW.Dockerfile" 2>/dev/null || true
    if [ -e "$CHECKOUT/.fugaro/$WORKFLOW.Dockerfile" ]; then
      RESTORE_FAIL="$RESTORE_FAIL remove $CHECKOUT/.fugaro/$WORKFLOW.Dockerfile;"
    else
      detail="$detail; .fugaro/$WORKFLOW.Dockerfile is gone"
    fi
  fi
  if [ "$YAML_CHANGED" = yes ]; then
    # Only the paths this run really created: `.fugaro/<workflow>.Dockerfile` is
    # step 5's, and `git add` of a path that is not there stages NOTHING and
    # fails, so a run that ended before step 5 would push no restoration at all
    # and leave the branch naming the entry this Restore removes in part 3.
    local paths=(fugaro.yaml)
    [ "$DOCKERFILE_ADDED" = yes ] && paths+=(".fugaro/$WORKFLOW.Dockerfile")
    if commit_and_push "check291: restore $WORKFLOW's own configuration" "${paths[@]}" >/dev/null 2>&1; then
      detail="$detail; the check's own edits are committed back out on $BRANCH"
    else
      RESTORE_FAIL="$RESTORE_FAIL commit and push the restoration of ${paths[*]} on branch $BRANCH;"
    fi
  fi
  # 2. The convergence that redeploys the job from the restored configuration —
  #    which is also what takes a name step 3 set by hand off the job.
  if [ "$YAML_CHANGED" = yes ]; then
    if fugaro update --repo "$REPO" >"$SCRATCH/restore.update" 2>&1; then
      detail="$detail; fugaro update --repo $REPO converged the job from the restored configuration"
    else
      RESTORE_FAIL="$RESTORE_FAIL run 'fugaro update --repo $REPO' (its output is in $SCRATCH/restore.update);"
    fi
  fi
  # 3. The catalog entry itself, then the publish that takes it off the project.
  if [ "$ENTRY_ADDED" = yes ]; then
    if fugaro image rm "$ENTRY" >"$SCRATCH/restore.rm" 2>&1 && fugaro update >"$SCRATCH/restore.publish" 2>&1; then
      detail="$detail; catalog entry $ENTRY is removed and fugaro update published the catalog without it"
    else
      RESTORE_FAIL="$RESTORE_FAIL remove the catalog entry ('fugaro image rm $ENTRY' then 'fugaro update'; their output is in $SCRATCH/restore.rm and $SCRATCH/restore.publish);"
    fi
  fi
  # 4. The pull requests the two runs opened and the branches they cut. `gh`
  #    takes a branch name here, so the run record is not parsed for a number.
  local branch state n=0
  for branch in ${RUNS_LAUNCHED[@]+"${RUNS_LAUNCHED[@]}"}; do
    n=$((n + 1))
    if ! gh pr close "$branch" --delete-branch >"$SCRATCH/restore.pr$n" 2>&1; then
      RESTORE_FAIL="$RESTORE_FAIL close the pull request of $branch ('gh pr close $branch --delete-branch' in $CHECKOUT);"
      continue
    fi
    state=$(gh pr view "$branch" --json state -q .state 2>/dev/null || true)
    if [ "$state" != CLOSED ]; then
      RESTORE_FAIL="$RESTORE_FAIL read $branch's pull request back as CLOSED (it answered '${state:-nothing}');"
      continue
    fi
    if git -C "$CHECKOUT" ls-remote --exit-code origin "refs/heads/$branch" >/dev/null 2>&1; then
      RESTORE_FAIL="$RESTORE_FAIL delete the branch $branch on origin (its pull request is closed and the branch is still there);"
      continue
    fi
    detail="$detail; $branch's pull request is CLOSED and its branch is gone"
  done
  # 5. Doctor's own read of the converged job: the entry is off it, and no
  #    reserved name this check set by hand is left on it. A run that stopped
  #    before step 3 never set one, and a line that reads not catalog-stamped is
  #    the state step 1 recorded.
  if [ "$YAML_CHANGED" = yes ]; then
    local dgot vgot
    dgot=$(doctor_all "$SCRATCH/restore.doctor")
    vgot=$(capture "$SCRATCH/restore.vocab" python3 "$SCRATCH/reader.py" imageenv "$SCRATCH/restore.doctor" "$ENTRY")
    if [ "$dgot" = 0 ] && [ "$vgot" = 0 ]; then
      local job entry drops still=""
      for job in $(sed -n 's/^line=//p' "$SCRATCH/restore.vocab"); do
        entry=$(jfield entry "$job" "$SCRATCH/restore.vocab")
        drops=$(jfield drops "$job" "$SCRATCH/restore.vocab")
        case "$entry" in
        "$ENTRY") still="$still the stamp still names $ENTRY on job $job;" ;;
        esac
        case "$drops" in
        *"$RESERVED_NAME"*)
          if [ "$HELD_ON_JOB" = yes ]; then
            still="$still $RESERVED_NAME is still set on job $job — take it off with 'gcloud run jobs update $job --region $REGION --remove-env-vars $RESERVED_NAME';"
          fi
          ;;
        esac
      done
      if [ -n "$still" ]; then
        RESTORE_FAIL="$RESTORE_FAIL$still"
      else
        detail="$detail; doctor's lines name no $ENTRY and no hand-set $RESERVED_NAME"
      fi
    else
      RESTORE_FAIL="$RESTORE_FAIL read 'fugaro doctor --all' back to confirm the job carries no stamp for $ENTRY;"
    fi
  fi
  if [ -z "$RESTORE_FAIL" ]; then
    RESTORE_STATE=done
    RESTORE_DETAIL="this check's own things are undone: ${detail#; }. What is NOT undone is the fugaro update it ran, which is the point of it — and one ordinary run of $REPO is yours to make, since this check launches no third run"
  else
    RESTORE_STATE=STILL_THERE
    RESTORE_DETAIL="this check undid part of what it created: ${detail#; }. STILL TO DO, by hand, in $CHECKOUT: $RESTORE_FAIL"
  fi
}

# ------------------------------------------------------------------- the plan
# print_plan: every command the run below would make, in order, with nothing
# called. It is the --dry-run's whole answer, and the reason a dry run needs no
# verdict: what it prints is what would happen, not what did.
print_plan() {
  local out=3
  printf 'CHECK 291 PLAN — every command the run makes, and NOTHING was called for this plan\n' >&$out
  printf '  1. fugaro doctor --json                       # which installation, refused if it is belong\n' >&$out
  printf '     read %s for runs_bucket and region\n' "${CFG:-<the project config of the installation>}" >&$out
  printf '  2. fugaro image render --workflow %s    # the base image the sandbox jobs run today\n' "$WORKFLOW" >&$out
  printf '     fugaro image ls                          # the catalog before (%s entries)\n' "${BEFORE_ENTRIES:-unknown}" >&$out
  printf '     write %s/check291.Dockerfile: FROM <base>, ENV %s=%s, ENV %s=%s\n' "$SCRATCH" "$MARKER_NAME" "$MARKER_VALUE" "$RESERVED_NAME" "$RESERVED_VALUE" >&$out
  printf '     fugaro image add %s --inline <that file>%s\n' "$ENTRY" "${TAGS:+ --tags $TAGS}" >&$out
  printf '     fugaro image build %s                    # ONE Cloud Build; asks YOU for the project name\n' "$ENTRY" >&$out
  printf '     fugaro update                            # publishes the catalog; its confirmations are YOURS\n' >&$out
  printf '  3. edit %s/fugaro.yaml: %s gets image_name: %s\n' "$CHECKOUT" "$WORKFLOW" "$ENTRY" >&$out
  printf '     git commit + git push origin %s\n' "${BRANCH:-<branch>}" >&$out
  printf '     fugaro update --repo %s             # writes the stamp on the workflow job\n' "$REPO" >&$out
  printf '     fugaro doctor --all                      # the job line must name %s, drops none, no value\n' "$ENTRY" >&$out
  printf '     gcloud run jobs update <job> --region %s --update-env-vars %s=%s\n' "$REGION" "$RESERVED_NAME" "$RESERVED_VALUE" >&$out
  printf '     fugaro doctor --all                      # the line must name %s among the dropped names\n' "$RESERVED_NAME" >&$out
  printf '     fugaro update --repo %s             # converges the hand-set name away again\n' "$REPO" >&$out
  printf '  4. edit %s/fugaro.yaml: %s gets a commands.test printing presence and never a value\n' "$CHECKOUT" "$WORKFLOW" >&$out
  printf '     git commit + git push origin %s\n' "${BRANCH:-<branch>}" >&$out
  printf '     fugaro run --workflow %s --json %s\n' "$WORKFLOW" "'<the trivial task: CHECK291.md, then fugaro verify test>'" >&$out
  printf '     fugaro runs ls --repo %s --json            # waited for the run to END (terminal), up to %ss\n' "$REPO" "$RUN_WAIT_SECONDS" >&$out
  printf '     fugaro runs show --repo %s <run> --json ; gcloud storage ls/cat the transcript\n' "$REPO" >&$out
  printf '     read: marker: present, reserved: absent, and example.invalid NOWHERE\n' >&$out
  printf '  5. fugaro image render --workflow %s > .fugaro/%s.Dockerfile (+ the two ENV lines)\n' "$WORKFLOW" "$WORKFLOW" >&$out
  printf '     edit %s/fugaro.yaml: image_name: out, dockerfile: .fugaro/%s.Dockerfile in (one edit)\n' "$CHECKOUT" "$WORKFLOW" >&$out
  printf '     git commit + git push origin %s\n' "${BRANCH:-<branch>}" >&$out
  printf '     fugaro image build --workflow %s    # the --workflow is load-bearing (D1144): ONE Cloud Build\n' "$WORKFLOW" >&$out
  printf '     fugaro update --repo %s ; gcloud run jobs describe <job> --region %s\n' "$REPO" "$REGION" >&$out
  printf '     fugaro doctor --all                      # the job must read NOT catalog-stamped\n' >&$out
  printf '     fugaro run --workflow %s --json %s\n' "$WORKFLOW" "'<the same trivial task>'" >&$out
  printf '     fugaro runs ls --repo %s --json            # waited for the run to END (terminal), up to %ss\n' "$REPO" "$RUN_WAIT_SECONDS" >&$out
  printf '     fugaro runs show --repo %s <run> --json ; gcloud storage ls/cat the transcript\n' "$REPO" >&$out
  printf '     read: marker: absent and reserved: absent\n' >&$out
  printf '  6. Restore (always, from the exit trap):\n' >&$out
  printf '     put %s/fugaro.yaml back byte-for-byte, remove .fugaro/%s.Dockerfile\n' "$CHECKOUT" "$WORKFLOW" >&$out
  printf '     git commit + git push origin %s ; fugaro update --repo %s\n' "${BRANCH:-<branch>}" "$REPO" >&$out
  printf '     fugaro image rm %s ; fugaro update\n' "$ENTRY" >&$out
  printf '     gh pr close fugaro/<run> --delete-branch (both runs), read back CLOSED and the branch gone\n' >&$out
  printf '     fugaro doctor --all                      # no stamp for %s and no hand-set %s\n' "$ENTRY" "$RESERVED_NAME" >&$out
  printf 'Nothing was created, published, built, committed, pushed or launched: this dry run made no cloud call and no fugaro call at all.\n' >&$out
}

# ------------------------------------------------------------------- the block
# Two key sets: the real run's rows, and the four a dry run can have — it makes
# no doctor call, so it has no `installation` row to offer and must not print one
# reading 'not reached' beside a plan that was never carried out.
BLOCK_KEYS_REAL=(belong installation checkout workflow consent entry stamp reserved converged run-marker run-value refutation refutation-run restore)
BLOCK_KEYS_DRY=(belong checkout workflow dry-run restore)
BLOCK_KEYS=("${BLOCK_KEYS_REAL[@]}")

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 291 RESULT\n' >&3
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
    printed="$printed"$'\n'"$key"
  done
  printed="$printed"$'\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 291 RESULT END (exit %s)\n' "$status" >&3
}

main() {
  need_tools
  parse_args "$@"
  if [ "$DRY_RUN" = yes ]; then
    BLOCK_KEYS=("${BLOCK_KEYS_DRY[@]}")
  fi

  scratch_up
  reader_up
  # After scratch_up, not before: a guard that stops the run records the fact
  # that says why, and a fact written before the facts file exists is dropped.
  guard_belong "$REPO" "the repository" belong

  if [ "$DRY_RUN" = yes ]; then
    # The two steps a plan needs are the two that make no call at all: git reads
    # of the checkout, and one read of the checkout's own fugaro.yaml.
    step_checkout
    step_workflow
    fact dry-run PASS "this was a dry run: the plan below is every command the real run would make, and NOTHING was called for it — no fugaro, no gcloud, no gh, no build and no run"
    print_plan
    RESTORE_STATE=untouched
    RESTORE_DETAIL="a dry run changed nothing, so there is nothing to restore"
    REACHED_END=yes
    trap - ERR
    return 0
  fi

  step_installation
  step_checkout
  step_workflow
  step_consent
  step_entry
  step_stamp
  step_reserved
  step_converged
  step_run_marker
  step_run_value
  step_refutation
  step_refutation_run

  REACHED_END=yes
  trap - ERR
  local failed
  failed=$(grep -c '|FAIL|' "$FACTS_FILE" 2>/dev/null || true)
  [ "${failed:-0}" -eq 0 ] || return 1
  return 0
}

trap finish EXIT
trap on_signal INT TERM HUP
trap on_error ERR
main "$@"
