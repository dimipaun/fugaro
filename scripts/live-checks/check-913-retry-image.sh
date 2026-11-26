#!/usr/bin/env bash
# Check 913 — every running execution of a project's jobs still runs the image
# it was created with, read with gcloud right after `fugaro update`. Run by YOU,
# with a run in flight. It settles U1 of docs/repo-override-rulings.
#
# U1 asks whether a RETRIED execution of a run launched before `fugaro update`
# boots on the image the update rebuilt: if it does, D3304's refusal (a
# release-A runner refuses at bootstrap a pre-L8 task naming a repository
# recipe) could meet an in-flight run whose task names a repository recipe, mid
# retry. It is the platform's behaviour and not this repository's code, so
# nothing offline answers it — what answers it is the image a real execution
# records after a real update put a new one on its job.
#
# One row per criterion:
#
#   premise  the project's local config names a GCP project and a region, the
#            jobs of that region were listed, and at least one execution of one
#            of them is RUNNING (an execution Cloud Run has not completed —
#            its status carries no completionTime). Where none is in flight the
#            row reads UNVERIFIED: the state this check is about is an execution
#            launched before the update and still running after it, and a check
#            that measured nothing settles nothing.
#   exec     one row per running execution: the image digest the execution RECORDS
#            (its own spec's container image, resolved through the registry where
#            it is a tag) against the digest its job's template now resolves to.
#            DIFFERENT is PASS: it keeps its own image, so a retry of it takes
#            the image it was created with and never the rebuilt one. THE SAME is
#            FAIL where that execution's status says a task has RETRIED
#            (`status.retriedCount`): the retry took the rebuilt image, U1 is
#            settled the other way, and D3304's refusal can meet this run. THE
#            SAME with no retry is UNVERIFIED: nothing says whether the job's
#            image moved under this execution at all — that is the reading of a
#            run started after the update, or of an update that rebuilt nothing.
#   job      one row per job whose executions could NOT be read, or whose listing
#            came back with exactly the window asked for (so a running execution
#            may sit outside it): UNVERIFIED, because a job whose executions were
#            not read is not a job with none running.
#   writes   every call this check made was a READ (the list is in the row), no
#            token was read and none was on an argv, and no token is printed.
#   restore  the scratch directory is gone from the exit trap.
#
# The job's current image is NOT itself a digest: a Fugaro job is deployed with
# a moving tag (`:latest`), so the digest is resolved from the registry
# (`gcloud artifacts docker images describe`, as Check 390 reads it). A digest
# an execution already records is used as it is. Nothing is written anywhere.
#
# Usage:   bash scripts/live-checks/check-913-retry-image.sh <project> [--dry-run]
#          bash scripts/live-checks/check-913-retry-image.sh --help
#
#          <project>      the Fugaro project whose installation is read, whose
#                         local config (CHECK913_CONFIG, else
#                         $XDG_CONFIG_HOME/fugaro/projects/<project>.yaml) names
#                         its GCP project and region.
#          --dry-run      prints the plan and calls NOTHING.
# Needs:   bash, mktemp, python3, and a gcloud on PATH (CHECK913_GCLOUD names
#          another one) that may read Cloud Run jobs, executions and Artifact
#          Registry on the installation (roles/run.viewer and a registry read —
#          the operator role holds both).
# Calls:   gcloud run jobs list; gcloud run jobs executions list, one per job;
#          gcloud artifacts docker images describe, one per distinct image
#          reference that is a tag. Every call runs with stdin closed.
# Where:   the project's own cloud objects, READ ONLY. No run is launched and
#          nothing is written.
# Costs:   nothing.
# Runs:    by YOU, right after release A's `fugaro update`, with a run in
#          flight. No agent runs any step of it — it refuses to start in a
#          coding agent's session (CLAUDECODE, CLAUDE_CODE_ENTRYPOINT,
#          CLAUDE_CODE_REMOTE, CURSOR_AGENT, AI_AGENT) and refuses a shell
#          started with -x. Its own test
#          (scripts/live-checks/check-913-retry-image_test.sh) drives it against
#          a fake `gcloud` on PATH, and touches no cloud.
# Cleanup: the scratch directory is removed from the exit trap, and the
#          `restore` row says whether it really went.
# Output:  progress on stderr, ONE block on stdout headed CHECK 913 RESULT, to
#          copy back. Its last line is the verdict, and the verdict is the exit
#          status:
#
#            CHECK 913 RESULT: PASS (exit 0)        every running execution keeps
#                                                   its own image;
#            CHECK 913 RESULT: FAIL (exit 1)        an execution that retried
#                                                   runs its job's rebuilt image;
#            CHECK 913 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                   could judge;
#            CHECK 913 RESULT: INCOMPLETE (exit 3)  it judged, and a row reads
#                                                   UNVERIFIED.
#
# NO secret is read at all: this check holds no credential of its own, calls no
# `gcloud auth` and prints no token. Its rows carry job names, execution names
# and image digests.
set -euo pipefail

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }

XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null

FACTS=""
SCRATCH=""
SCRATCH_REMOVED=no
REACHED_END=no
ABORT=""
PROJECT=""

stop() {
  printf '%s: stopped: %s\n' "$PROG" "$1" >&2
  ABORT=$1
  exit 2
}

fact() { # fact KEY PASS|FAIL|UNVERIFIED "detail"
  [ -n "$FACTS" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS"
}

need() {
  command -v "$1" >/dev/null 2>&1 || stop "$1 is not on PATH, and this check reads a real installation with it"
}

guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 913: it reads a real installation's jobs and executions with the owner's own gcloud. Run it yourself: bash scripts/live-checks/check-913-retry-image.sh <project>"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x), and its trace would print the arguments of every command this check runs. Run it without -x: bash scripts/live-checks/check-913-retry-image.sh <project>"
  fi
}

usage() {
  cat <<'USAGE'
Check 913 — every running execution of a project's jobs still runs the image it
was created with, read with gcloud right after `fugaro update`.

Usage:
  bash scripts/live-checks/check-913-retry-image.sh <project> [--dry-run]
  bash scripts/live-checks/check-913-retry-image.sh --help

Run it with a run in flight, right after release A's `fugaro update`: for every
execution Cloud Run has not completed, the image digest it records is compared
with the digest its job's template now resolves to. An execution that keeps its
own image passes; one that runs the job's current image AND has retried is the
retry U1 asks about, and fails. The block's last line is the verdict and the
exit status:

  PASS (exit 0)        every running execution keeps its own image
  FAIL (exit 1)        an execution that retried runs its job's rebuilt image
  INCOMPLETE (exit 2)  the run stopped before it could judge
  INCOMPLETE (exit 3)  at least one row reads UNVERIFIED
USAGE
}

DRY_RUN=no
while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  --dry-run)
    DRY_RUN=yes
    shift
    ;;
  --*)
    stop "unknown argument $1 (usage: check-913-retry-image.sh <project> [--dry-run], or --help)"
    ;;
  *)
    if [ -z "$PROJECT" ]; then
      PROJECT=$1
    else
      stop "one argument only, a project: $PROJECT was already given"
    fi
    shift
    ;;
  esac
done

GCLOUD_BIN=${CHECK913_GCLOUD:-gcloud}
LIMIT=${CHECK913_LIMIT:-100}
CONFIG_HOME=${XDG_CONFIG_HOME:-${HOME:-}/.config}
CONFIG_FILE=${CHECK913_CONFIG:-$CONFIG_HOME/fugaro/projects/${PROJECT:-<project>}.yaml}

dry_run_plan() {
  cat <<PLAN
Check 913 plan (nothing is called: no gcloud, no read)

  project:      ${PROJECT:-<not given>}
  gcloud:       $GCLOUD_BIN
  project file: $CONFIG_FILE
  window:       $LIMIT executions read per job

1. read the project's local config: the GCP project and the region its jobs
   live in.
2. gcloud run jobs list --project <gcp> --region <region> --format=json
   — every job of the installation, and the image its template is deployed with.
3. gcloud run jobs executions list --job <job> ... --limit=$LIMIT, per job
   — every execution of it; the RUNNING ones are those whose status carries no
   completionTime.
4. gcloud artifacts docker images describe <ref> --format=json, once per distinct
   image reference that is a tag — the digest it resolves to now. A reference an
   execution records as a digest is used as it is.
5. judge each running execution: its own image digest against its job's. Equal
   with a task retried (\`status.retriedCount\`) is FAIL; equal with no retry is
   UNVERIFIED; different is PASS.

Nothing is written, no token is read, and every call runs with stdin closed.
PLAN
}

if [ "$DRY_RUN" = yes ]; then
  guard_xtrace
  guard_agent
  dry_run_plan
  exit 0
fi

[ -n "$PROJECT" ] ||
  stop "no <project> was given: name the installation to read, e.g. check-913-retry-image.sh fugaro"

guard_xtrace
guard_agent
need bash
need mktemp
need python3
need "$GCLOUD_BIN"

[ -f "$CONFIG_FILE" ] ||
  stop "$CONFIG_FILE is not there: this check reads the project's GCP project and region through the local config fugaro setup writes, and no local config names a project $PROJECT"

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk913.XXXXXXXX") || stop "cannot make a scratch directory"
chmod 700 "$SCRATCH"
FACTS="$SCRATCH/facts"
: >"$FACTS"
MARKER="$SCRATCH/.chk913-mine"
: >"$MARKER"
CALLS="$SCRATCH/calls"
: >"$CALLS"

cleanup() {
  [ -n "$SCRATCH" ] || return 0
  [ -f "$MARKER" ] || { say "refusing to remove $SCRATCH: it carries no marker from this check"; return 0; }
  rm -rf "$SCRATCH" 2>/dev/null || true
  [ -e "$SCRATCH" ] || SCRATCH_REMOVED=yes
}

# carries answers whether any row of the facts is in this state, and it does it
# with the shell's own matching rather than `printf ... | grep -q`: under `set -o
# pipefail` the writer's SIGPIPE is the pipeline's status, so a reader that stops
# at the match can make a row that is there read as absent — and this is the one
# answer of this check that must never be wrong. `aborted` is not a state of a
# row but the row that says the run could not be measured at all, and it is the
# line's own start that says so.
carries() { # carries <facts> <FAIL|UNVERIFIED|aborted>
  case "$2" in
  aborted) case "$1" in aborted\|*|*$'\n'aborted\|*) return 0 ;; esac ;;
  *) case "$1" in *"|$2|"*) return 0 ;; esac ;;
  esac
  return 1
}

verdict() { # verdict <facts>
  if carries "$1" aborted; then
    printf 'INCOMPLETE'
  elif carries "$1" FAIL; then
    printf 'FAIL'
  elif carries "$1" UNVERIFIED; then
    printf 'INCOMPLETE'
  else
    printf 'PASS'
  fi
}

print_block() { # print_block <facts> <status>
  local facts=$1 status=$2
  printf '\nCHECK 913 RESULT\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-10s %-11s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 913 RESULT: %s (exit %s)\n' "$(verdict "$facts")" "$status"
}

finish() {
  local status=$?
  trap - EXIT
  local facts
  facts=$(cat "$FACTS" 2>/dev/null || true)
  cleanup
  if [ -n "$ABORT" ] && ! carries "$facts" aborted; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT"
  fi
  if [ "$REACHED_END" = no ] && ! carries "$facts" aborted; then
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): the rows that were not reached were NOT measured, which is this script ending on an error rather than an answer about the platform"
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|the scratch directory is gone, and with it every document this read of the cloud was made from"
  else
    facts="${facts}"$'\n'"restore|FAIL|the scratch directory is STILL THERE, at ${SCRATCH} — remove it yourself"
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

# --------------------------------------------------------------- the reader

# py runs the one python3 program this check owns. It reads the project's local
# config, the two gcloud documents and the registry's answer, and judges the
# digests.
py() { # py <mode> [args...]
  python3 - "$@" <<'PY'
import json, re, sys

DIGEST = re.compile(r"@(sha256:[0-9a-f]{64})\s*$")


def die(code, msg):
    sys.stderr.write("check-913: %s\n" % msg)
    sys.exit(code)


def read(path):
    try:
        with open(path, "r", errors="replace") as fh:
            return fh.read()
    except Exception:
        return ""


def load(path):
    try:
        return json.loads(read(path))
    except Exception:
        return None


def flat(text):
    """Whitespace flattened and `|` turned away from the row separator the block
    is built from: every judgement and every detail is made on this."""
    return re.sub(r"\s+", " ", (text or "").replace("|", "/")).strip()


def items(doc):
    """The list a gcloud listing answers: a bare list, or a document carrying
    one under `items`."""
    if isinstance(doc, list):
        return doc
    if isinstance(doc, dict) and isinstance(doc.get("items"), list):
        return doc["items"]
    return []


def spec_of(doc):
    """The container spec of a job (`spec.template.spec.template.spec`) or of one
    execution (`spec.template.spec`), as Check 196 reads it. The known paths are
    tried in order and the document is finally searched for the first containers
    list, which is the one fact both shapes carry."""
    for path in (("spec", "template", "spec", "template", "spec"),
                 ("spec", "template", "spec")):
        node = doc
        for key in path:
            node = node.get(key) if isinstance(node, dict) else None
            if node is None:
                break
        if isinstance(node, dict) and isinstance(node.get("containers"), list):
            return node
    return None


def image_of(doc):
    spec = spec_of(doc)
    cs = (spec.get("containers") or []) if isinstance(spec, dict) else []
    if cs and isinstance(cs[0], dict):
        return str(cs[0].get("image") or "")
    return ""


def short_name(name):
    return str(name or "").split("/")[-1]


# --------------------------------------------------------------- the modes

def mode_config(args):
    """The project's own GCP project and region, off its local config. It is read
    as text and not decoded: the three fields wanted are top-level scalars, and a
    reader that needed a YAML library would refuse a config a real installation
    has written with a comment in it."""
    if not args:
        die(2, "config needs the project's local config")
    text = read(args[0])
    if not text:
        die(3, "%s is empty or unreadable" % args[0])
    want = {}
    for line in text.splitlines():
        if line[:1] in (" ", "\t") or line.lstrip().startswith("#"):
            continue
        for key in ("name", "gcp_project", "region"):
            m = re.match(r"^%s:\s*(.+?)\s*$" % key, line)
            if m:
                want[key] = m.group(1).strip().strip("'\"")
    for key in ("gcp_project", "region"):
        if not want.get(key):
            die(3, "the local config %s names no %s, and this check reads the jobs of one GCP project in one region" % (args[0], key))
    print("gcp=%s" % want["gcp_project"])
    print("region=%s" % want["region"])
    print("name=%s" % want.get("name", ""))
    return 0


def mode_jobs(args):
    """One line per job of the region: its name and the image its template is
    deployed with."""
    if not args:
        die(2, "jobs needs the listing's file")
    doc = load(args[0])
    if doc is None:
        die(3, "%s is not a JSON document" % args[0])
    for j in items(doc):
        if not isinstance(j, dict):
            continue
        name = short_name((j.get("metadata") or {}).get("name") or j.get("name"))
        if not name:
            continue
        print("%s|%s" % (name, image_of(j)))
    return 0


def mode_running(args):
    """One line per RUNNING execution — one Cloud Run has not completed, its
    status carrying no completionTime — of the job named: the job, the execution,
    the image the execution's own spec records, its retriedCount and how many
    tasks of it are running. `window` says whether the listing came back with
    exactly the window asked for, so a running execution may be outside it."""
    if len(args) < 2:
        die(2, "running needs the listing's file and the job's name")
    path, job = args[0], args[1]
    doc = load(path)
    if doc is None:
        die(3, "%s is not a JSON document" % path)
    rows = items(doc)
    print("window=%d" % len(rows))
    for e in rows:
        if not isinstance(e, dict):
            continue
        st = e.get("status") or {}
        if not isinstance(st, dict) or str(st.get("completionTime") or ""):
            continue
        name = short_name((e.get("metadata") or {}).get("name") or e.get("name"))
        if not name:
            continue
        retried = st.get("retriedCount")
        running = st.get("runningCount")
        print("exec=%s|%s|%s|%s|%s" % (
            job, name, image_of(e),
            "-" if retried is None else retried,
            "-" if running is None else running))
    return 0


def mode_summary(args):
    """The digest `gcloud artifacts docker images describe` answers, or nothing
    at all: a document carrying no sha256 is not a digest."""
    if not args:
        die(2, "summary needs the registry's file")
    doc = load(args[0])
    if not isinstance(doc, dict):
        return 0
    summary = doc.get("image_summary") or {}
    digest = str(summary.get("digest") or "")
    if re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
        print(digest)
    return 0


def mode_judge(args):
    """The rows: every running execution's own image digest against its job's,
    with the retry the difference turns on."""
    if len(args) < 3:
        die(2, "judge needs the jobs, the executions and the digests")
    jobs, own, want = {}, {}, {}
    for line in read(args[0]).splitlines():
        name, _, ref = line.partition("|")
        if name:
            jobs[name] = ref
    for line in read(args[1]).splitlines():
        f = line.split("|")
        if len(f) >= 5:
            own[f[1]] = {"job": f[0], "image": f[2], "retried": f[3], "running": f[4]}
    for line in read(args[2]).splitlines():
        ref, _, digest = line.partition("|")
        if ref:
            want[ref] = digest

    def digest_of(ref, what):
        if not ref:
            return "", "the %s records no container image" % what
        m = DIGEST.search(ref)
        if m:
            return m.group(1), ""
        got = want.get(ref, "")
        if got:
            return got, ""
        return "", "the digest %s resolves to was not read (gcloud artifacts docker images describe)" % ref

    def short(d):
        return "<none>" if not d else d.split(":")[-1][:12]

    for name in sorted(own):
        e = own[name]
        job = e["job"]
        mine, why = digest_of(e["image"], "execution %s of %s" % (name, job))
        theirs, why2 = digest_of(jobs.get(job, ""), "job " + job)
        where = "%s/%s" % (job, name)
        retry = e["retried"]
        if not mine or not theirs:
            print("exec|UNVERIFIED|%s: %s, so the image it runs cannot be held to its job's. This row measures nothing about the retry U1 asks about" % (where, why or why2))
            continue
        if mine != theirs:
            print("exec|PASS|%s keeps its own image: the execution records %s while the job is deployed with %s (retriedCount %s), so a retry of it boots the image it was created with and never the one the update rebuilt — U1 settled where it matters" % (where, short(mine), short(theirs), retry))
            continue
        if retry not in ("-", "0"):
            print("exec|FAIL|%s runs its job's current image (%s) and its status says %s task(s) RETRIED: the retry took the image the update rebuilt, so a release-A runner's bootstrap refusal (D3304) can meet this run mid retry — U1 settled the other way, and this execution is the one to report" % (where, short(theirs), retry))
            continue
        print("exec|UNVERIFIED|%s runs its job's current image (%s) and has retried NOTHING (%s): nothing here says whether the job's image moved under this execution at all — it may have started after the update, or the update may have rebuilt nothing. Run the check right after `fugaro update`, with a run in flight, and with an execution the update did not start" % (where, short(theirs), retry))
    return 0


def main():
    if len(sys.argv) < 2:
        die(2, "no mode given")
    mode, args = sys.argv[1], sys.argv[2:]
    if mode == "config":
        return mode_config(args)
    if mode == "jobs":
        return mode_jobs(args)
    if mode == "running":
        return mode_running(args)
    if mode == "summary":
        return mode_summary(args)
    if mode == "judge":
        return mode_judge(args)
    die(2, "unknown mode %s" % mode)


sys.exit(main())
PY
}

# read_call is the ONLY way this check reaches gcloud: it records the call and
# runs it with stdin closed, so a `gcloud` that would ask a question on the
# owner's terminal is refused by its own closed stdin instead of waiting for an
# answer nobody is there to give — and the `writes` row is read off this log.
read_call() { # read_call <argv...> > out
  printf '%s %s\n' "$GCLOUD_BIN" "$*" >>"$CALLS"
  "$GCLOUD_BIN" "$@" </dev/null
}

# ------------------------------------------------------------------- the run

imgs=$(py config "$CONFIG_FILE" 2>"$SCRATCH/config.err") || {
  flat_err=$(tr '\n' ' ' <"$SCRATCH/config.err" 2>/dev/null | tr -s ' ')
  stop "the project's local config could not be read: ${flat_err:-$CONFIG_FILE}"
}
GCP=$(printf '%s\n' "$imgs" | sed -n 's/^gcp=//p')
REGION=$(printf '%s\n' "$imgs" | sed -n 's/^region=//p')
say "project $PROJECT is on GCP project $GCP, region $REGION (its local config)"

rc=0
read_call run jobs list --project="$GCP" --region="$REGION" --format=json >"$SCRATCH/jobs.json" 2>"$SCRATCH/jobs.err" || rc=$?
[ "$rc" = 0 ] ||
  stop "gcloud run jobs list on $GCP/$REGION exited $rc: $(tr '\n' ' ' <"$SCRATCH/jobs.err" 2>/dev/null | tr -s ' ' | head -c 300). This check needs an account that may read Cloud Run jobs there (roles/run.viewer or better)"
JOBS=$(py jobs "$SCRATCH/jobs.json" 2>"$SCRATCH/jobs-parse.err") || stop "the job listing of $GCP could not be read: $(tr '\n' ' ' <"$SCRATCH/jobs-parse.err" 2>/dev/null | tr -s ' ' | head -c 300)"

: >"$SCRATCH/execs"
: >"$SCRATCH/running"
: >"$SCRATCH/job-rows"
JOBS_READ=0
JOBS_ANOMALY=""
while IFS='|' read -r job image; do
  [ -n "$job" ] || continue
  JOBS_READ=$((JOBS_READ + 1))
  rc=0
  read_call run jobs executions list --job="$job" --project="$GCP" --region="$REGION" \
    --limit="$LIMIT" --format=json >"$SCRATCH/execs-$job.json" 2>"$SCRATCH/execs-$job.err" || rc=$?
  if [ "$rc" != 0 ]; then
    say "gcloud run jobs executions list --job $job exited $rc"
    printf 'job|UNVERIFIED|%s\n' "the executions of the job $job could NOT be read: gcloud exited $rc ($(tr '\n' ' ' <"$SCRATCH/execs-$job.err" 2>/dev/null | tr -s ' ' | head -c 200)). A job whose executions were not read is not a job with none running, and a running execution of it could be the one a retry took the rebuilt image into" >>"$SCRATCH/job-rows"
    JOBS_ANOMALY="$JOBS_ANOMALY $job(unread)"
    continue
  fi
  rows=$(py running "$SCRATCH/execs-$job.json" "$job" 2>"$SCRATCH/running-$job.err") || {
    printf 'job|UNVERIFIED|%s\n' "the executions of the job $job could not be read as a document: $(tr '\n' ' ' <"$SCRATCH/running-$job.err" 2>/dev/null | tr -s ' ' | head -c 200)" >>"$SCRATCH/job-rows"
    JOBS_ANOMALY="$JOBS_ANOMALY $job(unreadable)"
    continue
  }
  window=$(printf '%s\n' "$rows" | sed -n 's/^window=//p' | head -n1)
  if [ "$window" = "$LIMIT" ]; then
    printf 'job|UNVERIFIED|%s\n' "the job $job answered exactly the window asked for ($LIMIT executions), so a RUNNING execution of it may sit outside the window this check read: raise CHECK913_LIMIT and run it again" >>"$SCRATCH/job-rows"
    JOBS_ANOMALY="$JOBS_ANOMALY $job(window-full)"
  fi
  { printf '%s\n' "$rows" | grep '^exec=' | sed 's/^exec=//' >>"$SCRATCH/running"; } || true
done <<EOF
$JOBS
EOF

RUNNING=$(grep -c . "$SCRATCH/running" 2>/dev/null || true)
RUNNING=${RUNNING:-0}
emit_job_rows() {
  while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    fact "$key" "$state" "$detail"
  done <"$SCRATCH/job-rows"
}

if [ "$RUNNING" = 0 ]; then
  fact premise UNVERIFIED "the installation $PROJECT (GCP project $GCP, region $REGION) holds $JOBS_READ job(s), and NO running execution of any of them was in flight: the state this check is about is an execution launched BEFORE \`fugaro update\` and still running after it. Run it during the update, with a run in flight${JOBS_ANOMALY:+, and mind the jobs it could not read:$JOBS_ANOMALY}"
  emit_job_rows
  REACHED_END=yes
  exit 0
fi

# Every distinct image reference that is a tag resolves once: the executions' own
# references first, then the jobs' templates.
: >"$SCRATCH/refs"
{ cut -d'|' -f3 "$SCRATCH/running" 2>/dev/null || true; printf '%s\n' "$JOBS" | cut -d'|' -f2; } |
  { grep -v '@sha256:' || true; } | { grep -v '^$' || true; } | sort -u >"$SCRATCH/refs"
: >"$SCRATCH/digests"
while IFS= read -r ref; do
  [ -n "$ref" ] || continue
  tag=$ref
  case "$tag" in
  *:*) : ;;
  *) tag="$ref:latest" ;;
  esac
  rc=0
  read_call artifacts docker images describe "$tag" --project="$GCP" --format=json \
    >"$SCRATCH/reg.json" 2>"$SCRATCH/reg.err" || rc=$?
  digest=""
  if [ "$rc" = 0 ]; then
    digest=$(py summary "$SCRATCH/reg.json" 2>/dev/null || true)
  else
    say "gcloud artifacts docker images describe $tag exited $rc"
  fi
  # The row is keyed by the reference the documents themselves carry, which is
  # what the judge looks up, and never by the tag this read appended.
  printf '%s|%s\n' "$ref" "$digest" >>"$SCRATCH/digests"
done <"$SCRATCH/refs"

fact premise PASS "the installation $PROJECT is on GCP project $GCP, region $REGION, and its local config named both; $JOBS_READ job(s) were listed there, and $RUNNING execution(s) of them are RUNNING (Cloud Run has not completed them)${JOBS_ANOMALY:+, though these jobs could not be read fully:$JOBS_ANOMALY}"

emit_job_rows
printf '%s\n' "$JOBS" >"$SCRATCH/jobs-rows"
say "judging $RUNNING running execution(s) against the image each job is deployed with"
py judge "$SCRATCH/jobs-rows" "$SCRATCH/running" "$SCRATCH/digests" 2>"$SCRATCH/judge.err" >"$SCRATCH/rows" || {
  stop "the digests could not be judged: $(tr '\n' ' ' <"$SCRATCH/judge.err" 2>/dev/null | tr -s ' ' | head -c 300)"
}
while IFS='|' read -r key state detail; do
  [ -n "$key" ] || continue
  fact "$key" "$state" "$detail"
done <"$SCRATCH/rows"

# ------------------------------------------------------------- what it did

if [ -s "$CALLS" ] && ! grep -qEv "^($GCLOUD_BIN|gcloud) (run jobs list|run jobs describe|run jobs executions list|artifacts docker images describe) " "$CALLS"; then
  fact writes PASS "every call this check made was a READ, and these are all of them: $(tr '\n' ';' <"$CALLS" | sed 's/;$//' | head -c 300). No \`gcloud auth\` was called at all, so no token was read and none was ever on an argv, and every call ran with stdin closed"
else
  fact writes FAIL "this check made a call that is not one of the reads it has any business making: $(tr '\n' ';' <"$CALLS" | head -c 300). It reads the jobs, their executions and the digests of their images, and nothing else"
fi

REACHED_END=yes
exit 0
