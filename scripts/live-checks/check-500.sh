#!/usr/bin/env bash
# Check 500 — the image setup skill, end to end on the sandbox: the catalog it
# proposed, built through Cloud Build, a run on a baked image, and the warning a
# repository with a stale `image:` block gets.
#
# Plan task IS13 writes this check; the steps are
# docs/image-setup-skill/implementation-plan.md's "Checks (reserved 500–509)".
# What it settles is read from the outside and nowhere else: D1900 (a
# repository's own `image:` is ignored once the project's catalog decides the
# job's image, and the ignored key is said so) and D1903 (the cap of four is the
# skill's, which nothing in the CLI enforces).
#
# What it measures:
#
#   * the project's catalog as it stands, from `fugaro image ls --json`: the
#     entries, their tags, which one is the default and the digest each entry's
#     last build recorded. This is the read-back of what the `fugaro:images`
#     skill proposed — the skill itself is yours to run in your own session, and
#     this check never proposes, writes or deletes an entry of its own;
#   * the cap the skill keeps and the CLI does not (D1903): more than four
#     entries is a finding, because four is where the skill stopped grouping;
#   * `fugaro image survey --json`: the second half of the skill's own loop, and
#     the three values it compares a rerun against (`setup.catalog`,
#     `setup.image_map`, `setup.bake_repos`);
#   * one **Cloud Build** per entry, through `fugaro image build <entry>`, which
#     is the only build that counts: a local Docker build proves nothing about
#     the entry's runtime contract or the job account it is built as. **Each
#     entry is built from its own checkout** — the CLI refuses a build from
#     another repository by name — so an entry whose Dockerfile belongs to
#     another repository of the project is given its checkout through
#     `CHECK500_CHECKOUT_<entry>`; every other entry is built from `--sandbox`;
#   * the digest each entry's build leaves, read back from `image ls --json`: a
#     build that ran and recorded no digest is a finding, and the digest is what
#     the daily check and a rebuild compare;
#   * one run launched from the sandbox checkout on an image **with a baked
#     checkout** (IS8b's stage, opened by IS8's reader): the record's
#     `image.baked_commit` is the baked checkout's HEAD when the run started, so
#     a non-empty one is the outside evidence that the image carried the
#     repository — and it is read only when this project bakes at all
#     (`setup.bake_repos`, and the sandbox on a mapped entry), because a project
#     that bakes nothing has nothing to show here;
#   * the warning a repository whose own `image:` block is ignored gets
#     (D1900): `fugaro update --repo <the stale checkout> --plan-only` prints
#     one `warning:` line per ignored key, and the line names the entry that
#     decides the job instead. **`--plan-only` creates and changes nothing.**
#
# What it creates: nothing of its own choosing. The builds are real Cloud Build
# runs of entries the catalog already lists (billable), the run is a real
# sandbox run, and it opens a pull request the Restore names. This check writes
# no catalog entry, no mapping and no switch — the skill's writes are yours.
#
# Usage:      bash scripts/live-checks/check-500.sh --sandbox <a sandbox repository checkout>
#             bash scripts/live-checks/check-500.sh --sandbox <path> --stale-repo <another checkout>
#             bash scripts/live-checks/check-500.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check-500.sh --help
# Needs:      bash, mktemp, date, python3 (the `--json` documents are read with
#             it), gcloud (authenticated as YOU, and able to read the runs
#             bucket) and fugaro (the installed release: the whole point is the
#             build you have).
# Where:      your own machine and the **sandbox project** (`fugaro-dev`) and
#             the **sandbox repository** only. A project or region naming belong
#             is refused by name, and so is a coding agent's session: every
#             command this check runs is one you could type yourself.
# Creates:    nothing in the cloud that the skill's own proposal did not already
#             put there, apart from one sandbox run and its pull request. On
#             disk: one scratch directory, removed at the end, holding the
#             commands' output.
# Costs:      one Cloud Build per catalog entry (billable, and the build itself
#             leases its cost from the project's budget where the installation
#             has one), plus one sandbox run and what its task costs.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check-500_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. The run's pull request and its branch are
#             NOT closed by this script: the Restore row names them.
# Output:     progress on stderr, ONE block on stdout headed CHECK 500 RESULT,
#             then CHECK 500 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold, or a guard stopped the run — among
#             them a project whose catalog holds more than four entries. 2: the
#             check could not measure — a tool is missing, the checkout is not
#             one, the catalog could not be read, or a command it drives did not
#             answer. A key the run never reached reads `FAIL — not reached`.
#
# A NOTE ON WHAT IS NEVER PRINTED: no credential value, no Dockerfile body and
# no file the survey read reaches this script's stdout, stderr, the block or the
# scratch directory. The rows carry names, tags, digests, counts and exit
# statuses.
#
# Knobs (all CHECK500_*): DRY_RUN, KEEP_SCRATCH, SANDBOX (the checkout, when you
# would rather set it in the environment), STALE_REPO (a checkout whose own
# `image:` block is stale; the sandbox by default), TASK and RECIPE (what the run
# is launched with), CHECKOUT_<entry> (the checkout one entry is built from),
# BUILD_TIMEOUT_SECONDS (1800), RUN_TIMEOUT_SECONDS (3600),
# UPDATE_TIMEOUT_SECONDS (900), POLL_SECONDS (15: how often the run is looked
# for), SKIP_BUILDS (1: read the catalog and launch the run, and spend no Cloud
# Build).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check drives a
# real `fugaro` at a real terminal, and a trace of that is a second copy of what
# its own output holds.
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
# finding about the code. The row reads FAIL in the block — it is not a pass —
# and the check ends as one that could not measure (exit 2).
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  say "an internal error ended this check at line $line"
  exit 2
}

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 500 needs bash"
  local t
  for t in mktemp date python3 gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 500 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk500-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
PROJECT=""
DEFAULT_ENTRY=""
ENTRIES=""
ENTRY_COUNT=""
BAKE_REPOS=""
BUILD_STATUS=0
BUILD_TIMED_OUT=no
RUN_ID=""
RUN_SLUG=""
RUN_STATUS=0
RUN_TIMED_OUT=no
SETTLED=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk500.XXXXXXXX") || die "Check 500 cannot make a scratch directory"
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
    say "CHECK500_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The `--json` documents this check reads, in one program: a live check on the
# owner's machine has python3 and no jq, and a row that read a document with
# grep would report a digest the build did not write the day the document is
# re-wrapped. Every reader prints `key=value` lines or `\t`-separated rows, so a
# shell can hold them without a second parser.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import json
import sys


def load(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def yes(value):
    return "true" if value else "false"


what = sys.argv[1]
doc = load(sys.argv[2])

if what == "catalog":
    entries = doc.get("entries") or []
    print("count=%d" % len(entries))
    for entry in entries:
        print("entry\t%s\t%s\t%s\t%s\t%s" % (
            entry.get("name") or "",
            yes(entry.get("default")),
            ";".join(entry.get("tags") or []),
            entry.get("source") or "",
            entry.get("digest") or "",
        ))
elif what == "survey":
    setup = doc.get("setup") or {}
    print("bake_repos=%s" % yes(setup.get("bake_repos")))
    print("catalog=%d" % len(setup.get("catalog") or []))
    print("image_map=%d" % len(setup.get("image_map") or {}))
    for repo in doc.get("repos") or []:
        print("repo\t%s\t%d\t%s" % (
            repo.get("repo") or "",
            len(repo.get("signals") or []),
            repo.get("unread") or "",
        ))
elif what == "launch":
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("repo=%s" % (doc.get("repo") or ""))
    print("status=%s" % (doc.get("status") or ""))
elif what == "rows":
    rows = doc.get("runs") or []
    print("count=%d" % len(rows))
    for row in rows:
        print("row\t%s\t%s\t%s\t%s" % (
            row.get("run_id") or "",
            row.get("status") or "",
            yes(row.get("terminal")),
            row.get("stage") or "",
        ))
elif what == "record":
    image = doc.get("image") or {}
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("status=%s" % (doc.get("status") or ""))
    print("stage=%s" % (doc.get("stage") or ""))
    print("baked_commit=%s" % (image.get("baked_commit") or ""))
    print("built_at=%s" % (image.get("built_at") or ""))
    print("pushed=%s" % yes(doc.get("pushed_head")))
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

# read_json QUERY FILE DEST: answer one query into a file, and fail the step when
# the document is not the document it should be.
read_json() { # read_json QUERY FILE DEST
  python3 "$SCRATCH/read.py" "$1" "$2" >"$3" 2>"$3.err"
}

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# name_of_entry NAME — the value of a `entry\t<name>\t...` row, for the row a key
# names.
entry_digest() { # entry_digest FILE NAME
  awk -F'\t' -v name="$2" '$1 == "entry" && $2 == name { print $6; exit }' "$1" 2>/dev/null || true
}

# in_dir DIR CMD...: run one command from the directory it has to be run in. A
# catalog entry's build is the build of the repository its Dockerfile belongs to
# — the CLI refuses it from anywhere else by name — and every other command here
# names the project through the checkout it is run in.
in_dir() { # in_dir DIR CMD...
  local dir=$1
  shift
  ( cd "$dir" && exec "$@" )
}

run_bounded() { # run_bounded SECONDS KEY CMD...
  local secs=$1 key=$2
  shift 2
  BOUNDED_TIMED_OUT=no
  # The ERR trap is dropped in the child: this check runs commands whose failing
  # exit status is one of its own readings (a build that fails, a record that is
  # not there), and a trap inherited here would turn each of them into "an
  # internal error" and a status of 2 before the step could report it.
  ( trap - ERR; "$@" ) >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
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

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check-500.sh --sandbox <a sandbox repository checkout> — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy the builds' and the run's output to the terminal"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy a build log or the run's output"
}

# guard_agent_session: the check is the owner's, and it drives commands whose
# writes a coding agent's session is refused. Refusing here rather than letting
# each command refuse one at a time is what makes the block say why: a session
# that ran this far would report the refusals as findings about the release.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 500 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

guard_belong() {
  case "${ACCOUNT:-}${PROJECT:-}${REGION}" in
  *belong*) stop "the account, project or region this check would use names belong: this check is never run there" ;;
  esac
  fact belong PASS "the project this check reads is ${PROJECT:-the one the sandbox checkout belongs to} and the account it runs as is yours; nothing here names belong"
}

version_row() {
  local v
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} (the build this check drives: the catalog it reads and the images it builds are the ones you have installed)"
}

# ---------------------------------------------------------------- the steps
# step_sandbox: the premise. The project comes from the checkout itself
# (`fugaro doctor --json`), so the check reads the project you actually work in
# rather than one it guessed, and the run and the builds are that project's.
step_sandbox() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox project)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the run this check launches is launched from one"
  fi
  command -v python3 >/dev/null 2>&1 || die "Check 500 needs python3 to read the --json documents"
  say "reading the installation: fugaro doctor --json, in ${SANDBOX}"
  run_bounded "$DOCTOR_TIMEOUT" doctor in_dir "$SANDBOX" "$FUGARO_BIN" doctor --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured sandbox "fugaro doctor --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): this check cannot read the project this checkout belongs to, and every row below is about that project. Its output is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
    return 0
  fi
  PROJECT=$(python3 -c 'import json,sys; print(((json.load(open(sys.argv[1])) or {}).get("project") or {}).get("gcp_project") or "")' "$SCRATCH/doctor.out" 2>/dev/null || true)
  if [ -z "$PROJECT" ]; then
    unmeasured sandbox "fugaro doctor --json answered without a project block: this check cannot say which project the sandbox checkout belongs to, and the rows below are about the project, the catalog and the runs bucket. What it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  case "$PROJECT" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${PROJECT}: this check is never run there" ;;
  esac
  fact sandbox PASS "${SANDBOX} is a Fugaro repository's checkout, and it belongs to ${PROJECT}: the check reads this project's catalog and launches from this checkout"
}

# step_catalog: what the project has today, read back rather than assumed. The
# skill's proposal lives in this machine's configuration and in the published
# node; `image ls --json` is the one read that shows both ends of it as a job
# sees them.
step_catalog() {
  if [ -z "$PROJECT" ]; then
    unmeasured catalog "there is no project to read a catalog from: the row above says why"
    return 0
  fi
  say "reading the catalog: fugaro image ls --json"
  run_bounded "$CLI_TIMEOUT" catalog in_dir "$SANDBOX" "$FUGARO_BIN" image ls --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured catalog "fugaro image ls --json did not answer (exit ${BOUNDED_STATUS}): this check cannot read the catalog the skill proposed. Its output is in $SCRATCH/catalog.out and $SCRATCH/catalog.err"
    return 0
  fi
  if ! read_json catalog "$SCRATCH/catalog.out" "$SCRATCH/catalog.txt"; then
    unmeasured catalog "fugaro image ls --json printed a document this check could not read: what it printed is in $SCRATCH/catalog.out"
    return 0
  fi
  ENTRY_COUNT=$(field "$SCRATCH/catalog.txt" count)
  ENTRIES=$(awk -F'\t' '$1 == "entry" { print $2 }' "$SCRATCH/catalog.txt" | paste -sd, - 2>/dev/null || true)
  if [ "${ENTRY_COUNT:-0}" = 0 ]; then
    fact catalog FAIL "the project ${PROJECT} has no catalog entry: the skill's whole proposal is the catalog, and a project with none gives every job the image its own configuration names — which is the state D1900 removes. If the skill has not been run yet, run it in your own session first (plugin/skills/images/SKILL.md) and then this check"
    return 0
  fi
  local defaults names
  defaults=$(awk -F'\t' '$1 == "entry" && $3 == "true" { print $2 }' "$SCRATCH/catalog.txt" | paste -sd, - 2>/dev/null || true)
  names=""
  while IFS=$'\t' read -r kind name isdefault tags source digest; do
    [ "$kind" = entry ] || continue
    names="${names:+$names, }${name}(tags=${tags:-none},digest=${digest:-none})"
  done <"$SCRATCH/catalog.txt"
  if [ -z "$defaults" ]; then
    DEFAULT_ENTRY=""
    fact catalog FAIL "the project ${PROJECT} has ${ENTRY_COUNT} catalog entries and none marked default: without a default the resolver keeps a repository's own image and D1900's warning has nothing to print, so the stale-`image:` row below cannot be measured either. The entries are ${names}"
    return 0
  fi
  DEFAULT_ENTRY=${defaults%%,*}
  fact catalog PASS "the project ${PROJECT} has ${ENTRY_COUNT} catalog entries, the default is ${defaults}, and they are: ${names} — read back with fugaro image ls --json, which is what a job's resolver reads"
}

# step_cap: D1903, read as a number rather than trusted to the skill that keeps
# it. The CLI enforces nothing here — fugaro image add writes what it is told —
# so a project with five entries has stopped grouping and started enumerating,
# which is the whole reason the number is written down.
step_cap() {
  case "$ENTRY_COUNT" in
  "" | 0)
    unmeasured cap "there is no catalog to count: the row above says why"
    return 0
    ;;
  esac
  if [ "$ENTRY_COUNT" -gt 4 ]; then
    fact cap FAIL "the catalog holds ${ENTRY_COUNT} entries (${ENTRIES}) and the cap the fugaro:images skill keeps is four (D1903): five is where a project has stopped grouping and started enumerating. Nothing in the CLI refuses this — the skill's own text is the only place the number lives — so this is a finding about the proposal and a question for you rather than a bug in the release"
    return 0
  fi
  fact cap PASS "the catalog holds ${ENTRY_COUNT} of the four the skill allows (D1903): ${ENTRIES}"
}

# step_survey: the half of the skill's loop that reads the repositories rather
# than writing them — and the three values a rerun compares against. It is
# read-only and clones nothing (D1902), so running it here costs nothing and is
# the second of the two commands the skill leans on.
step_survey() {
  if [ -z "$PROJECT" ]; then
    unmeasured survey "there is no project to survey: the row above says why"
    return 0
  fi
  say "reading the repositories: fugaro image survey --json (read-only; nothing is cloned)"
  run_bounded "$CLI_TIMEOUT" survey in_dir "$SANDBOX" "$FUGARO_BIN" image survey --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured survey "fugaro image survey --json did not answer (exit ${BOUNDED_STATUS}): the survey is half of the skill's loop and the only place a rerun's comparison comes from. Its output is in $SCRATCH/survey.out and $SCRATCH/survey.err"
    return 0
  fi
  if ! read_json survey "$SCRATCH/survey.out" "$SCRATCH/survey.txt"; then
    unmeasured survey "fugaro image survey --json printed a document this check could not read: what it printed is in $SCRATCH/survey.out"
    return 0
  fi
  BAKE_REPOS=$(field "$SCRATCH/survey.txt" bake_repos)
  local repos mapped
  repos=$(awk -F'\t' '$1 == "repo" { print $2 }' "$SCRATCH/survey.txt" | paste -sd, - 2>/dev/null || true)
  mapped=$(field "$SCRATCH/survey.txt" image_map)
  fact survey PASS "the survey answered for ${repos:-no repository} with $(field "$SCRATCH/survey.txt" catalog) catalog entries, ${mapped:-0} mapped repositories and bake_repos=${BAKE_REPOS:-false} — the three values a rerun compares against, read without a clone"
}

# step_builds: the build that counts. One Cloud Build per entry, from the
# entry's own checkout, because the CLI refuses a build from another
# repository by name — a Dockerfile built against the wrong account builds the
# wrong thing under the wrong credential.
step_builds() {
  if [ -z "$ENTRY_COUNT" ] || [ "$ENTRY_COUNT" = 0 ]; then
    unmeasured builds "there is no catalog entry to build: the row above says why"
    return 0
  fi
  if [ -n "$SKIP_BUILDS" ]; then
    fact builds INFO "not asked for (CHECK500_SKIP_BUILDS): the catalog, the cap, the survey and the run were measured, and no Cloud Build was spent. The digests below are the ones the catalog already recorded"
    return 0
  fi
  local name checkout status=0 failed="" timedout="" built=0
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    checkout=$(checkout_for "$name")
    built=$((built + 1))
    if [ "$built" -gt "$MAX_BUILDS" ]; then
      failed="${failed:+$failed, }${name}(over CHECK500_MAX_BUILDS)"
      status=1
      continue
    fi
    say "building ${name} from ${checkout} (a real Cloud Build: billable)"
    run_bounded "$BUILD_TIMEOUT" "build-$name" in_dir "$checkout" "$FUGARO_BIN" image build "$name"
    if [ "$BOUNDED_TIMED_OUT" = yes ]; then
      timedout="${timedout:+$timedout, }$name"
      continue
    fi
    if [ "$BOUNDED_STATUS" != 0 ]; then
      failed="${failed:+$failed, }${name}(exit $BOUNDED_STATUS)"
      status=1
    fi
  done < <(awk -F'\t' '$1 == "entry" { print $2 }' "$SCRATCH/catalog.txt")
  if [ -n "$timedout" ]; then
    unmeasured builds "the build(s) of ${timedout} did not finish inside ${BUILD_TIMEOUT}s and were ended: a Cloud Build that hangs is a longer look than a check should take, and this check cannot say whether the entry builds. The output is in $SCRATCH/build-<entry>.out and .err"
    return 0
  fi
  if [ "$status" != 0 ]; then
    fact builds FAIL "the build(s) of ${failed} did not succeed: an entry that does not build through Cloud Build is one no job of the project can start on, whatever the Dockerfile looks like locally. Each build's own output is in $SCRATCH/build-<entry>.out and .err, and a build refused for the checkout names the repository it wanted — give that entry its own checkout with CHECK500_CHECKOUT_<entry>=<path>"
    return 0
  fi
  fact builds PASS "every catalog entry built through Cloud Build — ${ENTRIES} — each from its own checkout, which is the build the job's own account and the runtime contract are measured on"
}

# checkout_for ENTRY: the checkout one entry is built from. An entry's Dockerfile
# may belong to another repository of the project, and the CLI refuses it by
# name from anywhere else, so the owner names it per entry; every entry that is
# this repository's own is built from the sandbox checkout.
checkout_for() { # checkout_for NAME
  local var="CHECK500_CHECKOUT_$1"
  var=${var//[^A-Za-z0-9_]/_}
  if [ -n "${!var:-}" ]; then
    printf '%s' "${!var}"
    return 0
  fi
  printf '%s' "$SANDBOX"
}

# step_digests: the digest a build leaves, read back from the catalog. A build
# that ran and recorded none is a finding: the digest is what the daily check,
# a rebuild and a job's own record compare, and one that is missing is an entry
# whose image nothing can be said about.
step_digests() {
  if [ -z "$ENTRY_COUNT" ] || [ "$ENTRY_COUNT" = 0 ]; then
    unmeasured digests "there is no catalog entry to read a digest for: the row above says why"
    return 0
  fi
  say "reading the digests back: fugaro image ls --json"
  run_bounded "$CLI_TIMEOUT" digests in_dir "$SANDBOX" "$FUGARO_BIN" image ls --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured digests "fugaro image ls --json did not answer after the builds (exit ${BOUNDED_STATUS}), so this check cannot say which entries recorded a digest. Its output is in $SCRATCH/digests.out and $SCRATCH/digests.err"
    return 0
  fi
  if ! read_json catalog "$SCRATCH/digests.out" "$SCRATCH/digests.txt"; then
    unmeasured digests "fugaro image ls --json printed a document this check could not read after the builds: what it printed is in $SCRATCH/digests.out"
    return 0
  fi
  local name digest missing="" found=""
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    digest=$(entry_digest "$SCRATCH/digests.txt" "$name")
    if [ -z "$digest" ]; then
      missing="${missing:+$missing, }$name"
      continue
    fi
    found="${found:+$found, }${name}=${digest}"
  done < <(awk -F'\t' '$1 == "entry" { print $2 }' "$SCRATCH/catalog.txt")
  if [ -n "$missing" ]; then
    fact digests FAIL "${missing} recorded no digest: the build either did not run or ran without recording what it built, and the digest is what a job's record, the daily check and a rebuild compare. What fugaro image ls --json printed is in $SCRATCH/digests.out"
    return 0
  fi
  fact digests PASS "every entry's last build recorded a digest — ${found} — read back from fugaro image ls --json rather than assumed from the build's exit status"
}

# step_run: one sandbox run, on whatever image the catalog gives the sandbox
# repository. `--json` is what names the run this check then follows; a launch
# that prints no run ID is a finding about the launcher, not a row this check
# could not measure.
step_run() {
  if [ -z "$PROJECT" ]; then
    unmeasured run-launched "there is no project to launch a run in: the row above says why"
    return 0
  fi
  say "launching one run from ${SANDBOX} (a real sandbox run, with what its task costs)"
  run_bounded "$LAUNCH_TIMEOUT" launch in_dir "$SANDBOX" \
    "$FUGARO_BIN" run --repo "$SANDBOX" ${RECIPE:+--recipe "$RECIPE"} --json "$TASK"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured run-launched "the launch did not finish inside ${LAUNCH_TIMEOUT}s and was ended: what it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact run-launched FAIL "fugaro run exited ${BOUNDED_STATUS} from ${SANDBOX}: the skill's whole point is an image a job starts on, and a launch that refuses says the image or the catalog is not usable by a job. What it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if ! read_json launch "$SCRATCH/launch.out" "$SCRATCH/launch.txt"; then
    fact run-launched FAIL "fugaro run --json printed a document this check could not read: the launch is the step this check follows, and its own --json view is how the run is named. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  RUN_ID=$(field "$SCRATCH/launch.txt" run_id)
  RUN_SLUG=$(field "$SCRATCH/launch.txt" repo)
  if [ -z "$RUN_ID" ] || [ -z "$RUN_SLUG" ]; then
    fact run-launched FAIL "fugaro run --json named no run (run_id=$(field "$SCRATCH/launch.txt" run_id), repo=$(field "$SCRATCH/launch.txt" repo)): this check follows the run it launched, and a launch that does not name it cannot be followed. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  fact run-launched PASS "fugaro run launched ${RUN_ID} on ${RUN_SLUG} from the sandbox checkout (status $(field "$SCRATCH/launch.txt" status)): the run is the check's own, and the rows below follow this one and no other"
}

# step_terminal: the run has to end. A check that read a record while a run was
# still going would read the previous run's, and the record of a run that is
# still working is not the reading this check is after.
step_terminal() {
  if [ -z "$RUN_ID" ]; then
    unmeasured terminal "there is no run to follow: the row above says why"
    return 0
  fi
  say "waiting for ${RUN_ID} to end (up to ${RUN_TIMEOUT}s; fugaro runs ls --repo ${RUN_SLUG} --json --limit 1)"
  local waited=0 status="" stage=""
  while [ "$waited" -lt "$RUN_TIMEOUT" ]; do
    run_bounded "$CLI_TIMEOUT" rows in_dir "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$RUN_SLUG" --json --limit 1
    status=""
    if [ "$BOUNDED_STATUS" = 0 ] && read_json rows "$SCRATCH/rows.out" "$SCRATCH/rows.txt"; then
      status=$(awk -F'\t' -v id="$RUN_ID" '$1 == "row" && $2 == id { print $4; exit }' "$SCRATCH/rows.txt")
      stage=$(awk -F'\t' -v id="$RUN_ID" '$1 == "row" && $2 == id { print $5; exit }' "$SCRATCH/rows.txt")
      if [ "$status" = true ]; then break; fi
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$status" != true ]; then
    unmeasured terminal "${RUN_ID} had not ended after ${RUN_TIMEOUT}s (last row: status=$(awk -F'\t' -v id="$RUN_ID" '$1 == "row" && $2 == id { print $3; exit }' "$SCRATCH/rows.txt" 2>/dev/null), stage=${stage:-unknown}): this check reads a terminal run's own record, and reading one while the run is still working would read whatever was there before. Watch it with 'fugaro watch ${RUN_ID}' and run this check again when it has ended — the run it launched is a real one and is not cancelled by stopping here"
    return 0
  fi
  SETTLED=yes
  fact terminal PASS "${RUN_ID} ended in ${waited}s (status=$(awk -F'\t' -v id="$RUN_ID" '$1 == "row" && $2 == id { print $3; exit }' "$SCRATCH/rows.txt"), stage=${stage:-unknown}, read from fugaro runs ls --json): the run reached its end, which is the state the record below is written in"
}

# step_result_json: the record, read from the runs bucket as the owner would
# read it. The record's own run_id is what ties it to the run this check
# launched: a record read by listing alone could be another run's.
step_result_json() {
  if [ -z "$RUN_ID" ] || [ -z "$RUN_SLUG" ]; then
    unmeasured result-json "there is no run whose record could be read: the row above says why"
    return 0
  fi
  if [ "$SETTLED" != yes ]; then
    unmeasured result-json "the run ${RUN_ID} had not ended by the time the row above gave up, so this check did not read its record: a record read while a run is still working is whatever was there before it, which is the state the row above refuses to read a terminal run's own record from. Watch it with 'fugaro watch ${RUN_ID}' and run this check again when it has ended"
    return 0
  fi
  local key="runs/${RUN_SLUG}/${RUN_ID}/result.json"
  say "reading the record: gcloud storage cat gs://fugaro-runs-${PROJECT}/${key}"
  run_bounded "$CLI_TIMEOUT" record gcloud storage cat "gs://fugaro-runs-${PROJECT}/${key}" --project="${PROJECT}"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact result-json FAIL "gs://fugaro-runs-${PROJECT}/${key} could not be read (exit ${BOUNDED_STATUS}): a run that ended has a record, and the record is where the run's image is written. What gcloud printed is in $SCRATCH/record.err"
    return 0
  fi
  if ! read_json record "$SCRATCH/record.out" "$SCRATCH/record.txt"; then
    fact result-json FAIL "${key} is not a document this check could read: what gcloud printed is in $SCRATCH/record.out"
    return 0
  fi
  local recorded
  recorded=$(field "$SCRATCH/record.txt" run_id)
  if [ "$recorded" != "$RUN_ID" ]; then
    fact result-json FAIL "${key} records run_id=${recorded} and this check launched ${RUN_ID}: the record at that key is another run's, and nothing below can be read as this run's"
    return 0
  fi
  fact result-json PASS "${key} records ${RUN_ID} (status=$(field "$SCRATCH/record.txt" status), stage=$(field "$SCRATCH/record.txt" stage), pushed=$(field "$SCRATCH/record.txt" pushed)): the run this check launched reached its own record, read from the bucket as you would read it"
}

# step_baked: the reading this check exists for on the image side. The record's
# `image.baked_commit` is the baked checkout's HEAD when the run started, so a
# non-empty one says the image carried the repository's own checkout — the
# faster start the bake is for, and the one thing about the image a job's log
# cannot show. Where the project bakes nothing, or the sandbox is on an entry
# the map does not put it on, there is nothing to read and the row says which.
step_baked() {
  if [ -z "$RUN_ID" ] || [ ! -s "$SCRATCH/record.txt" ]; then
    unmeasured baked "there is no record to read the image from: the row above says why"
    return 0
  fi
  local baked
  baked=$(field "$SCRATCH/record.txt" baked_commit)
  if [ "$BAKE_REPOS" = false ]; then
    fact baked INFO "not asked for: this project does not bake (setup.bake_repos is false), so no image of it carries a baked checkout and the record's image.baked_commit is empty by design. The run above still started on a catalog image, and fugaro image build measured it"
    return 0
  fi
  if [ -z "$baked" ]; then
    fact baked FAIL "the run ${RUN_ID} started on an image with no baked checkout (image.baked_commit is empty) while this project bakes (setup.bake_repos=${BAKE_REPOS:-true}): the sandbox repository's image was built without its checkout, so its jobs clone at start — the thing IS8b's stage exists to stop. Check that the project's image_map puts the sandbox on the entry the build composed, and that the build was run after the mapping was published"
    return 0
  fi
  fact baked PASS "the run ${RUN_ID} started on an image whose baked checkout was at ${baked} (image.baked_commit): the image carried the repository, which is the reading no build log and no local Docker build can give"
}

# step_stale: D1900's warning, in the one place a person meets it. A repository
# whose own `image:` block is still written is told, once per ignored key, in
# the run that moves its job. `--plan-only` prints the plan and the warnings and
# creates nothing.
step_stale() {
  if [ -z "$PROJECT" ]; then
    unmeasured stale-warning "there is no project to plan against: the row above says why"
    return 0
  fi
  if [ -z "$DEFAULT_ENTRY" ]; then
    fact stale-warning INFO "not asked for: this project's catalog has no default entry, so no repository's own image is ignored yet and D1900's warning has nothing to print. It appears once an entry is marked default with fugaro image add --default"
    return 0
  fi
  if [ ! -f "$STALE_REPO/fugaro.yaml" ]; then
    unmeasured stale-warning "--stale-repo carries no fugaro.yaml: $STALE_REPO is not the checkout of a repository with its own image: block to read. Point --stale-repo at one, or leave it unset to use the sandbox checkout"
    return 0
  fi
  say "reading the warning: fugaro update --repo ${STALE_REPO} --plan-only (prints the plan and creates nothing)"
  run_bounded "$UPDATE_TIMEOUT" stale in_dir "$STALE_REPO" "$FUGARO_BIN" update --repo "$STALE_REPO" --plan-only
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured stale-warning "fugaro update --repo ${STALE_REPO} --plan-only did not finish inside ${UPDATE_TIMEOUT}s and was ended: it plans by reading the project, and this is a longer look than a check should take. Its output is in $SCRATCH/stale.out and $SCRATCH/stale.err"
    return 0
  fi
  local text
  text=$(cat "$SCRATCH/stale.out" "$SCRATCH/stale.err" 2>/dev/null || true)
  if ! printf '%s\n' "$text" | grep -q 'is ignored'; then
    fact stale-warning FAIL "fugaro update --repo ${STALE_REPO} --plan-only printed no 'is ignored' warning (exit ${BOUNDED_STATUS}) for a repository whose own image: block the project's default entry ${DEFAULT_ENTRY} overrides: D1900's whole point is that the key is dropped and the person is told which entry decides the job instead, and a silent drop is the one outcome nobody can act on. What it printed is in $SCRATCH/stale.out and $SCRATCH/stale.err"
    return 0
  fi
  if ! printf '%s\n' "$text" | grep -q 'D1900'; then
    fact stale-warning FAIL "the warning fugaro update printed does not name D1900: a person told their key is ignored is told where the rule is written, and the decision number is how they find it. What it printed is in $SCRATCH/stale.out and $SCRATCH/stale.err"
    return 0
  fi
  fact stale-warning PASS "$(printf '%s\n' "$text" | grep -m1 'is ignored' | head -c 240) — the repository's own image: block is ignored and the run says so, naming the entry that decides the job (D1900)"
}

# restore: what this check changed, said rather than assumed.
restore() {
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="this check wrote no catalog entry, no mapping and no switch — the skill's writes are yours. It ran one Cloud Build per catalog entry (billable), launched the sandbox run ${RUN_ID:-none} and left its pull request and its branch open: close that pull request and delete its branch yourself once you have read the run. The scratch directory held the commands' output"
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
  say "CHECK 500 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] fugaro doctor --json in ${SANDBOX} — the project this checkout belongs to"
  say "  [read] fugaro image ls --json — the catalog the fugaro:images skill proposed: the entries, their tags, the default, the digests"
  say "  [read] the cap: more than four entries is a finding (D1903 — the CLI enforces nothing here)"
  say "  [network, read-only, no clone] fugaro image survey --json — the repositories' signals and the three values a rerun compares against (D1902)"
  if [ -n "$SKIP_BUILDS" ]; then
    say "  [not asked for] the builds: CHECK500_SKIP_BUILDS is set, so no Cloud Build is spent"
  else
    say "  [network, BILLABLE] one Cloud Build per entry — fugaro image build <entry>, each from its own checkout (CHECK500_CHECKOUT_<entry>, the sandbox checkout by default)"
    say "  [read] fugaro image ls --json again — the digest each entry's build recorded"
  fi
  say "  [network, BILLABLE] fugaro run --repo ${SANDBOX}${RECIPE:+ --recipe ${RECIPE}} --json \"${TASK}\" — one sandbox run, followed with fugaro runs ls --json until it is terminal"
  say "  [read] gcloud storage cat gs://fugaro-runs-<the project>/runs/<repo>/<run>/result.json — the record, and its image.baked_commit"
  say "  [network, read, creates nothing] fugaro update --repo ${STALE_REPO} --plan-only — D1900's 'is ignored' warning for the stale image: block"
  fact stale-warning INFO "planned: a real run measures this row; --dry-run did not"
  fact builds INFO "planned: a real run measures this row; --dry-run did not"
  fact digests INFO "planned: a real run measures this row; --dry-run did not"
  for key in sandbox catalog cap survey run-launched terminal result-json baked; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no doctor, no catalog read, no build, no run and no update"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(fugaro-version xtrace agent-session belong sandbox catalog cap survey
  builds digests run-launched terminal result-json baked stale-warning restore
  scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 500 RESULT\n' >&3
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
  printf 'CHECK 500 RESULT END (exit %s)\n' "$status" >&3
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
DRY_RUN_CLI=""
SANDBOX_CLI=""
STALE_CLI=""
TASK_CLI=""
RECIPE_CLI=""
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of a repository of the sandbox project, which is what the run is launched from"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --stale-repo)
    [ $# -ge 2 ] || die "--stale-repo needs a path: a checkout whose own image: block the project's catalog now overrides"
    STALE_CLI=$2
    shift 2
    ;;
  --task)
    [ $# -ge 2 ] || die "--task needs the text the sandbox run is launched with"
    TASK_CLI=$2
    shift 2
    ;;
  --recipe)
    [ $# -ge 2 ] || die "--recipe needs a recipe name (agent, default, review, ...)"
    RECIPE_CLI=$2
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

SANDBOX=${CHECK500_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 500 needs --sandbox: the checkout of a repository of the sandbox project. It is what the run is launched from, what the catalog is read through, and the default checkout each entry is built from. Usage: bash scripts/live-checks/check-500.sh --sandbox <a sandbox repository checkout>"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
STALE_REPO=${CHECK500_STALE_REPO:-${STALE_CLI:-$SANDBOX}}
case "$STALE_REPO" in
/*) ;;
*) STALE_REPO="$PWD/$STALE_REPO" ;;
esac
TASK=${CHECK500_TASK:-${TASK_CLI:-"Reply with the single word ok and change nothing in the repository."}}
RECIPE=${CHECK500_RECIPE:-${RECIPE_CLI:-}}
SKIP_BUILDS=${CHECK500_SKIP_BUILDS:-}
DRY_RUN=${CHECK500_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK500_KEEP_SCRATCH:-}
REGION=${CHECK500_REGION:-us-east5}
BUILD_TIMEOUT=${CHECK500_BUILD_TIMEOUT_SECONDS:-1800}
RUN_TIMEOUT=${CHECK500_RUN_TIMEOUT_SECONDS:-3600}
POLL_SECONDS=${CHECK500_POLL_SECONDS:-15}
UPDATE_TIMEOUT=${CHECK500_UPDATE_TIMEOUT_SECONDS:-900}
DOCTOR_TIMEOUT=${CHECK500_DOCTOR_TIMEOUT_SECONDS:-180}
CLI_TIMEOUT=${CHECK500_CLI_TIMEOUT_SECONDS:-180}
LAUNCH_TIMEOUT=${CHECK500_LAUNCH_TIMEOUT_SECONDS:-600}
MAX_BUILDS=${CHECK500_MAX_BUILDS:-4}
# Resolved before anything runs, and by absolute path: this check drives a real
# `fugaro` that reaches the cloud, so it must be the one on PATH when the check
# started rather than one a later step could put there.
FUGARO_BIN=$(command -v fugaro || true)
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

main() {
  need_tools
  [ -n "$FUGARO_BIN" ] || die "Check 500 needs 'fugaro' on PATH: the check drives the build you have installed, and it is not there"
  scratch_up
  trap on_error ERR
  write_reader
  guard_xtrace
  guard_agent_session
  guard_belong
  version_row
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  step_sandbox
  step_catalog
  step_cap
  step_survey
  step_builds
  step_digests
  step_run
  step_terminal
  step_result_json
  step_baked
  step_stale
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
