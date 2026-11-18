#!/usr/bin/env bash
# Check 292 — a catalog entry whose source names a build `context:`: the copy
# that stages it, the file in the built image, the one commit the record and the
# label name, and the nightly decision the staged context fires, and then
# converges.
#
# Plan task CB5 of docs/catalog-build-context/implementation-plan.md §6 writes
# this check — with that folder's F22, F29, F30 and F32 and its D1402, D1403,
# D1408 and D1409 — and docs/gcp-live-checklist.md's Check 292 is the register's
# own row and section for it. What it settles is read from the outside and
# nowhere else: the copy is a provider tree read at one commit
# (internal/cli/catalog_copy.go's writeCatalogDockerfile, whose one caller is
# `fugaro image build` and NOT `fugaro update`), the `COPY` of a file under the
# context is a real `docker build` in a real Cloud Build, the record is written
# by that build's own last step, and the trigger is the digest the copy staged
# beside the Dockerfile compared with the record's
# (internal/imagecheck.DecideCatalog, ReasonCatalogContext).
#
# What it measures:
#
#   * `fugaro update` publishes the member and asks nothing new: the entry whose
#     source carries `context:` is in the published node
#     (`fugaro project policy show --json`), the update ran with its input closed
#     and printed no question, and it staged NOTHING under the entry's own prefix
#     in the runs bucket. What stages the definition, the pinned commit and the
#     context's digest is the copy in the build below, which is the plan's own
#     step 1 read the other way round;
#   * the built image carries the copied file, read out of the image itself:
#     `docker run --rm --entrypoint cat <the built image> <the COPY's target>`
#     prints the marker this check wrote, which is the fact the whole feature is
#     about — a catalog build could not reach a file of the repository before it;
#   * the entry's build record names ONE commit: `source_commit` and
#     `context_commit` are the same value, both the commit this check pushed,
#     `context_sha256` is a 64-hex string that is not the Dockerfile's
#     `definition_sha256`, `template_salt` is there, and the image's
#     `org.opencontainers.image.revision` label is that same commit;
#   * a commit that changes a file UNDER the context directory, pushed and then
#     STAGED by a submission cancelled before its record step, makes the
#     project's catalog check job decide `rebuild` with the `catalog-context`
#     reason; the rebuild it submits is pinned to the staged commit; and a second
#     decision on the same entry carries no `catalog-context`. A commit OUTSIDE
#     the context directory, with the entry's Dockerfile unchanged, fires
#     nothing — the negative half of the plan's step 4.
#
# The negative half is the one thing the plan's own step 4 cannot be run as
# written, and the register's Check 292 section says so: the project's catalog
# check job reads no branch at all — it reads two staged objects out of the runs
# bucket (`builds/catalog/<entry>/context-sha256` and `.../source-commit`) and
# never opens the provider, which is what keeps it credential-less (D1401) — so a
# pushed commit fires nothing until a copy stages it and no build records it.
# This check therefore reads the trigger where the code puts it: it stages a copy
# of its own with `fugaro image build <entry> --no-wait`, cancels that build
# before its record step, and runs the check job itself.
#
# Usage:      bash scripts/live-checks/check292.sh --sandbox <a sandbox repository checkout>
#             bash scripts/live-checks/check292.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check292.sh --sandbox <path> --entry <catalog entry name>
#             bash scripts/live-checks/check292.sh --help
# Needs:      bash, mktemp, python3 (the `--json` documents are read with it),
#             git (the commits this check pushes go through your checkout's own
#             credential), docker (the copied file and the label are read out of
#             the built image), gcloud (authenticated as YOU: the staged objects,
#             the record, the cancelled build and the check job are read and run
#             with it) and fugaro (the installed release: every step this check
#             drives is one you could type yourself).
# Where:      your own machine, the **sandbox installation** and the **sandbox
#             repository** only. A project, region, installation, repository or
#             path naming belong (or edge-devel-dimi, or edgeappinc) is refused
#             by name, and so is a coding agent's session: no agent runs any step
#             of this check.
# Creates:    one catalog entry in this machine's project config (added with
#             `--no-apply` and removed in the Restore), one throwaway branch on
#             the remote carrying three commits, the entry's staged objects, its
#             record and its check state in the project's runs bucket, and one
#             image in the installation's shared registry. The Restore row names
#             every one of them.
# Costs:      four Cloud Builds (**billable**, each reserved against the project's
#             budget the way every build is): the entry's own build, the two
#             submissions staged with `--no-wait` and cancelled before their
#             record step (one per half of the trigger's reading), and the
#             rebuild the project's catalog check job submits when the entry
#             fires — that one is the reading, not overhead. No run is launched,
#             no model is called and no secret is written or printed.
# Runs:       by YOU. Its sibling test (check292_test.sh) drives it against fake
#             tools only and reaches no cloud.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. What the check leaves behind — the throwaway
#             branch and its commits, the image — is named in the Restore row and
#             is not this check's to undo.
# Output:     progress on stderr, ONE block on stdout headed CHECK 292 RESULT,
#             then CHECK 292 RESULT END (exit N) — to copy back into the
#             register's Check 292 section, and nothing else. Exit 0: every
#             criterion that was measured held. 1: a measured criterion did not
#             hold. 2: a row could not be measured for a reason that is this
#             check's own setup rather than the release (the block says which, by
#             name). A key the run never reached reads `FAIL — not reached`,
#             which is a row that was NOT measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: no token, no key, no credential value and no
# build log. Every fact below is a NAME — an entry's name, a commit, a digest, a
# bucket key, a build id, a decision — and the one file this check reads out of
# an image is one it wrote itself. It holds no credential of its own: it drives
# `fugaro`, `gcloud`, `docker` and your checkout's `git`.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK292_SANDBOX (or --sandbox: the checkout of a sandbox repository —
# REQUIRED), CHECK292_ENTRY (or --entry: the entry this check adds. Unset, it is
# `chk292-<hex>`, a name no project has), CHECK292_CONTEXT (the directory the
# entry's context names, `ci` by default: the Dockerfile is
# <context>/Dockerfile, the file it copies is <context>/scripts/marker.sh, and a
# checkout that already has that directory is refused rather than written into),
# CHECK292_BASE (the image the entry's Dockerfile is FROM; unset, it is the
# `base` image `fugaro doctor --json` reports for this installation),
# CHECK292_DRY_RUN, CHECK292_KEEP_SCRATCH, CHECK292_REGION (the region the builds
# and the check job are read in — the project's own region, as `fugaro doctor`
# reports it, unless the knob names another),
# CHECK292_DOCTOR_TIMEOUT_SECONDS (180), CHECK292_CLI_TIMEOUT_SECONDS (180),
# CHECK292_UPDATE_TIMEOUT_SECONDS (3600), CHECK292_BUILD_TIMEOUT_SECONDS (1800),
# CHECK292_REBUILD_TIMEOUT_SECONDS (1800), CHECK292_LOG_WAIT_SECONDS (120) and
# CHECK292_LOG_POLL_SECONDS (10).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check drives a real
# `fugaro` and a real `docker` at a real terminal, and a trace of that is a
# second copy of what its own output holds.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
die() { say "$*"; exit 2; }

stop() {
  fact aborted FAIL "$1"
  owner "stopped: $1"
  exit 1
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# unmeasured: a row the check could NOT measure, for a reason that is not a
# finding about the release. The row reads FAIL in the block — it is not a pass
# — and the check ends as one that could not measure (exit 2).
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

# flat TEXT: one borrowed string as a fact detail may hold it — no newline and no
# `|`, which is the facts file's own separator, and bounded, because a Cloud
# Logging line or a record value is not this check's text.
flat() {
  printf '%s' "${1:-}" | tr '\n\r' '  ' | tr -d '|' | cut -c1-400
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  say "an internal error ended this check at line $line"
  exit 2
}

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 292 needs bash"
  local t
  for t in mktemp python3 git docker gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 292 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk292-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
INSTALL=""
PROJECT=""
REGION=""
BASE=""
BUCKET=""
CFG=""
REPO=""
ENTRY=""
BRANCH=""
ORIG_REF=""
COMMIT_A=""
COMMIT_B=""
COMMIT_C=""
IMAGE=""
IMAGE_DIGEST=""
SLUG=""
RECORD_PATH=""
REPO_ADDED=no
BRANCH_MADE=no
UPDATED=no
BUILT=no
RECORD_OK=no
CONTEXT_DIGEST=""
QUIET_NOTE=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk292.XXXXXXXX") || die "Check 292 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  mkdir -p "$SCRATCH/out"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
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
    say "CHECK292_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# -------------------------------------------------------------------- globals
# Every knob is read here, once, before any command runs, so that no refusal
# below is one a knob caused halfway through.
FUGARO_BIN=""
SANDBOX=${CHECK292_SANDBOX:-}
ENTRY=${CHECK292_ENTRY:-}
CONTEXT=${CHECK292_CONTEXT:-ci}
BASE=${CHECK292_BASE:-}
REGION=${CHECK292_REGION:-}
DRY_RUN=${CHECK292_DRY_RUN:-}
DRY_RUN_CLI=""
KEEP_SCRATCH=${CHECK292_KEEP_SCRATCH:-}
DOCTOR_TIMEOUT=${CHECK292_DOCTOR_TIMEOUT_SECONDS:-180}
CLI_TIMEOUT=${CHECK292_CLI_TIMEOUT_SECONDS:-180}
UPDATE_TIMEOUT=${CHECK292_UPDATE_TIMEOUT_SECONDS:-3600}
BUILD_TIMEOUT=${CHECK292_BUILD_TIMEOUT_SECONDS:-1800}
REBUILD_TIMEOUT=${CHECK292_REBUILD_TIMEOUT_SECONDS:-1800}
CHECK_TIMEOUT=${CHECK292_CHECK_TIMEOUT_SECONDS:-900}
LOG_WAIT=${CHECK292_LOG_WAIT_SECONDS:-120}
LOG_POLL=${CHECK292_LOG_POLL_SECONDS:-10}
# The bound the submitted-and-cancelled build's own status is given: the cancel
# has to land before the build's record step, and a build whose status never
# comes back is a race this check cannot read either way.
STAGE_WAIT=${CHECK292_STAGE_WAIT_SECONDS:-90}
# The path the entry's Dockerfile COPYs the marker to, inside the image: one
# value, so the Dockerfile this check writes and the path it reads back cannot
# drift. It is deliberately not a path a real image is likely to hold.
MARKER_DEST=/opt/team/scripts/marker.sh
BUILD_ID=""
REBUILD_ID=""
REBUILD_STAGED=""
STAGE_ID=""
STAGE_STATUS=""
STAGE_WHY=""
CHECK_WHY=""
CHECK_SINCE=""
EVENT_WHY=""

# The `--json` documents this check reads, in one program: a live check on the
# owner's machine has python3 and no jq, and a row that read a document with grep
# would report a member, a commit or a decision the tool did not print the day
# the document is re-wrapped. Every query prints `key=value` lines or
# `\t`-separated rows, so a shell can hold them without a second parser.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import json
import sys


def load(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def word(value, limit=200):
    if value is None:
        return "-"
    text = str(value)
    return text[:limit] if text else "-"


def yes(value):
    return "true" if value else "false"


what = sys.argv[1]
doc = load(sys.argv[2])

if what == "doctor":
    project = (doc or {}).get("project") or {}
    bases = project.get("base_images") or {}
    print("install=%s" % word(project.get("name"), 60))
    print("project=%s" % word(project.get("gcp_project"), 80))
    print("region=%s" % word(project.get("region"), 40))
    print("base=%s" % word(bases.get("base") if isinstance(bases, dict) else "", 200))
elif what == "catalog":
    entries = (doc or {}).get("entries") or []
    print("count=%d" % len(entries))
    for entry in entries:
        print("entry\t%s\t%s\t%s\t%s\t%s" % (
            entry.get("name") or "",
            yes(entry.get("default")),
            ";".join(entry.get("tags") or []),
            entry.get("source") or "",
            entry.get("digest") or "",
        ))
elif what == "policy":
    # One entry of the published node as the member this check is about: the
    # context. A node that carries no catalog at all is `entries=-`, which is a
    # different row from a node whose entry carries no context.
    entry_name = sys.argv[3]
    policy = (doc or {}).get("policy") or {}
    catalog = policy.get("image_catalog")
    if not isinstance(catalog, dict):
        print("entries=-")
        print("found=no")
        print("context=-")
        print("repo=-")
        print("path=-")
        print("branch=-")
    else:
        entries = catalog.get("entries") or []
        print("entries=%d" % len(entries))
        hit = None
        for entry in entries:
            if (entry or {}).get("name") == entry_name:
                hit = entry
                break
        if hit is None:
            print("found=no")
            print("context=-")
            print("repo=-")
            print("path=-")
            print("branch=-")
        else:
            source = hit.get("source") or {}
            print("found=yes")
            print("context=%s" % word(source.get("context"), 200))
            print("repo=%s" % word(source.get("repo"), 120))
            print("path=%s" % word(source.get("path"), 200))
            print("branch=%s" % word(source.get("branch"), 120))
elif what == "record":
    print("source_commit=%s" % word((doc or {}).get("source_commit"), 80))
    print("context_commit=%s" % word((doc or {}).get("context_commit"), 80))
    print("context_sha256=%s" % word((doc or {}).get("context_sha256"), 80))
    print("definition_sha256=%s" % word((doc or {}).get("definition_sha256"), 80))
    print("template_salt=%s" % word((doc or {}).get("template_salt"), 200))
    print("image=%s" % word((doc or {}).get("image"), 300))
    print("image_digest=%s" % word((doc or {}).get("image_digest"), 120))
    print("build_id=%s" % word((doc or {}).get("build_id"), 120))
elif what == "build":
    print("status=%s" % word((doc or {}).get("status"), 40))
elif what == "events":
    # The newest catalog-check line for the entry, at or after the moment the
    # check job was started. Cloud Logging answers newest first, and the
    # timestamp is compared as the RFC3339 text it is: an older run's line must
    # never be read as this one's decision.
    since = sys.argv[3] if len(sys.argv) > 3 else ""
    rows = doc if isinstance(doc, list) else []
    best = None
    for row in rows:
        payload = (row or {}).get("jsonPayload") or {}
        if payload.get("event") != "catalog-check":
            continue
        stamp = str((row or {}).get("timestamp") or "")
        if since and stamp < since:
            continue
        if best is None or stamp > best[0]:
            best = (stamp, payload)
    if best is None:
        print("event=-")
    else:
        stamp, payload = best
        reasons = payload.get("reasons") or []
        print("event\t%s\t%s\t%s\t%s\t%s" % (
            stamp,
            payload.get("decision") or "",
            ",".join(str(r) for r in reasons),
            payload.get("build_id") or "",
            str(payload.get("message") or "").replace("\t", " ")[:400],
        ))
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

# read_json QUERY FILE DEST [ARG...]: answer one query into a file, and fail the
# step when the document is not the document it should be.
read_json() { # read_json QUERY FILE DEST [ARG...]
  local query=$1 file=$2 dest=$3
  shift 3
  python3 "$SCRATCH/read.py" "$query" "$file" "$@" >"$dest" 2>"$dest.err"
}

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# ev_field FILE COL — one column of the newest catalog-check line the events
# reader printed.
ev_field() { # ev_field FILE COL
  awk -F'\t' '$1 == "event" { print $'"$2"'; exit }' "$1" 2>/dev/null || true
}

# entry_exists FILE NAME — `yes` when `fugaro image ls --json` lists the entry.
entry_exists() { # entry_exists FILE NAME
  awk -F'\t' -v name="$2" '$1 == "entry" && $2 == name { print "yes"; exit }' "$1" 2>/dev/null || true
}

# run_bounded SECONDS KEY DIR CMD...: run one command from the directory it has
# to be run in, under a bound, with its input closed. The input is closed in
# every child: no command this check drives may wait for a person, and a check
# that let one read this terminal would be the prompt the whole point of it is
# to report.
#
# The child execs, so the pid the bound kills IS the command: a hung cloud call
# would otherwise outlive the check that gave up on it. The ERR trap is dropped
# in it too: this check runs commands whose failing exit status is one of its own
# readings, and a trap inherited here would turn each of them into "an internal
# error" and a status of 2 before the step could report it.
run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  BOUNDED_TIMED_OUT=no
  ( trap - ERR; cd "$dir" || exit 1; exec "$@" ) </dev/null >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
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
  BOUNDED_STATUS=0
  wait "$pid" 2>/dev/null || BOUNDED_STATUS=$?
}

# out_of KEY...: the combined output of the named runs, for the readers that look
# at a tool's own sentences.
out_of() { # out_of KEY... [SUFFIX]
  cat "$@" 2>/dev/null || true
}

# bucket_ls KEY PATTERN: what a `gcloud storage ls` answered. OBJECTS holds one
# URI per line, and the answer is `objects`, `none` or `error`: the real command
# exits non-zero with "matched no objects" for a prefix nothing lives under, and
# a listing that failed for another reason is not an empty prefix.
BUCKET_LS_ANSWER=""
BUCKET_LS_OBJECTS=""
bucket_ls() { # bucket_ls SECONDS KEY PATTERN
  local secs=$1 key=$2 pattern=$3
  run_bounded "$secs" "$key" "$SANDBOX" gcloud storage ls "$pattern"
  BUCKET_LS_ANSWER=error
  BUCKET_LS_OBJECTS=""
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then return 1; fi
  if [ "$BOUNDED_STATUS" = 0 ]; then
    BUCKET_LS_OBJECTS=$(sed -n 's/^\(gs:\/\/.*\)$/\1/p' "$SCRATCH/$key.out" 2>/dev/null || true)
    if [ -z "$BUCKET_LS_OBJECTS" ]; then
      BUCKET_LS_ANSWER=none
    else
      BUCKET_LS_ANSWER=objects
    fi
    return 0
  fi
  if grep -qi 'matched no objects' "$SCRATCH/$key.err" 2>/dev/null; then
    BUCKET_LS_ANSWER=none
    return 0
  fi
  return 1
}

# bucket_cat KEY URI: one object's bytes into $SCRATCH/$key.out.
bucket_cat() { # bucket_cat SECONDS KEY URI
  local secs=$1 key=$2 uri=$3
  run_bounded "$secs" "$key" "$SANDBOX" gcloud storage cat "$uri"
}

# bucket_rm KEY PATTERN: remove the objects one pattern matches. A pattern that
# matches nothing is not a failure — it is the state a run that stopped before it
# staged anything leaves — and the answer says which of the two happened.
BUCKET_RM_ANSWER=""
bucket_rm() { # bucket_rm SECONDS KEY PATTERN
  local secs=$1 key=$2 pattern=$3
  run_bounded "$secs" "$key" "$SANDBOX" gcloud storage rm "$pattern"
  BUCKET_RM_ANSWER=removed
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    BUCKET_RM_ANSWER=timeout
    return 1
  fi
  if [ "$BOUNDED_STATUS" = 0 ]; then return 0; fi
  if grep -qi 'matched no objects' "$SCRATCH/$key.err" 2>/dev/null; then
    BUCKET_RM_ANSWER=none
    return 0
  fi
  return 1
}

# yaml_scalar FILE KEY: one scalar out of a flat yaml file, for the project
# config's own fields (runs_bucket).
yaml_scalar() { sed -n "s|^ *$2: *||p" "$1" 2>/dev/null | tr -d "\"'" | head -1; }

# repo_from_origin URL: the owner/name a git remote URL names.
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

# git_commit MSG PATH...: commit exactly the paths this check edited — a
# `git add -A` would carry whatever else the checkout holds — and push them to
# the throwaway branch.
git_commit() { # git_commit MSG PATH...
  local msg=$1
  shift
  git -C "$SANDBOX" add -- "$@" 2>>"$SCRATCH/git.err" || return 1
  if git -C "$SANDBOX" diff --cached --quiet 2>>"$SCRATCH/git.err"; then
    say "nothing to commit for '$msg' (the checkout already carries it)"
    return 1
  fi
  git -C "$SANDBOX" -c user.name="fugaro check292" -c user.email="check292@fugaro.invalid" \
    commit -q -m "$msg" >>"$SCRATCH/git.log" 2>>"$SCRATCH/git.err" || return 1
  git -C "$SANDBOX" push -q origin "$BRANCH" >>"$SCRATCH/git.log" 2>>"$SCRATCH/git.err" || return 1
  git -C "$SANDBOX" rev-parse HEAD 2>/dev/null
}

# The one write this check makes to a file of yours: the entry's `context:`
# member, which no command writes (`fugaro image add` has no --context flag, and
# nothing else writes source.context either). It is a line edit of the YAML and
# not a parse-and-re-emit, so nothing else in the file moves, and the value is
# only ever inserted under the entry's own `source:`. The read-back is the
# command's own: `fugaro image ls` decodes the file strictly
# (internal/localcfg's KnownFields), so an edit that is not a document this
# binary parses is a row this check reports rather than a file it leaves broken.
write_ctx() {
  cat >"$SCRATCH/ctx.py" <<'PY'
import sys


def indent(line):
    return len(line) - len(line.lstrip(" \t"))


mode, path, entry, value = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
with open(path, "r", encoding="utf-8") as fh:
    lines = fh.read().split("\n")

top = None
for i, line in enumerate(lines):
    if line.rstrip() == "image_catalog:" and not line.startswith((" ", "\t")):
        top = i
        break
if top is None:
    sys.stderr.write("no image_catalog: block at the top level\n")
    sys.exit(2)

items = None
for i in range(top + 1, len(lines)):
    line = lines[i]
    if line.strip() == "" or line.lstrip().startswith("#"):
        continue
    if not line.startswith((" ", "\t")):
        break
    if line.strip() == "entries:":
        items = i
        break
if items is None:
    sys.stderr.write("no entries: list under image_catalog:\n")
    sys.exit(2)

item = None
for i in range(items + 1, len(lines)):
    line = lines[i]
    if line.strip() == "":
        continue
    if not line.startswith((" ", "\t")):
        break
    stripped = line.strip()
    if stripped.startswith("- "):
        head = stripped[2:].strip()
        if head == "name: " + entry or head == 'name: "%s"' % entry or head == "name: '%s'" % entry:
            item = i
            break
if item is None:
    sys.stderr.write("the catalog has no entry named %s\n" % entry)
    sys.exit(3)

item_indent = indent(lines[item])
end = len(lines)
for j in range(item + 1, len(lines)):
    if lines[j].strip() == "":
        continue
    if indent(lines[j]) <= item_indent:
        end = j
        break

src = None
for j in range(item + 1, end):
    stripped = lines[j].strip()
    if stripped == "source:" or stripped.startswith("source: {"):
        src = j
        break
if src is None:
    sys.stderr.write("the entry carries no source: member\n")
    sys.exit(4)

if lines[src].strip().startswith("source: {") or "{" in lines[src]:
    if "context" in lines[src].split("{", 1)[1]:
        sys.stderr.write("the entry's source already carries a context\n")
        sys.exit(5)
    head, _, tail = lines[src].partition("{")
    if "}" not in tail:
        sys.stderr.write("the entry's source is not a flow mapping this edit can hold\n")
        sys.exit(6)
    inner, _, rest = tail.rpartition("}")
    inner = inner.strip()
    joined = inner + (", " if inner else "") + "context: " + value
    lines[src] = head + "{ " + joined + " }" + rest
else:
    child = None
    for j in range(src + 1, end):
        if lines[j].strip() == "":
            continue
        child = indent(lines[j])
        break
    if child is None or child <= indent(lines[src]):
        child = indent(lines[src]) + 2
    for j in range(src + 1, end):
        if lines[j].strip() == "":
            continue
        if indent(lines[j]) < child:
            break
        if lines[j].strip().split(":", 1)[0] == "context":
            sys.stderr.write("the entry's source already carries a context\n")
            sys.exit(5)
    # The children of source: sit one level in from it, and the member goes
    # first among them: the file is read by a strict decoder, which does not care
    # about the order, and a person reading it finds the context where the
    # Dockerfile's own keys are.
    lines.insert(src + 1, " " * child + "context: " + value)

with open(path, "w", encoding="utf-8") as fh:
    fh.write("\n".join(lines))
print("context=written")
PY
  chmod 700 "$SCRATCH/ctx.py"
}

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check292.sh --sandbox <a sandbox repository checkout> — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy a build's and an image's output to the terminal"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy a build's output"
}

# guard_agent_session: the check is the owner's, it drives billable builds and it
# writes a catalog entry. Refusing here rather than letting each command refuse
# one at a time is what makes the block say why: a session that ran this far
# would report the refusals as findings about the release.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 292 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

BELONG_NAMES="belong edge-devel-dimi edgeappinc"
guard_belong() { # guard_belong VALUE KIND KEY
  local value=$1 kind=$2 key=$3 name
  [ -n "$value" ] || return 0
  for name in $BELONG_NAMES; do
    case "$value" in
    *"$name"*)
      fact "$key" FAIL "$kind is $value, which names $name: Check 292 writes a catalog entry, pushes commits and submits billable Cloud Builds, and it is for a sandbox only"
      stop "Check 292 never runs against $name. Name a sandbox $kind"
      ;;
    esac
  done
}

version_row() {
  local v
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} (the builds and the check this reads back are the ones the release you have submits; the check can only be run from a CLI at or after the release that flips the context gate, 0.26.0 — an older one refuses the publish with its own sentence, which the update row reports)"
}

# ---------------------------------------------------------------- the steps
# step_sandbox: the premise. The installation, the project, the region, the base
# image and the runs bucket are read from the checkout and from the local config
# in force, so the check measures the installation you work in rather than one it
# guessed.
step_sandbox() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a sandbox repository)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and this check commits into it and builds a catalog entry whose Dockerfile is read out of it"
  fi
  guard_belong "$SANDBOX" "the --sandbox path" belong
  local rc=0 url=""
  url=$(git -C "$SANDBOX" remote get-url origin 2>"$SCRATCH/origin.err") || rc=$?
  if [ "$rc" -ne 0 ] || [ -z "$url" ]; then
    fact sandbox FAIL "the checkout at $SANDBOX names no origin this check could read (git exited ${rc}): the entry this check adds reads its Dockerfile out of that repository, and the commits it pushes go to it. Point the checkout at the sandbox repository's remote"
    stop "the checkout at $SANDBOX names no origin this check could read (git exited ${rc}). Point it at the sandbox repository's remote"
  fi
  REPO=$(repo_from_origin "$url")
  guard_belong "$REPO" "the checkout's origin" belong
  if [ -z "$REPO" ]; then
    fact sandbox FAIL "the origin of $SANDBOX is '$url', which names no repository this check could read: the entry it adds reads its Dockerfile out of that repository by owner and name, so an origin this check cannot read is one it will not guess at"
    stop "the origin of $SANDBOX is '$url', which names no repository this check could read. Point it at the sandbox repository"
  fi
  local dirty
  dirty=$(git -C "$SANDBOX" status --porcelain 2>/dev/null || true)
  if [ -n "$dirty" ]; then
    fact sandbox FAIL "the checkout at $SANDBOX has uncommitted changes ('$(flat "$(printf '%s' "$dirty" | head -n 1)")'): this check cuts a throwaway branch, commits into it and pushes them, so your own work would ride along. Commit or stash them and run the check again"
    stop "the checkout at $SANDBOX has uncommitted changes. Commit or stash them and run the check again"
  fi
  ORIG_REF=$(git -C "$SANDBOX" symbolic-ref --short -q HEAD 2>/dev/null || true)
  if [ -z "$ORIG_REF" ]; then
    fact sandbox FAIL "the checkout at $SANDBOX is on no branch (a detached HEAD): this check leaves the checkout on the branch it found it on, and there is none to return to"
    stop "the checkout at $SANDBOX is on no branch (a detached HEAD). Check out a branch and run the check again"
  fi
  say "reading the installation: fugaro doctor --json, in ${SANDBOX}"
  run_bounded "$DOCTOR_TIMEOUT" doctor "$SANDBOX" "$FUGARO_BIN" doctor --json
  # doctor exits 1 whenever any of its checks is not ok, warnings included, and
  # still prints its document; only a timeout or another status is no answer.
  if [ "$BOUNDED_TIMED_OUT" = yes ] || { [ "$BOUNDED_STATUS" != 0 ] && [ "$BOUNDED_STATUS" != 1 ]; }; then
    unmeasured sandbox "fugaro doctor --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): this check cannot read the installation this checkout belongs to, and every row below is about that installation. Its output is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
    return 0
  fi
  if ! read_json doctor "$SCRATCH/doctor.out" "$SCRATCH/doctor.txt"; then
    unmeasured sandbox "the doctor document in $SCRATCH/doctor.out could not be read: what it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  INSTALL=$(field "$SCRATCH/doctor.txt" install)
  PROJECT=$(field "$SCRATCH/doctor.txt" project)
  local doc_region base
  doc_region=$(field "$SCRATCH/doctor.txt" region)
  base=$(field "$SCRATCH/doctor.txt" base)
  [ "$INSTALL" = - ] && INSTALL=""
  [ "$PROJECT" = - ] && PROJECT=""
  [ "$doc_region" = - ] && doc_region=""
  if [ -z "$PROJECT" ]; then
    unmeasured sandbox "fugaro doctor --json answered without a project block: this check cannot say which installation the sandbox checkout belongs to, and the entry, the builds and the check job are all that installation's. What it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  if [ -z "${CHECK292_REGION:-}" ] && [ -n "$doc_region" ]; then REGION=$doc_region; fi
  [ "$base" = - ] && base=""
  if [ -z "$BASE" ]; then BASE=$base; fi
  guard_belong "${INSTALL}${PROJECT}${REGION}" "the installation" belong
  CFG="${XDG_CONFIG_HOME:-$HOME/.config}/fugaro/projects/${INSTALL:-$PROJECT}.yaml"
  if [ ! -r "$CFG" ]; then
    unmeasured sandbox "no project config at $CFG: this check reads the installation's own runs bucket out of that file and writes the entry it adds into it (fugaro image add --no-apply writes exactly this file, and the path is the one fugaro doctor --project ${INSTALL:-$PROJECT} names). Its own cause is a config this machine has not got, which is a setup row rather than a finding"
    return 0
  fi
  BUCKET=$(yaml_scalar "$CFG" runs_bucket)
  if [ -z "$BUCKET" ]; then
    unmeasured sandbox "the project config at $CFG names no runs_bucket: the entry's staged objects, its record and its check state all live in that bucket, and with no bucket read here the check cannot tell an entry whose copy staged nothing from one it cannot look for. Its own config is in $CFG"
    return 0
  fi
  if [ -z "$BASE" ]; then
    unmeasured sandbox "no base image: fugaro doctor --json reported none for ${INSTALL:-$PROJECT} (project.base_images.base), and the Dockerfile this check writes is FROM it. Set CHECK292_BASE to the base your jobs run today, or fix what fugaro doctor reports"
    return 0
  fi
  local stated=no
  if grep -q '^image_catalog:' "$CFG" 2>/dev/null; then stated=yes; fi
  if [ "$stated" = no ]; then
    unmeasured sandbox "the project config at $CFG states no image_catalog: block, and the entry this check adds would then be the whole catalog this machine publishes — replacing the published one, which is not a sandbox check's to change. Add the project's catalog to that file first (a published node's catalog that this machine does not state is taken away on purpose by a config that does: internal/localcfg's ImageCatalogEffective)"
    return 0
  fi
  fact sandbox PASS "${SANDBOX} is a Fugaro repository's checkout of ${REPO}, on ${ORIG_REF}, with a clean tree, and it belongs to ${INSTALL:-<no name>} (${PROJECT}, region ${REGION}): the entry this check adds is that installation's, its Dockerfile is read out of ${REPO}, and its runs bucket is ${BUCKET}"
}

# step_entry: the entry, its context, and the file its Dockerfile copies. The
# subdirectory is load-bearing: `context: .` is legal and is the issue's own
# example, but a repository-root context makes a commit "outside the context"
# impossible to write, and the negative half of the trigger's reading needs one.
# Everything here is on a throwaway branch cut from the branch the checkout was
# on, so the branch you were working on never moves.
step_entry() {
  if [ -z "$BUCKET" ]; then
    unmeasured entry "there is no installation to add an entry to: the row above says why"
    return 0
  fi
  if [ "$CONTEXT" = "." ]; then
    unmeasured entry "CHECK292_CONTEXT is '.', the repository root, and the negative half of this check's reading needs a commit outside the context: a root context is the one thing this check's shape adds to the plan's. Name a subdirectory"
    return 0
  fi
  case "$CONTEXT" in
  "" | /* | *..*)
    unmeasured entry "CHECK292_CONTEXT is '$CONTEXT', which is not a directory inside the repository: the entry's source.path and source.context are both read inside ${REPO}. Name a subdirectory such as ci"
    return 0
    ;;
  esac
  if [ -e "$SANDBOX/$CONTEXT" ]; then
    unmeasured entry "the checkout at $SANDBOX already carries ${CONTEXT}/, and this check writes that directory itself and commits it: writing into yours would put your files into its commits. Name another directory with CHECK292_CONTEXT"
    return 0
  fi
  say "cutting a throwaway branch and writing the entry's definition: git checkout -b ${BRANCH} (from ${ORIG_REF}), then ${CONTEXT}/Dockerfile and ${CONTEXT}/scripts/marker.sh"
  run_bounded "$CLI_TIMEOUT" branch "$SANDBOX" git checkout -b "$BRANCH"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured entry "git checkout -b ${BRANCH} exited ${BOUNDED_STATUS} in ${SANDBOX}: the entry's Dockerfile has to be on a branch the copy can read through the provider, and this is the branch that carries it. What git printed is in $SCRATCH/branch.err"
    return 0
  fi
  BRANCH_MADE=yes
  mkdir -p "$SANDBOX/$CONTEXT/scripts" || {
    unmeasured entry "cannot write ${CONTEXT}/ in $SANDBOX"
    return 0
  }
  printf 'FROM %s\nCOPY scripts/marker.sh %s\n' "$BASE" "$MARKER_DEST" >"$SANDBOX/$CONTEXT/Dockerfile"
  printf 'check 292 marker\n' >"$SANDBOX/$CONTEXT/scripts/marker.sh"
  cp "$SANDBOX/$CONTEXT/scripts/marker.sh" "$SCRATCH/marker.a"
  local commit_a
  commit_a=$(git_commit "check292: the entry's Dockerfile and the file its COPY reaches" "$CONTEXT")
  if [ -z "$commit_a" ]; then
    unmeasured entry "the commit that carries ${CONTEXT}/Dockerfile could not be made or pushed in $SANDBOX: the copy reads the provider and not the working tree, so an entry whose definition is not pushed is one nothing can build. What git printed is in $SCRATCH/git.err"
    return 0
  fi
  COMMIT_A=$commit_a
  say "adding the entry: fugaro image add ${ENTRY} --dockerfile ${REPO}:${CONTEXT}/Dockerfile --branch ${BRANCH} --tags context-check --no-apply"
  run_bounded "$CLI_TIMEOUT" add "$SANDBOX" "$FUGARO_BIN" image add "$ENTRY" \
    --dockerfile "${REPO}:${CONTEXT}/Dockerfile" --branch "$BRANCH" --tags context-check --no-apply
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured entry "fugaro image add ${ENTRY} exited ${BOUNDED_STATUS} in $SANDBOX: the entry this whole check reads back has to be in this machine's project config, and --no-apply is what keeps that write from building every entry of the catalog first. What it printed is in $SCRATCH/add.out and $SCRATCH/add.err"
    return 0
  fi
  if ! grep -q "^image_catalog:" "$CFG" 2>/dev/null || ! grep -q "$ENTRY" "$CFG" 2>/dev/null; then
    unmeasured entry "fugaro image add wrote no entry named ${ENTRY} into $CFG: the check edits that entry's source and reads it back, and an entry it cannot find is one it will not guess at. What 'fugaro image add' printed is in $SCRATCH/add.out"
    return 0
  fi
  if ! python3 "$SCRATCH/ctx.py" add "$CFG" "$ENTRY" "$CONTEXT/" >"$SCRATCH/ctx.out" 2>"$SCRATCH/ctx.err"; then
    local why
    why=$(head -n 1 "$SCRATCH/ctx.err" 2>/dev/null || true)
    unmeasured entry "the context: member could not be added to ${ENTRY}'s source in $CFG: $(flat "${why:-no reason printed}"). No command writes that member — fugaro image add has no --context flag — so this one edit is the check's own, and a file it cannot edit is one it will not leave half written"
    return 0
  fi
  REPO_ADDED=yes
  say "reading the entry back: fugaro image ls --json (a strict decode of the file the edit just wrote)"
  run_bounded "$CLI_TIMEOUT" catalog "$SANDBOX" "$FUGARO_BIN" image ls --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured entry "fugaro image ls --json did not answer after the edit (exit ${BOUNDED_STATUS}): the project config decodes strictly (internal/localcfg's KnownFields), so this read is what says the edit left a document this binary parses. What it printed is in $SCRATCH/catalog.out and $SCRATCH/catalog.err"
    return 0
  fi
  if ! read_json catalog "$SCRATCH/catalog.out" "$SCRATCH/catalog.txt"; then
    unmeasured entry "fugaro image ls --json printed a document this check could not read: what it printed is in $SCRATCH/catalog.out"
    return 0
  fi
  if [ "$(entry_exists "$SCRATCH/catalog.txt" "$ENTRY")" != yes ]; then
    unmeasured entry "fugaro image ls --json does not list ${ENTRY}, which the config at $CFG now holds: the entry the check builds is read back from this listing, and one that is not in it is one no build below would be about. What it printed is in $SCRATCH/catalog.out"
    return 0
  fi
  fact entry PASS "the catalog now carries ${ENTRY}, whose Dockerfile is ${REPO}:${CONTEXT}/Dockerfile on the throwaway branch ${BRANCH} (commit ${COMMIT_A}) and whose source names context: ${CONTEXT}/ — the member no command writes, edited into $CFG by this check — and fugaro image ls --json reads it back, so the file is a document this binary parses. The entry's tag is context-check, which no repository's needs: names, so it covers no job and no redeploy is owed for it"
}

# step_update: the publish, and the fact that nothing was staged by it. What
# stages the definition, the pinned commit and the context's digest is the copy
# inside a build (writeCatalogDockerfile, whose one caller is `fugaro image
# build`), so the prefix the entry's own objects live under has to be EMPTY after
# the update — which is the reading the plan's step 1 has the other way round.
# The update runs with its input closed and under a bound: a command that asked a
# question could not be answered here, and its own refusal (the version gate that
# refuses a node carrying a context: it does not know) is a finding this row
# reports rather than one a person meets at a prompt.
PROMPT_RE='\(y/N\)|\(Y/n\)|\(y/n\)|\(yes/no\)|\(Y/N\)|\[y/N\]|\[Y/n\]|Type .yes.|type yes|Press [Ee]nter|press [Ee]nter'
step_update() {
  if [ "$REPO_ADDED" != yes ]; then
    unmeasured update "no entry was added, so there is nothing to publish: the row above says why"
    return 0
  fi
  say "publishing the entry: fugaro update (in ${SANDBOX}; minutes of Terraform, and it asks nothing)"
  run_bounded "$UPDATE_TIMEOUT" update "$SANDBOX" "$FUGARO_BIN" update
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured update "fugaro update did not finish inside ${UPDATE_TIMEOUT}s and was ended: this check cannot say whether the node it publishes carries the entry's context, and every row below is read in an installation whose node it is. Its output is in $SCRATCH/update.out and $SCRATCH/update.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact update FAIL "fugaro update exited ${BOUNDED_STATUS} in ${SANDBOX}, so the entry's context: member is not known to be published: $(flat "$(tail -n 3 "$SCRATCH/update.err" 2>/dev/null | head -n 2)"). A refusal naming the version gate (imageContextShipped: no released fugaro reads the key yet) is a run on the release BEHIND 0.26.0 — take the current CLI and run its fugaro update, which is the whole move. What it printed is in $SCRATCH/update.out and $SCRATCH/update.err"
    return 0
  fi
  local asked
  asked=$(cat "$SCRATCH/update.out" "$SCRATCH/update.err" 2>/dev/null | grep -En "$PROMPT_RE" | head -n 1 || true)
  if [ -n "$asked" ]; then
    fact update FAIL "fugaro update ran to a question — '$(flat "$(printf '%s' "$asked" | cut -c1-200)")' — with its input closed: publishing an entry whose source names a context: must ask nothing new, and a line that waits for a person is one this check cannot answer. It exited ${BOUNDED_STATUS} and its output is in $SCRATCH/update.out and $SCRATCH/update.err"
    return 0
  fi
  UPDATED=yes
  say "reading the published node: fugaro project policy show --project ${INSTALL} --json"
  run_bounded "$CLI_TIMEOUT" policy "$SANDBOX" "$FUGARO_BIN" project policy show --project "${INSTALL:-$PROJECT}" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured update "fugaro project policy show --json did not answer (exit ${BOUNDED_STATUS}): the published node is where the entry's context: member has to be, and a read that did not answer is not a node without it. Its output is in $SCRATCH/policy.out and $SCRATCH/policy.err"
    return 0
  fi
  if ! read_json policy "$SCRATCH/policy.out" "$SCRATCH/policy.txt" "$ENTRY"; then
    unmeasured update "fugaro project policy show --json printed a document this check could not read: what it printed is in $SCRATCH/policy.out"
    return 0
  fi
  local entries found ctx
  entries=$(field "$SCRATCH/policy.txt" entries)
  found=$(field "$SCRATCH/policy.txt" found)
  ctx=$(field "$SCRATCH/policy.txt" context)
  if [ "$entries" = "-" ]; then
    fact update FAIL "the published node carries no image_catalog at all, so the entry ${ENTRY} this check added is not in it and no job of the installation could run it: the node's catalog is published from this machine's own config (the policy stage of fugaro update), and a node without one says that stage did not carry it. What fugaro project policy show --json printed is in $SCRATCH/policy.out"
    return 0
  fi
  if [ "$found" != yes ]; then
    fact update FAIL "the published node carries a catalog of ${entries} entries and none of them is ${ENTRY}: the entry this check added to ${CFG} was not published, so nothing about it is live for a job and no build below can be submitted for it. Publish it with 'fugaro update' from this checkout"
    return 0
  fi
  if [ "$ctx" = "-" ] || [ "$ctx" = "" ]; then
    fact update FAIL "the published node carries ${ENTRY} but its source names no context: 'fugaro update' published the entry without the member this check added to ${CFG} — the member no command writes — so the version this installation runs is one whose publisher drops it (D1403's gate). The node's entry is the one every job and every build of this installation reads"
    return 0
  fi
  if [ "$ctx" != "$CONTEXT/" ]; then
    fact update FAIL "the published node carries ${ENTRY} with context: ${ctx}, and this check wrote context: ${CONTEXT}/: the node is published from this machine's config, so the two disagreeing is a publish that did not carry the edit. What fugaro project policy show --json printed is in $SCRATCH/policy.out"
    return 0
  fi
  say "reading the entry's own prefix in the runs bucket: gcloud storage ls gs://${BUCKET}/builds/catalog/${ENTRY}/"
  if ! bucket_ls "$CLI_TIMEOUT" prels "gs://${BUCKET}/builds/catalog/${ENTRY}/"; then
    unmeasured update "gcloud storage ls gs://${BUCKET}/builds/catalog/${ENTRY}/ did not answer (exit ${BOUNDED_STATUS}): whether fugaro update staged anything under the entry's own prefix is half of this row, and a read that failed is not an empty prefix. Its output is in $SCRATCH/prels.err"
    return 0
  fi
  if [ "$BUCKET_LS_ANSWER" = objects ]; then
    fact update FAIL "fugaro update staged objects under the entry's own prefix: $(flat "$(printf '%s' "$BUCKET_LS_OBJECTS" | head -n 4 | tr '\n' ' ')"). What stages a definition, the commit it was read at and the context's digest is the COPY inside a build (internal/cli/catalog_copy.go's writeCatalogDockerfile, whose one caller is 'fugaro image build'), and the plan's own step 1 has that the other way round — so an update that staged them is the reading this row exists to take, and it did not hold"
    return 0
  fi
  fact update PASS "the published node carries ${ENTRY} with context: ${CONTEXT}/ (read back with fugaro project policy show --json), fugaro update ran with its input closed, exited 0 and printed no question, and it staged NOTHING under gs://${BUCKET}/builds/catalog/${ENTRY}/ — the prefix the copy in a build writes — so what stages the definition, the pinned commit and the context's digest is the build below and not the update"
}

# step_build: the build that counts. One Cloud Build of the entry, from the
# checkout its Dockerfile belongs to (the CLI refuses a build from another
# repository by name), and the entry's copied file is what the build's own
# `COPY` puts in the image.
step_build() {
  if [ "$UPDATED" != yes ]; then
    unmeasured build "the entry was not published, so a build of it would read a node this check could not read: the row above says why"
    return 0
  fi
  say "building ${ENTRY} from ${SANDBOX} (a real Cloud Build: billable, reserved against the project's budget)"
  run_bounded "$BUILD_TIMEOUT" build "$SANDBOX" "$FUGARO_BIN" image build "$ENTRY"
  local text
  text=$(cat "$SCRATCH/build.out" "$SCRATCH/build.err" 2>/dev/null || true)
  BUILD_ID=$(printf '%s\n' "$text" | grep -o 'Cloud Build build [A-Za-z0-9._-]*' | head -n 1 | awk '{print $4}' || true)
  local made
  made=$(printf '%s\n' "$text" | sed -n 's/^.*built \([^[:space:]]*\)@\([^[:space:]]*\) (Cloud Build build .*$/\1 \2/p' | head -n 1 || true)
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured build "the build of ${ENTRY} did not finish inside ${BUILD_TIMEOUT}s and was ended: a Cloud Build that hangs is a longer look than a check should take, and this check cannot say whether the entry builds. The build it submitted is ${BUILD_ID:-not named in its output}, and its output is in $SCRATCH/build.out and $SCRATCH/build.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    local hint=""
    case "$text" in
    *'--build-context'*)
      hint=" The build's own words name '--build-context': that is the 0.25.0 bug the release after it fixes (#2995 — the flag is not one the Cloud Build runner it pinned understands), so this row is the release BEHIND and not the feature: take the current CLI, run its fugaro update, and build again"
      ;;
    esac
    fact build FAIL "fugaro image build ${ENTRY} exited ${BOUNDED_STATUS} in ${SANDBOX}: the entry whose source names a context: does not build through Cloud Build, so nothing below it — the copied file, the record, the label — exists to read. Its own words are '$(flat "$(tail -n 3 "$SCRATCH/build.err" 2>/dev/null | grep -v '^$' | head -n 2 | tr '\n' ' ' || true)")'. What it printed is in $SCRATCH/build.out and $SCRATCH/build.err.${hint}"
    return 0
  fi
  if [ -z "$made" ]; then
    case "$text" in
    *"digest unknown"*)
      fact build FAIL "the build of ${ENTRY} finished and reported no pushed image: '$(flat "$(printf '%s\n' "$text" | grep -m 1 'digest unknown' || true)")'. Cloud Build reported a SUCCESS without a digest for the image, which is what the promotion step of the build writes, so there is no image for this check to look inside — and the record the build's own last step wrote names one. Read the build with 'gcloud builds log ${BUILD_ID:-<the build it named>} --region=${REGION} --project=${PROJECT}'; its output is in $SCRATCH/build.out and $SCRATCH/build.err"
      return 0
      ;;
    esac
    unmeasured build "the build of ${ENTRY} exited 0 but its output names no 'built <image>@<digest>' line: the image this check reads the copied file and the label out of is the one the build says it made, and without it the rows below have nothing to look at. What it printed is in $SCRATCH/build.out and $SCRATCH/build.err"
    return 0
  fi
  IMAGE=${made%% *}
  IMAGE_DIGEST=${made#* }
  BUILT=yes
  fact build PASS "the entry ${ENTRY} built through Cloud Build as build ${BUILD_ID:-unnamed} (a real, billable build, run from ${SANDBOX}), and it reported the image ${IMAGE}@${IMAGE_DIGEST}"
}

# strip_tag REF: an image reference without its tag, so a digest can be pinned to
# it (`<image>@<digest>` is invalid when the reference carries a tag of its own).
strip_tag() {
  local ref=$1 tail=${1##*/}
  case "$tail" in
  *:*) printf '%s' "${ref%:*}" ;;
  *) printf '%s' "$ref" ;;
  esac
}

# step_record: the entry's build record, out of the runs bucket. The record is
# written by the build's own last step, so a record that is not there is a build
# that did not reach the end; and it is the entry's ONE record of what was built,
# which is what the nightly decision compares and what the image's label has to
# agree with.
step_record() {
  if [ "$BUILT" != yes ]; then
    unmeasured record "no build ran, so there is no record to read: the row above says why"
    return 0
  fi
  say "reading the entry's record: gcloud storage ls gs://${BUCKET}/builds/*/catalog/${ENTRY}/image.json"
  if ! bucket_ls "$CLI_TIMEOUT" recls "gs://${BUCKET}/builds/*/catalog/${ENTRY}/image.json"; then
    unmeasured record "the listing of the entry's record did not answer (exit ${BOUNDED_STATUS}): the repository whose slug the entry is built under is read from it, and a listing that failed is not a record that is missing. Its output is in $SCRATCH/recls.err"
    return 0
  fi
  if [ "$BUCKET_LS_ANSWER" = none ]; then
    fact record FAIL "no build record for ${ENTRY} is in ${BUCKET} (gs://${BUCKET}/builds/*/catalog/${ENTRY}/image.json matched no objects): the record is written by the build's own last step, so a build that reported ${IMAGE}@${IMAGE_DIGEST} and left none did not reach the end of its steps. Read the build with 'gcloud builds log ${BUILD_ID} --region=${REGION} --project=${PROJECT}', and the entry's record key is builds/<slug>/catalog/${ENTRY}/image.json (internal/backend/gcp's CatalogRecordKey)"
    return 0
  fi
  local count
  count=$(printf '%s\n' "$BUCKET_LS_OBJECTS" | grep -c . || true)
  if [ "${count:-0}" != 1 ]; then
    fact record FAIL "${count} records for ${ENTRY} are in ${BUCKET} — one per repository slug — and the entry belongs to exactly one: $(flat "$(printf '%s' "$BUCKET_LS_OBJECTS" | tr '\n' ' ')"). An entry whose Dockerfile is read out of a repository is built under that repository's slug, and two records are two builds of one entry nothing can compare"
    return 0
  fi
  RECORD_PATH=$BUCKET_LS_OBJECTS
  SLUG=$(printf '%s' "$RECORD_PATH" | sed -n "s|^gs://[^/]*/builds/\([^/]*\)/catalog/$ENTRY/image.json$|\1|p")
  if ! bucket_cat "$CLI_TIMEOUT" record "$RECORD_PATH"; then
    unmeasured record "reading ${RECORD_PATH} did not answer (exit ${BOUNDED_STATUS}): the record is the entry's one account of what was built, and a read that failed is not a record without the fields below. Its output is in $SCRATCH/record.err"
    return 0
  fi
  if ! read_json record "$SCRATCH/record.out" "$SCRATCH/record.txt"; then
    unmeasured record "${RECORD_PATH} is not a document this check could read: what it holds is in $SCRATCH/record.out"
    return 0
  fi
  local src ctx sha def salt recimage recdigest
  src=$(field "$SCRATCH/record.txt" source_commit)
  ctx=$(field "$SCRATCH/record.txt" context_commit)
  sha=$(field "$SCRATCH/record.txt" context_sha256)
  def=$(field "$SCRATCH/record.txt" definition_sha256)
  salt=$(field "$SCRATCH/record.txt" template_salt)
  recimage=$(field "$SCRATCH/record.txt" image)
  recdigest=$(field "$SCRATCH/record.txt" image_digest)
  # The record is the authority for the image the rows below read: it is what the
  # build's own step wrote, where the console line is a rendering of it.
  if [ -n "$recimage" ] && [ "$recimage" != - ]; then IMAGE=$(strip_tag "$recimage"); fi
  if [ -n "$recdigest" ] && [ "$recdigest" != - ]; then IMAGE_DIGEST=$recdigest; fi
  if [ -z "$sha" ] || [ "$sha" = - ]; then
    fact record FAIL "the record of ${ENTRY} carries no context_sha256 (its source_commit is ${src:-none} and its context_commit ${ctx:-none}): the digest the copy staged is what the nightly check compares, and an entry recorded without one is rebuilt every night with the catalog-context reason or never at all — either way the reading this check exists to take is not there. The record is ${RECORD_PATH}"
    return 0
  fi
  if [ "$src" != "$COMMIT_A" ] || [ "$ctx" != "$COMMIT_A" ]; then
    fact record FAIL "the record of ${ENTRY} names source_commit ${src:-none} and context_commit ${ctx:-none}, and this check pushed ${COMMIT_A}: the copy pins ONE commit — the one both the Dockerfile and the context were read at — and 'one record of what was built and not two that can disagree' is the issue's own first bullet. A source_commit that is another commit means the definition came from another revision than the context, which is what the two fields exist to make unreadable. The record is ${RECORD_PATH}"
    return 0
  fi
  case "$sha" in
  [0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]*) ;;
  *)
    fact record FAIL "the record of ${ENTRY} carries context_sha256 '$(flat "$sha")', which is not a sha256: the digest is compared for equality by the nightly check and read as the context's identity everywhere, so a value that is not a digest is one nothing can compare. The record is ${RECORD_PATH}"
    return 0
    ;;
  esac
  if [ "$sha" = "$def" ]; then
    fact record FAIL "the record of ${ENTRY} carries the SAME value for context_sha256 and definition_sha256 ($(flat "$sha")): the two are different inputs — the Dockerfile's bytes and the tree the build runs on — and the nightly check reads them as two triggers so an operator can tell a Dockerfile edit from a repository edit (D1402). One value for both is the second input never being read"
    return 0
  fi
  if [ -z "$salt" ] || [ "$salt" = - ]; then
    fact record FAIL "the record of ${ENTRY} carries no template_salt: it is what says the build template the entry was built with, and without it a release that changes how every catalog image is built rebuilds no entry at all (F33, D1402). The record is ${RECORD_PATH}"
    return 0
  fi
  RECORD_OK=yes
  CONTEXT_DIGEST=$sha
  fact record PASS "the record of ${ENTRY} (${RECORD_PATH}) names ONE commit: source_commit and context_commit are both ${COMMIT_A}, the commit this check pushed — and context_sha256 is $(flat "$sha"), a digest of its own and not the Dockerfile's definition_sha256, with template_salt present. That is the entry's one account of what was built"
}

# step_content: the reading the whole issue is about. The image the catalog built
# carries the file its Dockerfile COPIES out of the context — a file of the
# repository that a catalog build could not reach before this work — and it is
# read out of the image itself rather than out of anything this check holds.
step_content() {
  if [ "$BUILT" != yes ] || [ -z "$IMAGE_DIGEST" ] || [ -z "$IMAGE" ]; then
    unmeasured content "no built image to look inside: the row above says why"
    return 0
  fi
  local ref="$IMAGE@$IMAGE_DIGEST"
  say "reading the copied file out of the image: docker run --rm --entrypoint cat ${ref} ${MARKER_DEST}"
  run_bounded "$CLI_TIMEOUT" content "$SANDBOX" docker run --rm --entrypoint cat "$ref" "$MARKER_DEST"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured content "docker run of ${ref} did not finish inside ${CLI_TIMEOUT}s: the copied file is read out of the image the build wrote, and a read that hung is not an image without the file. Its output is in $SCRATCH/content.out and $SCRATCH/content.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    local err
    err=$(cat "$SCRATCH/content.err" 2>/dev/null || true)
    case "$err" in
    *unauthorized* | *denied* | *UNAUTHENTICATED* | *credential*)
      unmeasured content "docker could not read ${ref} out of the registry: '$(flat "$(printf '%s' "$err" | tail -n 2 | head -n 1)")'. The image is the entry's own in the installation's shared registry, and reading it needs this machine's daemon logged in: run 'gcloud auth configure-docker ${REGION}-docker.pkg.dev' and run the check again. Its output is in $SCRATCH/content.err"
      return 0
      ;;
    esac
    fact content FAIL "the image ${ref} does not carry ${MARKER_DEST}: docker run --rm --entrypoint cat exited ${BOUNDED_STATUS}, and its own words are '$(flat "$(printf '%s' "$err" | tail -n 2 | head -n 1)")'. The entry's Dockerfile COPIES ${CONTEXT}/scripts/marker.sh into it, and a COPY that reaches a file under the context is the fact this whole feature exists for — a catalog build could not run it before this work (the copy, the build context and the definition are one revision of the repository). What docker printed is in $SCRATCH/content.out and $SCRATCH/content.err"
    return 0
  fi
  local got want
  got=$(tr -d '\r\n' <"$SCRATCH/content.out")
  want=$(tr -d '\r\n' <"$SCRATCH/marker.a")
  if [ "$got" != "$want" ]; then
    fact content FAIL "the image ${ref} carries ${MARKER_DEST} with other bytes than the commit ${COMMIT_A} holds: the image holds '$(flat "$got")' and ${CONTEXT}/scripts/marker.sh at that commit holds '$(flat "$want")'. The context's whole point is that the build runs on the tree at the commit the copy resolved, so bytes that are neither are a build that ran on something else. Run the check again, and if it holds, read the build with 'gcloud builds log ${BUILD_ID} --region=${REGION} --project=${PROJECT}'"
    return 0
  fi
  fact content PASS "the image ${IMAGE}@${IMAGE_DIGEST} carries the copied file: docker run --rm --entrypoint cat ${ref} ${MARKER_DEST} printed the bytes of ${CONTEXT}/scripts/marker.sh at ${COMMIT_A} — the file the entry's Dockerfile reaches through the build context, which is what no catalog build could do before this work"
}

# step_label: the image's own label against the record's commit. The build stamps
# `org.opencontainers.image.revision` with the commit it built
# (images/derived/cloudbuild.yaml), so a label that is not the record's commit is
# two accounts of one build that disagree — which is the second half of the
# issue's first bullet.
step_label() {
  if [ "$RECORD_OK" != yes ] || [ -z "$IMAGE_DIGEST" ] || [ -z "$IMAGE" ]; then
    unmeasured label "there is no record and image to read a label against: the rows above say why"
    return 0
  fi
  local ref="$IMAGE@$IMAGE_DIGEST" label
  say "reading the image's revision label: docker inspect --format '{{ index .Config.Labels \"org.opencontainers.image.revision\" }}' ${ref}"
  docker_label label "$ref"
  if [ -n "$LABEL_WHY" ]; then
    unmeasured label "${LABEL_WHY}"
    return 0
  fi
  label=$LABEL_VALUE
  if [ -z "$label" ] || [ "$label" = "<no value>" ] || [ "$label" = "null" ]; then
    fact label FAIL "the image ${ref} carries no org.opencontainers.image.revision label (docker printed '${label:-nothing}'): the build stamps it with the commit it built, and it is the half of 'one record of what was built' that lives in the image rather than in the bucket. The record names ${COMMIT_A} (${RECORD_PATH})"
    return 0
  fi
  if [ "$label" != "$COMMIT_A" ]; then
    fact label FAIL "the image ${ref} is labelled ${label} and the entry's record names ${COMMIT_A} as source_commit and context_commit (${RECORD_PATH}): the label and the record are the two accounts of ONE build, and the issue's first bullet is that they cannot disagree. A label naming another commit is a build that stamped what it read rather than what the copy pinned"
    return 0
  fi
  fact label PASS "the image ${ref} carries org.opencontainers.image.revision=${COMMIT_A} — the same commit the record names twice — so the image, the record and the commit the copy pinned are one account of what was built"
}

# step_staged: the copy, read out of the objects it wrote. The build stages three
# objects under the entry's own name before anything is paid for, and the update
# row read that prefix EMPTY earlier: what staged them is the build.
step_staged() {
  if [ "$RECORD_OK" != yes ]; then
    unmeasured staged "there is no record to compare the staged objects with: the row above says why"
    return 0
  fi
  local base="gs://${BUCKET}/builds/catalog/${ENTRY}"
  say "reading the entry's staged objects: gcloud storage ls ${base}/"
  if ! bucket_ls "$CLI_TIMEOUT" stagedls "${base}/"; then
    unmeasured staged "gcloud storage ls ${base}/ did not answer (exit ${BOUNDED_STATUS}): the objects the copy staged are what a build pins and the nightly check reads, and a listing that failed is not a prefix without them. Its output is in $SCRATCH/stagedls.err"
    return 0
  fi
  if [ "$BUCKET_LS_ANSWER" != objects ]; then
    fact staged FAIL "the build of ${ENTRY} staged nothing under ${base}/: the copy writes the Dockerfile, the commit it was read at and the context's digest there BEFORE the build is submitted (internal/cli/catalog_copy.go), so an empty prefix with a record in the bucket is a build whose copy wrote somewhere else — and the nightly check reads these objects and nothing else. The record is ${RECORD_PATH}"
    return 0
  fi
  local have_dockerfile=no
  case "$BUCKET_LS_OBJECTS" in
  *"${base}/Dockerfile"*) have_dockerfile=yes ;;
  esac
  if [ "$have_dockerfile" != yes ]; then
    fact staged FAIL "the build of ${ENTRY} staged no ${base}/Dockerfile (it staged $(flat "$(printf '%s' "$BUCKET_LS_OBJECTS" | tr '\n' ' ')")): the definition a build reads is the copied object and not your working tree, which is what makes the build one revision of one repository. The record is ${RECORD_PATH}"
    return 0
  fi
  if ! bucket_cat "$CLI_TIMEOUT" stagedcommit "${base}/source-commit"; then
    unmeasured staged "reading ${base}/source-commit did not answer (exit ${BOUNDED_STATUS}): it is the commit a rebuild is pinned to, and a read that failed is not a value. Its output is in $SCRATCH/stagedcommit.err"
    return 0
  fi
  if ! bucket_cat "$CLI_TIMEOUT" stagedhash "${base}/context-sha256"; then
    unmeasured staged "reading ${base}/context-sha256 did not answer (exit ${BOUNDED_STATUS}): it is the digest the nightly check compares, and a read that failed is not a value. Its output is in $SCRATCH/stagedhash.err"
    return 0
  fi
  local staged_commit staged_hash
  staged_commit=$(tr -d ' \r\n' <"$SCRATCH/stagedcommit.out")
  staged_hash=$(tr -d ' \r\n' <"$SCRATCH/stagedhash.out")
  if [ "$staged_commit" != "$COMMIT_A" ]; then
    fact staged FAIL "${base}/source-commit holds '$(flat "$staged_commit")' and this check pushed ${COMMIT_A}: the commit the copy staged is the one a rebuild is pinned to, so a rebuild of this entry would build a revision nothing chose. The record names ${COMMIT_A}"
    return 0
  fi
  if [ "$staged_hash" != "$CONTEXT_DIGEST" ]; then
    fact staged FAIL "${base}/context-sha256 holds '$(flat "$staged_hash")' and the record the same build wrote names '$(flat "$CONTEXT_DIGEST")' (${RECORD_PATH}): the check compares these two values, so a build that stages one and records the other decides 'the context moved' on the very night after it built — and every night after that. internal/cli/catalog_copy.go writes both from one Resolve"
    return 0
  fi
  fact staged PASS "the build of ${ENTRY} staged the definition, the commit and the context's digest under ${base}/ — source-commit ${COMMIT_A}, context-sha256 ${CONTEXT_DIGEST}, the same values the record names — while the prefix was EMPTY after 'fugaro update' (the update row): the COPY inside a build is what stages the entry's identity, which is the plan's step 1 read the right way round"
}

# ------------------------------------------- the project's catalog check job
# It reads the two staged objects out of the runs bucket and the entry's record,
# decides, and prints one JSON line per entry on STDOUT — which Cloud Run takes
# as a structured entry, so it lands in jsonPayload and not in textPayload, the
# field the installation's own alert is built on
# (deploy/terraform/gcp/modules/installation/alert.tf,
# jsonPayload.event="catalog-check"). Nothing here opens a branch: the subject of
# the decision is what the copy staged.
run_check_job() { # run_check_job KEY
  local key=$1
  CHECK_WHY=""
  CHECK_SINCE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  say "running the project's catalog check job once, as its schedule does: gcloud run jobs execute ${CHECK_JOB} --region=${REGION} --project=${PROJECT} --wait"
  run_bounded "$CHECK_TIMEOUT" "$key-run" "$SANDBOX" gcloud run jobs execute "$CHECK_JOB" \
    --region="$REGION" --project="$PROJECT" --wait
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    CHECK_WHY="gcloud run jobs execute ${CHECK_JOB} did not finish inside ${CHECK_TIMEOUT}s: the decision this row reads is the job's own line, and the job was ended before it wrote one. Its output is in $SCRATCH/$key-run.out and $SCRATCH/$key-run.err"
    return 1
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    CHECK_WHY="gcloud run jobs execute ${CHECK_JOB} exited ${BOUNDED_STATUS}: the check's own refusal is not this check's finding, and a job that did not run wrote no line for the entry. What it printed is in $SCRATCH/$key-run.err"
    return 1
  fi
  return 0
}

# entry_event KEY SINCE: the newest catalog-check line for this entry at or after
# the moment the job was started, polled for as long as Cloud Logging takes to
# ingest one. An older run's line must never be read as this run's decision,
# which is why the timestamp is carried and compared.
entry_event() { # entry_event KEY SINCE
  local key=$1 since=$2 waited=0 line=""
  EVENT_WHY=""
  while :; do
    # The retry reads the same file, and `run_bounded` appends: without this the
    # second pass holds two documents and no JSON reader can take them for one.
    : >"$SCRATCH/$key.out"
    run_bounded "$CLI_TIMEOUT" "$key" "$SANDBOX" gcloud logging read "$LOG_FILTER" \
      --project="$PROJECT" --freshness=30m --limit=100 --format=json
    if [ "$BOUNDED_TIMED_OUT" = yes ]; then
      EVENT_WHY="gcloud logging read did not answer inside ${CLI_TIMEOUT}s: the entry's decision is the job's own line and the only place it can be read. Its output is in $SCRATCH/$key.err"
      return 1
    fi
    if [ "$BOUNDED_STATUS" != 0 ]; then
      EVENT_WHY="gcloud logging read exited ${BOUNDED_STATUS}: '$(flat "$(tail -n 2 "$SCRATCH/$key.err" 2>/dev/null | head -n 1)")'. The filter is resource.type=cloud_run_job AND resource.labels.job_name=${CHECK_JOB} AND jsonPayload.event=catalog-check AND jsonPayload.entry=${ENTRY}"
      return 1
    fi
    if ! read_json events "$SCRATCH/$key.out" "$SCRATCH/$key.txt" "$since"; then
      EVENT_WHY="gcloud logging read printed a document this check could not read: what it printed is in $SCRATCH/$key.out"
      return 1
    fi
    line=$(head -n 1 "$SCRATCH/$key.txt" 2>/dev/null || true)
    if [ -n "$line" ] && [ "${line%%$'\t'*}" = event ]; then return 0; fi
    if [ "$waited" -ge "$LOG_WAIT" ]; then
      EVENT_WHY="no catalog-check line for ${ENTRY} landed within ${LOG_WAIT}s of the job run: the job prints one line per entry on stdout, so a run that left none for this entry is one this check cannot read a decision from. The filter is jsonPayload.entry=${ENTRY} and the freshness 30m"
      return 1
    fi
    sleep "$LOG_POLL"
    waited=$((waited + LOG_POLL))
  done
}

# stage_copy KEY: stage a copy whose build does NOT record it. The staging happens
# before the submission and the record is written by the build's own last step,
# so submitting with --no-wait and cancelling before that step leaves exactly the
# state the trigger is about: the staged digest and commit are the new commit and
# the record still names the previous one. A cancel means the race was won;
# `gcloud builds describe` says whether the build ended anyway, because a build
# that finished wrote the new record and the next decision is quiet — the
# register's step 5 says losing that race is not a failed step.
stage_copy() { # stage_copy KEY
  local key=$1 text
  STAGE_ID=""
  STAGE_STATUS=""
  STAGE_WHY=""
  run_bounded "$CLI_TIMEOUT" "$key-submit" "$SANDBOX" "$FUGARO_BIN" image build "$ENTRY" --no-wait
  text=$(cat "$SCRATCH/$key-submit.out" "$SCRATCH/$key-submit.err" 2>/dev/null || true)
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    STAGE_WHY="fugaro image build ${ENTRY} --no-wait did not finish inside ${CLI_TIMEOUT}s: the staging is what the decision below reads, and a submission that did not come back names no build to cancel. Its output is in $SCRATCH/$key-submit.out and $SCRATCH/$key-submit.err"
    return 1
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    STAGE_WHY="fugaro image build ${ENTRY} --no-wait exited ${BOUNDED_STATUS}: $(flat "$(printf '%s' "$text" | tail -n 2 | head -n 1)"). The staging is what the decision reads, and a submission that was refused staged nothing"
    return 1
  fi
  STAGE_ID=$(printf '%s\n' "$text" | grep -o 'Cloud Build build [A-Za-z0-9._-]*' | head -n 1 | awk '{print $4}' || true)
  if [ -z "$STAGE_ID" ]; then
    STAGE_WHY="the submission of ${ENTRY} names no Cloud Build build id, so this check cannot cancel the build the staging submitted and cannot tell a build that did not record from one that did. What it printed is in $SCRATCH/$key-submit.out"
    return 1
  fi
  say "cancelling the submitted build before its record step: gcloud builds cancel ${STAGE_ID} --region=${REGION} --project=${PROJECT}"
  run_bounded "$CLI_TIMEOUT" "$key-cancel" "$SANDBOX" gcloud builds cancel "$STAGE_ID" \
    --region="$REGION" --project="$PROJECT"
  local waited=0
  while [ "$waited" -lt "$STAGE_WAIT" ]; do
    run_bounded "$CLI_TIMEOUT" "$key-describe" "$SANDBOX" gcloud builds describe "$STAGE_ID" \
      --region="$REGION" --project="$PROJECT" --format=json
    if [ "$BOUNDED_TIMED_OUT" != yes ] && [ "$BOUNDED_STATUS" = 0 ] && read_json build "$SCRATCH/$key-describe.out" "$SCRATCH/$key-describe.txt"; then
      STAGE_STATUS=$(field "$SCRATCH/$key-describe.txt" status)
      case "$STAGE_STATUS" in
      SUCCESS | FAILURE | CANCELLED | EXPIRED | TIMEOUT | INTERNAL_ERROR) break ;;
      esac
    fi
    sleep 5
    waited=$((waited + 5))
  done
  return 0
}

# step_quiet: the negative half of the plan's step 4, and the one the register's
# section reads where the code puts it. A commit OUTSIDE the context directory,
# with the entry's Dockerfile unchanged, moves the staged source-commit but not
# the context's digest — so the decided entry carries no catalog-context. A
# decision that fired on the Dockerfile's own digest instead (catalog-definition)
# would be the same defect the other way round, so both are held absent here.
step_quiet() {
  if [ "$RECORD_OK" != yes ]; then
    unmeasured quiet "the entry's record could not be read, so there is nothing to compare a quiet decision against: the row above says why"
    return 0
  fi
  local outside="${ENTRY}-outside.txt"
  printf 'check 292: this file is outside %s/, so the context tree at that commit does not change\n' "$CONTEXT" >"$SANDBOX/$outside"
  COMMIT_B=$(git_commit "check292: a commit outside the context directory, the Dockerfile unchanged" "$outside")
  if [ -z "$COMMIT_B" ]; then
    unmeasured quiet "the commit outside ${CONTEXT}/ could not be made or pushed in $SANDBOX: what git printed is in $SCRATCH/git.err"
    return 0
  fi
  say "staging a copy of ${COMMIT_B} without recording it (fugaro image build --no-wait, cancelled)"
  if ! stage_copy quiet; then
    unmeasured quiet "${STAGE_WHY}"
    return 0
  fi
  if [ "$STAGE_STATUS" = SUCCESS ]; then
    QUIET_NOTE="the submitted build ${STAGE_ID} had already run SUCCESS when the check job was run, so the record moved too"
  fi
  if ! run_check_job quiet; then
    unmeasured quiet "${CHECK_WHY}"
    return 0
  fi
  if ! entry_event quiet "$CHECK_SINCE"; then
    unmeasured quiet "${EVENT_WHY}"
    return 0
  fi
  local decision reasons message
  decision=$(ev_field "$SCRATCH/quiet.txt" 3)
  reasons=$(ev_field "$SCRATCH/quiet.txt" 4)
  message=$(ev_field "$SCRATCH/quiet.txt" 6)
  case ",$reasons," in
  *",catalog-context,"*)
    fact quiet FAIL "a commit outside ${CONTEXT}/ (${COMMIT_B}, which changes no file under the context and not the entry's Dockerfile) made the catalog check decide '${decision}' with '${reasons}' for ${ENTRY}: '$(flat "$message")'. The context's digest is a function of the tree under ${CONTEXT}/ alone (internal/catalog's contextDigest), so a commit outside it must not move it — a decision that fires here rebuilds the entry on every commit of the repository, and it is the negative half of the issue's second bullet. The submitted build was ${STAGE_ID:-none} (${STAGE_STATUS:-no status read})"
    return 0
    ;;
  *",catalog-definition,"*)
    fact quiet FAIL "a commit outside ${CONTEXT}/, with the entry's Dockerfile unchanged, made the catalog check decide '${decision}' with '${reasons}' for ${ENTRY}: '$(flat "$message")'. The definition's own digest is a sha256 of the copied Dockerfile's bytes and ${outside} is not that file, so this trigger firing is the copied object having moved when nothing wrote it — and the entry would rebuild on every commit. The submitted build was ${STAGE_ID:-none}"
    return 0
    ;;
  esac
  fact quiet PASS "a commit outside ${CONTEXT}/ (${COMMIT_B}), with the entry's Dockerfile unchanged, left the entry's decision without the context's reason: the catalog check decided '${decision}' with '${reasons:-no reason}' — '$(flat "$message")'. The staged source-commit moved to ${COMMIT_B} while the context's digest did not, which is exactly the reading the plan's step 4 asks for and cannot get from a pushed commit alone${QUIET_NOTE:+, though ${QUIET_NOTE}}"
}

# ------------------------------------------------------------------- the label
# docker_label KEY REF: the image's `org.opencontainers.image.revision`, read out
# of the daemon — pulling the digest first when the daemon has not got it, because
# a local cache without the image is this machine's state and not a finding about
# a build. LABEL_VALUE holds what docker printed and LABEL_WHY the read that did
# not answer; the value is empty in both when the read answered nothing.
LABEL_VALUE=""
LABEL_WHY=""
docker_label() { # docker_label KEY REF
  local key=$1 ref=$2
  LABEL_VALUE=""
  LABEL_WHY=""
  run_bounded "$CLI_TIMEOUT" "$key" "$SANDBOX" docker inspect \
    --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}' "$ref"
  if [ "$BOUNDED_TIMED_OUT" != yes ] && [ "$BOUNDED_STATUS" != 0 ]; then
    say "the image is not in this machine's daemon: docker pull ${ref}"
    run_bounded "$CLI_TIMEOUT" "$key-pull" "$SANDBOX" docker pull "$ref"
    if [ "$BOUNDED_TIMED_OUT" != yes ] && [ "$BOUNDED_STATUS" = 0 ]; then
      run_bounded "$CLI_TIMEOUT" "$key" "$SANDBOX" docker inspect \
        --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}' "$ref"
    fi
  fi
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    LABEL_WHY="docker could not read ${ref} inside ${CLI_TIMEOUT}s: the label is a property of the image the build pushed, and a read that hung is not an image without one. Its output is in $SCRATCH/$key.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    LABEL_WHY="docker could not read ${ref} (exit ${BOUNDED_STATUS}): '$(flat "$(tail -n 2 "$SCRATCH/$key.err" 2>/dev/null | head -n 1)")'. Run 'gcloud auth configure-docker ${REGION}-docker.pkg.dev' if the daemon is not logged in, and run the check again"
    return 0
  fi
  LABEL_VALUE=$(tail -n 1 "$SCRATCH/$key.out" | tr -d ' \r\n')
}

# step_trigger: the half of the plan's step 4 the code can be read at. A commit
# UNDER the context directory moves the digest the copy stages, while the record
# still names the digest of the build before it — and the check job, which reads
# only the two staged objects and the record, has to decide `rebuild` with the
# `catalog-context` reason and submit a build pinned to the staged commit. The
# staged build is cancelled before its record step, so the difference the trigger
# is about is real; a build that had already recorded is a lost race, and the
# register's step 5 says that is not a failed step — this pushes another commit
# and looks again.
step_trigger() {
  if [ "$RECORD_OK" != yes ]; then
    unmeasured trigger "the entry's record could not be read, so there is no digest for a changed context to differ from: the row above says why"
    return 0
  fi
  local attempt=0 decision reasons message race=""
  while :; do
    attempt=$((attempt + 1))
    if [ "$attempt" -gt 3 ]; then
      unmeasured trigger "$race — and the third attempt lost it too, so this check could not put the staged digest ahead of the record without a build of its own recording it. Run the check again, or cancel the submitted build by hand: 'fugaro image build ${ENTRY} --no-wait' then 'gcloud builds cancel <build id> --region=${REGION} --project=${PROJECT}'"
      return 0
    fi
    printf 'check 292: the file the context copies, moved (attempt %d)\n' "$attempt" \
      >"$SANDBOX/$CONTEXT/scripts/marker.sh"
    COMMIT_C=$(git_commit "check292: a commit under ${CONTEXT}/ that moves the copied file (attempt ${attempt})" "$CONTEXT/scripts/marker.sh")
    if [ -z "$COMMIT_C" ]; then
      unmeasured trigger "the commit under ${CONTEXT}/ could not be made or pushed in $SANDBOX: what git printed is in $SCRATCH/git.err"
      return 0
    fi
    say "staging a copy of ${COMMIT_C} without recording it (fugaro image build --no-wait, cancelled)"
    if ! stage_copy trigger-stage; then
      unmeasured trigger "${STAGE_WHY}"
      return 0
    fi
    if [ "$STAGE_STATUS" != SUCCESS ]; then
      race=""
      break
    fi
    race="the submitted build ${STAGE_ID} had already run SUCCESS when it was cancelled, so the record moved with the staged digest and the next decision is quiet for the right reason"
    say "$race"
  done
  say "running the project's catalog check job once, as its schedule does"
  if ! run_check_job trigger; then
    unmeasured trigger "${CHECK_WHY}"
    return 0
  fi
  if ! entry_event trigger "$CHECK_SINCE"; then
    unmeasured trigger "${EVENT_WHY}"
    return 0
  fi
  decision=$(ev_field "$SCRATCH/trigger.txt" 3)
  reasons=$(ev_field "$SCRATCH/trigger.txt" 4)
  REBUILD_ID=$(ev_field "$SCRATCH/trigger.txt" 5)
  message=$(ev_field "$SCRATCH/trigger.txt" 6)
  [ "$REBUILD_ID" = - ] && REBUILD_ID=""
  case ",$reasons," in
  *",catalog-context,"*) ;;
  *)
    fact trigger FAIL "a commit under ${CONTEXT}/ that moves the file the entry's Dockerfile copies (${COMMIT_C}) left the entry's decision without the context's reason: the check decided '${decision}' with '${reasons:-no reason}' — '$(flat "$message")'. The digest a copy stages is a function of the tree under ${CONTEXT}/ alone (internal/catalog's contextDigest), and this commit moved it, so a decision that does not name catalog-context is a rebuild trigger that never fires: every job of the installation then reads the image ${RECORD_PATH} records, built from a tree this commit changed. The decision is written to builds/${SLUG}/catalog/${ENTRY}/check.json"
    return 0
    ;;
  esac
  if [ "$decision" != rebuild ]; then
    fact trigger FAIL "a commit under ${CONTEXT}/ that moves the copied file (${COMMIT_C}) made the check name catalog-context among '${reasons}' and decide '${decision}' rather than rebuild: '$(flat "$message")'. The reason without the decision is a check that sees the context move and does not build — the nightly job would report it for ever and the entry would never catch up. The decision is written to builds/${SLUG}/catalog/${ENTRY}/check.json"
    return 0
  fi
  if [ -z "$REBUILD_ID" ]; then
    fact trigger FAIL "the check decided rebuild with catalog-context for ${ENTRY} and its line names no build id ('$(flat "$message")'): the decision is worth what the build it submits is worth, and the rebuild has to be pinned to the commit the copy staged (${COMMIT_C}) rather than to a branch. The line is the job's own stdout (jsonPayload.event=catalog-check), read back from Cloud Logging"
    return 0
  fi
  REBUILD_STAGED="$COMMIT_C"
  fact trigger PASS "a commit under ${CONTEXT}/ (${COMMIT_C}) that moves the file the entry's Dockerfile copies made the catalog check decide 'rebuild' with the reason 'catalog-context' — '$(flat "$message")' — and submit build ${REBUILD_ID}: the trigger is the digest the copy staged and not the branch, which is why the plan's step 4 cannot be run by pushing a commit alone and this check stages one instead${race:+. $race}"
}

# step_rebuild: what the decision submitted, and the record it wrote. The rebuild
# is pinned to the commit the copy staged, so the record has to name ONE commit
# again after it — and the image it made has to carry that same commit as its
# revision label. This is the plan's step 3 read on a build nothing but the check
# asked for.
step_rebuild() {
  if [ -z "$REBUILD_ID" ]; then
    unmeasured rebuild "the decision named no build to follow, so no rebuild ran: the row above says why"
    return 0
  fi
  say "following the rebuild the decision submitted: gcloud builds describe ${REBUILD_ID} --region=${REGION} --project=${PROJECT} (up to ${REBUILD_TIMEOUT}s)"
  local waited=0 status="" rebuilt=no
  while [ "$waited" -lt "$REBUILD_TIMEOUT" ]; do
    run_bounded "$CLI_TIMEOUT" rebuild-desc "$SANDBOX" gcloud builds describe "$REBUILD_ID" \
      --region="$REGION" --project="$PROJECT" --format=json
    if [ "$BOUNDED_TIMED_OUT" != yes ] && [ "$BOUNDED_STATUS" = 0 ] && read_json build "$SCRATCH/rebuild-desc.out" "$SCRATCH/rebuild-desc.txt"; then
      status=$(field "$SCRATCH/rebuild-desc.txt" status)
      case "$status" in
      SUCCESS)
        rebuilt=yes
        break
        ;;
      FAILURE | CANCELLED | EXPIRED | TIMEOUT | INTERNAL_ERROR)
        fact rebuild FAIL "the rebuild ${REBUILD_ID} the catalog check submitted for ${ENTRY} ended ${status}: the check decided rebuild with catalog-context and the build it asked for did not make an image, so the decision leads nowhere — the entry's next job still runs the image built from the tree before ${REBUILD_STAGED}, and the nightly check decides the same thing again tomorrow. Read the build with 'gcloud builds log ${REBUILD_ID} --region=${REGION} --project=${PROJECT}'. internal/imagecheck decides the rebuild; the submitting is the job's own (internal/backend/gcp's CatalogCheckJob)"
        return 0
        ;;
      esac
    fi
    sleep "$LOG_POLL"
    waited=$((waited + LOG_POLL))
  done
  if [ "$rebuilt" != yes ]; then
    unmeasured rebuild "the rebuild ${REBUILD_ID} had not reached a terminal status inside ${REBUILD_TIMEOUT}s (the last status read was '${status:-none}'): a Cloud Build still running is a longer look than this row can take, and what it has made so far is what the row below would read. Follow it with 'gcloud builds describe ${REBUILD_ID} --region=${REGION} --project=${PROJECT}'"
    return 0
  fi
  if ! bucket_cat "$CLI_TIMEOUT" rrecord "$RECORD_PATH"; then
    unmeasured rebuild "reading ${RECORD_PATH} after the rebuild did not answer (exit ${BOUNDED_STATUS}): the record the rebuild wrote is the whole of this row, and a read that failed is not a record that did not move. Its output is in $SCRATCH/rrecord.err"
    return 0
  fi
  if ! read_json record "$SCRATCH/rrecord.out" "$SCRATCH/rrecord.txt"; then
    unmeasured rebuild "the record of ${ENTRY} after the rebuild is not a document this check could read: what it holds is in $SCRATCH/rrecord.out"
    return 0
  fi
  local src ctx sha recimage recdigest
  src=$(field "$SCRATCH/rrecord.txt" source_commit)
  ctx=$(field "$SCRATCH/rrecord.txt" context_commit)
  sha=$(field "$SCRATCH/rrecord.txt" context_sha256)
  recimage=$(field "$SCRATCH/rrecord.txt" image)
  recdigest=$(field "$SCRATCH/rrecord.txt" image_digest)
  if [ "$src" != "$REBUILD_STAGED" ] || [ "$ctx" != "$REBUILD_STAGED" ]; then
    fact rebuild FAIL "the rebuild ${REBUILD_ID} wrote a record naming source_commit ${src:-none} and context_commit ${ctx:-none}, and the commit the copy staged for it was ${REBUILD_STAGED}: a rebuild is pinned to the STAGED commit — the one whose context digest the decision compared — and a record naming another one is the rebuild having read a branch instead, which is the reading the whole check is about. The record is ${RECORD_PATH}"
    return 0
  fi
  if [ "$sha" = "$CONTEXT_DIGEST" ]; then
    fact rebuild FAIL "the rebuild ${REBUILD_ID} recorded the same context_sha256 it recorded before (${CONTEXT_DIGEST}, ${RECORD_PATH}) while its record names ${REBUILD_STAGED}: the commit the copy staged for it is not the commit recorded, or the context's digest is not a function of the tree under ${CONTEXT}/ — either way the decision would compare one digest for ever and the entry would rebuild every night. What the record holds is in $SCRATCH/rrecord.out"
    return 0
  fi
  if [ -z "$recimage" ] || [ "$recimage" = - ] || [ -z "$recdigest" ] || [ "$recdigest" = - ]; then
    unmeasured rebuild "the record ${RECORD_PATH} the rebuild wrote names no image ('$(flat "${recimage:-none}")'@'$(flat "${recdigest:-none}")'): the label this row reads is the image's, and a record without one names nothing to read. What the record holds is in $SCRATCH/rrecord.out"
    return 0
  fi
  local ref
  ref="$(strip_tag "$recimage")@$recdigest"
  say "reading the rebuilt image's revision label: docker inspect --format '{{ index .Config.Labels \"org.opencontainers.image.revision\" }}' ${ref}"
  docker_label rebuild-label "$ref"
  if [ -n "$LABEL_WHY" ]; then
    unmeasured rebuild "${LABEL_WHY}"
    return 0
  fi
  if [ "$LABEL_VALUE" != "$REBUILD_STAGED" ]; then
    fact rebuild FAIL "the image ${ref} the rebuild ${REBUILD_ID} made is labelled '$(flat "${LABEL_VALUE:-nothing}")' and the record it wrote names ${REBUILD_STAGED} (${RECORD_PATH}): the label is stamped with the commit the build read, so a label naming another commit is a rebuild of something other than the commit the check compared and decided on. The image the entry now points at is ${ref}"
    return 0
  fi
  fact rebuild PASS "the decision's build ${REBUILD_ID} ran to SUCCESS and wrote ${RECORD_PATH}: source_commit and context_commit are both ${REBUILD_STAGED}, the commit the copy staged and the check compared, its context_sha256 moved off the previous build's (${CONTEXT_DIGEST} → $(flat "$sha")), and the image it made (${ref}) carries org.opencontainers.image.revision=${REBUILD_STAGED} — so a decision to rebuild produces an image whose label, record and staged commit are one account, which is the issue's first bullet read on a build nothing but the check asked for"
}

# step_converge: the second night. The check state the decision wrote is what the
# next run reads, and the rebuild has just moved the record onto the staged digest
# — so a second run of the same job must decide WITHOUT the context's reason. A
# decision that fires twice on the same digest is the entry rebuilding every
# night, which is what the issue's third bullet is about.
step_converge() {
  if [ -z "$REBUILD_STAGED" ] || [ "$RECORD_OK" != yes ]; then
    unmeasured converge "no rebuild was read, so there is no new record for a second decision to agree with: the rows above say why"
    return 0
  fi
  say "running the project's catalog check job again: the decision the rebuild left behind has to be quiet"
  if ! run_check_job converge; then
    unmeasured converge "${CHECK_WHY}"
    return 0
  fi
  if ! entry_event converge "$CHECK_SINCE"; then
    unmeasured converge "${EVENT_WHY}"
    return 0
  fi
  local decision reasons message
  decision=$(ev_field "$SCRATCH/converge.txt" 3)
  reasons=$(ev_field "$SCRATCH/converge.txt" 4)
  message=$(ev_field "$SCRATCH/converge.txt" 6)
  case ",$reasons," in
  *",catalog-context,"*)
    fact converge FAIL "a second run of the catalog check, right after the rebuild it asked for had recorded ${REBUILD_STAGED}, decided '${decision}' WITH the context's reason again: '$(flat "$message")'. The record now carries the digest the copy stages, so the two agree and there is nothing left to rebuild — a decision that fires again is the entry rebuilding on every run, which never converges and bills a Cloud Build a night. The state the decision writes is builds/${SLUG}/catalog/${ENTRY}/check.json"
    return 0
    ;;
  esac
  fact converge PASS "a second run of the catalog check, after the rebuild it asked for recorded ${REBUILD_STAGED}, decided '${decision}' with '${reasons:-no reason}' — without the context's reason: '$(flat "$message")'. The digest the copy stages and the digest the record carries are the same value again, so the decision the rebuild was asked for converges and the next night is quiet"
}

# ------------------------------------------------------------------- the shape
# belong_row: the row the block prints for the one name this check never runs
# against. It is recorded once, after every value that could carry the name has
# been read and guarded, so a PASS here is a statement about the run and not
# about a path argument.
belong_row() {
  fact belong PASS "nothing this check reads names belong: ${SANDBOX:+the sandbox checkout ${SANDBOX}, }${REPO:+its origin ${REPO}, }${INSTALL:+the installation ${INSTALL}, }${PROJECT:+the project ${PROJECT}, }${REGION:+the region ${REGION}, }and the entry name ${ENTRY:-<none>} — Check 292 writes a catalog entry, pushes commits and submits billable Cloud Builds, so it is a sandbox check or it is nothing"
}

# restore: what this check changed, said rather than assumed. It undoes the two
# things it can undo — the entry it added to this machine's config, and the
# checkout it moved onto a throwaway branch — and NAMES the things it cannot:
# the branch and its commits on the remote (it pushes through your checkout's own
# git and holds no code-host credential of its own), the image in the registry
# (nothing here deletes an image), the staged objects and the record in the runs
# bucket (a build writes them on any day), and the catalog's own build step, which
# every --no-apply this check used left owed to `fugaro apply`.
restore() {
  RESTORE_VERDICT=PASS
  if [ -n "$DRY_RUN" ]; then
    RESTORE_NOTE="this was a dry run: nothing was created, nothing was run, and no command was given to fugaro, gcloud, docker or git"
    return 0
  fi
  local why=""
  if [ "$BRANCH_MADE" = yes ] && [ -n "$ORIG_REF" ]; then
    say "putting the checkout back on ${ORIG_REF}: git checkout ${ORIG_REF} (in ${SANDBOX})"
    run_bounded "$CLI_TIMEOUT" back "$SANDBOX" git checkout -q "$ORIG_REF"
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
      RESTORE_VERDICT=FAIL
      why="the checkout at ${SANDBOX} is still on ${BRANCH}: 'git checkout ${ORIG_REF}' exited ${BOUNDED_STATUS}, and what it printed is in $SCRATCH/back.err"
    fi
  fi
  if [ "$REPO_ADDED" = yes ]; then
    say "taking the entry back out of this machine's catalog: fugaro image rm ${ENTRY} --no-apply (in ${SANDBOX})"
    run_bounded "$CLI_TIMEOUT" rm "$SANDBOX" "$FUGARO_BIN" image rm "$ENTRY" --no-apply
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
      RESTORE_VERDICT=FAIL
      why="${why:+$why; }the entry ${ENTRY} is still in ${CFG}: 'fugaro image rm ${ENTRY} --no-apply' exited ${BOUNDED_STATUS} ($(flat "$(tail -n 2 "$SCRATCH/rm.err" 2>/dev/null | head -n 1)")) — remove it by hand, or with 'fugaro image rm ${ENTRY}' from ${SANDBOX}, and run 'fugaro update'"
    else
      say "publishing the catalog without the entry: fugaro update (in ${SANDBOX})"
      run_bounded "$UPDATE_TIMEOUT" rmupdate "$SANDBOX" "$FUGARO_BIN" update
      if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
        RESTORE_VERDICT=FAIL
        why="${why:+$why; }the entry is out of ${CFG} but the node was not republished: 'fugaro update' exited ${BOUNDED_STATUS} ($(flat "$(tail -n 2 "$SCRATCH/rmupdate.err" 2>/dev/null | head -n 1)")) — run it again from ${SANDBOX}"
      fi
    fi
  fi
  local removed="" keys=""
  if [ "$REPO_ADDED" = yes ]; then
    local pair key pattern
    for pair in "staged:gs://${BUCKET}/builds/catalog/${ENTRY}/*" "record:gs://${BUCKET}/builds/*/catalog/${ENTRY}/*"; do
      key=${pair%%:*}
      pattern=${pair#*:}
      say "removing the entry's own objects: gcloud storage rm ${pattern}"
      if ! bucket_rm "$CLI_TIMEOUT" "rm-${key}" "$pattern"; then
        RESTORE_VERDICT=FAIL
        why="${why:+$why; }the objects ${pattern} names are still in ${BUCKET}: 'gcloud storage rm ${pattern}' exited ${BOUNDED_STATUS}$([ "$BUCKET_RM_ANSWER" = timeout ] && printf ' (it did not finish inside %ss)' "$CLI_TIMEOUT") ($(flat "$(tail -n 2 "$SCRATCH/rm-${key}.err" 2>/dev/null | head -n 1)")) — remove them by hand with 'gcloud storage rm ${pattern}', and never remove builds/image-lock.json, which is the project's and shared with every other entry"
        keys="${keys:+$keys, }${pattern}"
      fi
    done
    if [ -z "$keys" ]; then
      removed="The staged objects, the record and the check state this check wrote were removed from ${BUCKET} (gs://${BUCKET}/builds/catalog/${ENTRY}/ and gs://${BUCKET}/builds/*/catalog/${ENTRY}/), so the next nightly run has nothing of this entry to decide. builds/image-lock.json is the project's and was not touched."
    fi
  fi
  local leaves="This check ran no job and opened no pull request."
  if [ "$BRANCH_MADE" = yes ]; then
    leaves="$leaves The branch ${BRANCH} and the commits on it (${COMMIT_A}${COMMIT_B:+, ${COMMIT_B}}${COMMIT_C:+, ${COMMIT_C}}) stay in ${REPO}: this check pushes through your checkout's own git and holds no code-host credential of its own, so it deletes no branch — delete it with 'git push origin --delete ${BRANCH}' when you are done with it"
  fi
  if [ "$BUILT" = yes ]; then
    leaves="$leaves The image${BUILD_ID:+ build ${BUILD_ID}} made is in ${REGION}-docker.pkg.dev and nothing here deletes an image from a registry: it is what the entry's own builds push, and your policy's retention is what removes it"
  fi
  if [ -n "$removed" ]; then
    leaves="$leaves $removed"
  fi
  leaves="$leaves The catalog's own build step is OWED in this installation, because every write this check made used --no-apply (D1565): run 'fugaro apply' before you trust the catalog's image to be current — 'fugaro apply --status' lists what is owed and changes nothing"
  if [ "$RESTORE_VERDICT" = PASS ]; then
    RESTORE_NOTE="$leaves"
  else
    RESTORE_NOTE="$why. $leaves"
  fi
}

cleanup() {
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -z "$facts" ]; then facts="aborted|FAIL|the run stopped before it recorded anything"; fi
  facts="${facts}"$'\n'"restore|${RESTORE_VERDICT:-FAIL}|${RESTORE_NOTE:-the restore did not run}"
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"scratch-removed|PASS|nothing was created: the run stopped before it made a scratch directory"
  else
    facts="${facts}"$'\n'"scratch-removed|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is still there, at $SCRATCH")"
  fi
  local key verdict failed=no
  while IFS='|' read -r key verdict _; do
    [ "$verdict" = FAIL ] || continue
    case " ${UNMEASURED_KEYS:-} " in *" $key "*) continue ;; esac
    failed=yes
  done <<<"$facts"
  if [ "$failed" = yes ] && [ "$status" = 0 ]; then status=1; fi
  if [ -n "$UNMEASURED_KEYS" ] && [ "$status" = 0 ]; then
    say "this run could NOT measure ${UNMEASURED_KEYS// /, }: read the FAIL row(s) above, settle it, and run the check again"
    status=2
  fi
  print_block "$facts" "$status"
  exit "$status"
}

# -------------------------------------------------------------------- the plan
plan() {
  say "CHECK 292 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] fugaro doctor --json in ${SANDBOX} — the installation, the project, the region and the base image"
  say "  [local write] fugaro image add ${ENTRY:-<entry>} --dockerfile <repo>:${CONTEXT}/Dockerfile --branch chk292-<hex> --tags context-check --no-apply, then the source.context member edited into the project config"
  say "  [network, writes the project config and the published node] fugaro update — minutes of Terraform, and it must ask nothing and stage nothing"
  say "  [network, BILLABLE] one Cloud Build — fugaro image build ${ENTRY:-<entry>} from ${SANDBOX}"
  say "  [read] gcloud storage — the entry's record, and the three objects the copy staged under the entry's own prefix"
  say "  [local, pulls when needed] docker run --entrypoint cat <image>@<digest> <the copied file>, and docker inspect of the revision label"
  say "  [network, BILLABLE] two more Cloud Builds, each submitted with --no-wait and cancelled before its record step, so the staged digest is ahead of the record"
  say "  [cloud write] two runs of the catalog check job (gcloud run jobs execute ${CHECK_JOB:-the catalog check job of this installation} --wait) and the decision each prints, read back from Cloud Logging"
  say "  [network, BILLABLE] the rebuild the decision submits, followed with gcloud builds describe, and the record and label it writes"
  say "  [local write, undone] one throwaway branch, three commits pushed to it, and an entry added to and removed from the project config"
  say "  [cloud write, undone] the entry's staged objects, its record and its check state removed with gcloud storage rm, and the node republished without it"
  say "  [left behind, named] the branch and its commits on the remote (this check holds no code-host credential), the image in the shared registry, and the catalog's build step --no-apply left owed to fugaro apply"
  local key
  for key in sandbox entry update build content record label staged quiet trigger rebuild converge; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no doctor, no entry, no update, no build, no check job, no docker read and no git write"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(fugaro-version xtrace agent-session belong sandbox entry update build
  content record label staged quiet trigger rebuild converge restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 292 RESULT\n' >&3
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
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 292 RESULT END (exit %s)\n' "$status" >&3
}

on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

# ---------------------------------------------------------------------- inputs
SANDBOX_CLI=""
ENTRY_CLI=""
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of a SANDBOX repository, which this check commits into and builds the entry's Dockerfile out of"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --entry)
    [ $# -ge 2 ] || die "--entry needs a catalog entry name: the entry this check adds, publishes, builds and then takes back out"
    ENTRY_CLI=$2
    shift 2
    ;;
  --dry-run)
    DRY_RUN_CLI=1
    shift
    ;;
  -h | --help)
    usage
    exit 0
    ;;
  *)
    say "unknown argument: $1"
    exit 2
    ;;
  esac
done

SANDBOX=${CHECK292_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 292 needs --sandbox: the checkout of a SANDBOX repository. This check writes a catalog entry whose Dockerfile is read out of it, pushes three commits to a throwaway branch of it and submits billable Cloud Builds — so it is a sandbox repository and never the repository you work in. Usage: bash scripts/live-checks/check292.sh --sandbox <a sandbox repository checkout>"
fi
# Absolute, before anything changes directory: every command below runs from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
ENTRY=${CHECK292_ENTRY:-${ENTRY_CLI:-}}
DRY_RUN=${CHECK292_DRY_RUN:-$DRY_RUN_CLI}
# The names this run writes with, made unique so that two runs — or a rerun after
# one was interrupted — never collide on a branch, an entry, a staged object or a
# decision. The entry is a name no project has, which is what makes it safe to add
# to a real catalog and take back out.
if [ -z "$ENTRY" ]; then ENTRY="chk292-$(python3 -c 'import os; print(os.urandom(3).hex())')"; fi
BRANCH="chk292-$(python3 -c 'import os; print(os.urandom(4).hex())')"
CHECK_JOB=${CHECK292_CHECK_JOB:-fugarochk-catalog}
LOG_FILTER="resource.type=cloud_run_job AND resource.labels.job_name=${CHECK_JOB} AND jsonPayload.event=catalog-check AND jsonPayload.entry=${ENTRY}"

main() {
  need_tools
  FUGARO_BIN=$(command -v fugaro || true)
  [ -n "$FUGARO_BIN" ] || die "Check 292 needs 'fugaro' on PATH: the check drives the CLI you have installed, and it is not there"
  scratch_up
  trap on_error ERR
  write_reader
  write_ctx
  guard_xtrace
  guard_agent_session
  guard_belong "$SANDBOX" "the --sandbox path" belong
  guard_belong "$ENTRY" "the entry name" belong
  version_row
  if [ -n "$DRY_RUN" ]; then
    belong_row
    plan
    return 0
  fi
  step_sandbox
  step_entry
  belong_row
  step_update
  step_build
  step_record
  step_content
  step_label
  step_staged
  step_quiet
  step_trigger
  step_rebuild
  step_converge
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
