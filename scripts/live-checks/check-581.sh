#!/usr/bin/env bash
# Check 581 — one catalog entry over two repositories: the multi-repository
# bake, and the leak scan that stands in front of the promote.
#
# Plan task B4 of docs/shared-jobs-default/implementation-plan.md writes this
# check; the number is that work's own (its README.md reserves 580-589). What it
# settles is read from the outside and nowhere else: **D2301** — one catalog
# build bakes every repository the project's image map puts on the entry being
# built, not the one it runs for alone — and **B3/R2** — the build scans the
# image it made for a leftover credential of the build's, between the smoke and
# the promote, and refuses rather than moves `latest`. Neither is answerable
# offline: the bake is the stages a build composes into a definition and the
# scan is a step of a real Cloud Build, so this check builds one entry and reads
# the build's own record of itself back.
#
# What it measures:
#
#   * the project's catalog and its image map, read back rather than assumed:
#     `fugaro image ls --json` is the entries, their digests and the default,
#     and `fugaro image survey --json` carries the map (repository to entry) the
#     bake is decided from. **The entry this check builds is the one the map puts
#     the most repositories on**, two at least: an entry with one repository is
#     the single-repository build this check is not about, and a project whose
#     map puts no entry on two repositories has nothing here to measure — which
#     is a finding, and its row names the command that maps a second one;
#   * one **Cloud Build** of that entry, through `fugaro image build <entry>`,
#     from the checkout of the repository the entry's Dockerfile belongs to (the
#     CLI refuses a build from anywhere else by name). It is the only build that
#     counts: a local Docker build proves nothing about the entry's runtime
#     contract, the account it is built as, or the steps Fugaro adds;
#   * the repositories the build says it baked, out of the build's **own note**
#     (`note: the catalog entry <entry> bakes the checkouts of <repo> at
#     /work/baked/<slug>, ..., so the image carries the source of every
#     repository the project's map puts on it`). Every repository the map puts on
#     the entry has to be named there, which is the outside evidence that the
#     definition the build read carries one bake stage per repository;
#   * the leak scan, out of the build's own steps: `gcloud builds describe`, in
#     the region the build ran in (a build is submitted to
#     projects/<project>/locations/<region>/builds, and a describe without a
#     region reads the global location and finds nothing) answers the step ids of
#     the build that ran, and the row holds the scan's
#     step (`catalog-scan`) to being present, to standing **before** `promote`,
#     and to having succeeded — with the build itself SUCCESS, since a scan that
#     refused fails the build and nothing is promoted;
#   * the digest the build recorded, read back from `fugaro image ls --json`. The
#     build writes its record after the promote, so the row reads the digest the
#     build's own output says it made (`built <image>@<digest>`) and holds the
#     entry to carrying that one: a repeated build that recorded nothing leaves
#     the digest the entry already had, and the row does not pass on it.
#
# What it creates: nothing of its own choosing. One Cloud Build of an entry the
# catalog already lists (**billable**, and the build leases its cost from the
# project's budget where the installation has one), which promotes that entry's
# image: `latest` moves to what the build made, and the check neither invents an
# entry, writes a mapping nor changes a switch. No run is launched, no branch is
# pushed and no pull request is opened.
#
# Usage:      bash scripts/live-checks/check-581.sh --sandbox <a sandbox repository checkout>
#             bash scripts/live-checks/check-581.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check-581.sh --sandbox <path> --entry <catalog entry>
#             bash scripts/live-checks/check-581.sh --sandbox <path> --checkout <the entry's own repository>
#             bash scripts/live-checks/check-581.sh --help
# Needs:      bash, mktemp, python3 (the `--json` documents are read with it),
#             awk, sort, gcloud (authenticated as YOU: the build's own steps are
#             read with `gcloud builds describe`) and fugaro (the installed
#             release: the build this check drives is the one you have).
# Where:      your own machine and the **sandbox project** (`fugaro-dev`) and the
#             **sandbox repository** only. A project or region naming belong is
#             refused by name, and so is a checkout the billable build would be
#             submitted from that belongs to another project (its own `fugaro
#             doctor --json` is read, and the check stops — no build runs — when
#             that names another project or one naming belong). A coding agent's
#             session is refused too: every command this check runs is one you
#             could type yourself.
# Creates:    one Cloud Build of an entry the catalog already lists, and the
#             promotion that build makes. On disk: one scratch directory,
#             removed at the end, holding the commands' output.
# Costs:      one Cloud Build (**billable**), and nothing else: no model call, no
#             run, no branch and no pull request.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check-581_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. The promoted image is NOT restored: it is
#             the entry's own image, moved to what `fugaro image build` made of
#             it, which is what that command does on any day.
# Output:     progress on stderr, ONE block on stdout headed CHECK 581 RESULT,
#             then CHECK 581 RESULT END (exit N) — to copy back into the
#             register's Check 581 section, and nothing else. Exit 0: every
#             criterion that was measured held. 1: a measured criterion did not
#             hold, or a guard stopped the check before it could run. 2: a row
#             could not be measured for a reason that is this check's own setup
#             rather than the release (the block says which, by name). A key the
#             run never reached reads `FAIL — not reached`, which is a row that
#             was NOT measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: no token, no key, no credential value, no
# authorization header and no build log. Every fact below is a NAME — an entry's
# name, a repository's name, a step's id, a digest, an exit status — and the
# build's own note is reduced to the repository names in it before it is
# printed. This check holds no credential at all: it drives `fugaro` and
# `gcloud` and reads what they answer.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK581_SANDBOX (or --sandbox: the checkout of a repository of the sandbox
# project — REQUIRED), CHECK581_ENTRY (or --entry: the catalog entry to build.
# Unset, the check takes the entry the project's map puts the most repositories
# on, two at least, and says which and why), CHECK581_CHECKOUT (or --checkout:
# the checkout the build is run from; the sandbox checkout by default, and the
# right value when the entry's Dockerfile belongs to another repository of the
# project — it has to be a repository of the same project, and its own `fugaro
# doctor --json` is read and the check stops rather than submit the billable
# build in another project, or in one naming belong), CHECK581_DRY_RUN,
# CHECK581_KEEP_SCRATCH, CHECK581_REGION (the region
# the build is described in — the project's own region, as `fugaro doctor`
# reports it, unless the knob names another, which is what an installation whose
# `build.region` differs needs; the guard that refuses a project or region naming
# belong reads it too),
# CHECK581_DOCTOR_TIMEOUT_SECONDS (180), CHECK581_CLI_TIMEOUT_SECONDS (180) and
# CHECK581_BUILD_TIMEOUT_SECONDS (1800).
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
# finding about the release. The row reads FAIL in the block — it is not a pass
# — and the check ends as one that could not measure (exit 2).
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
  command -v bash >/dev/null 2>&1 || die "Check 581 needs bash"
  local t
  for t in mktemp python3 awk sort gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 581 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk581-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
PROJECT=""
ENTRY=""
ENTRY_SOURCE=""
ENTRY_REPOS=""
ENTRY_DIGEST_BEFORE=""
MAP_REPOS=""
BUILD_ID=""
NOTE_REPOS=""
BUILT=no
CATALOG_OK=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk581.XXXXXXXX") || die "Check 581 cannot make a scratch directory"
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
    say "CHECK581_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The `--json` documents this check reads, in one program: a live check on the
# owner's machine has python3 and no jq, and a row that read a document with
# grep would report a step or a digest the build did not write the day the
# document is re-wrapped. Every reader prints `key=value` lines or `\t`-separated
# rows, so a shell can hold them without a second parser.
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
    # The map is two members and never one member under two meanings (R4): a
    # project whose map an update has not migrated yet carries the bare-name
    # legacy_image_map instead of repo_images, and this reads whichever the
    # document holds — that member is what the entry below is picked from.
    mapping = setup.get("repo_images") or {}
    member = "repo_images"
    if not mapping and setup.get("legacy_image_map") is not None:
        mapping = setup.get("legacy_image_map") or {}
        member = "legacy_image_map"
    # A repo_images key is provider/owner/name (projectpolicy.Canonical) while
    # the build's own note names a repository owner/name, so the provider is
    # dropped here: the rows below are compared against that note, and the
    # legacy member's key is already the bare name. One owner/name can be
    # mapped at two providers (D3373), which is one repository and not two, so
    # the pairs are a set and the order is fixed rather than the read's.
    pairs = set()
    for repo in mapping:
        name = repo.split("/", 1)[1] if member == "repo_images" and repo.count("/") == 2 else repo
        pairs.add((name, mapping[repo]))
    print("member=%s" % member)
    print("map_count=%d" % len({name for name, _ in pairs}))
    for name, entry in sorted(pairs):
        print("map\t%s\t%s" % (name, entry))
elif what == "build":
    print("status=%s" % (doc.get("status") or ""))
    print("detail=%s" % (((doc.get("failureInfo") or {}).get("detail")) or ""))
    for i, step in enumerate(doc.get("steps") or []):
        print("step\t%d\t%s\t%s" % (i, step.get("id") or "", step.get("status") or ""))
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

# doctor_field FILE KEY — one field of `fugaro doctor --json`'s project block,
# `gcp_project` and `region` being the two this check reads. "" is a document
# that carries no project block, or one read for a value the document has not
# got: a checkout whose project the check cannot name is one it must not build
# from.
doctor_field() { # doctor_field FILE KEY
  python3 -c 'import json,sys; print(((json.load(open(sys.argv[1])) or {}).get("project") or {}).get(sys.argv[2]) or "")' "$1" "$2" 2>/dev/null || true
}

# entry_field FILE NAME COLUMN — one column of the entry this check builds, out
# of the last `fugaro image ls --json` read.
entry_field() { # entry_field FILE NAME COLUMN
  awk -F'\t' -v name="$2" -v col="$3" '$1 == "entry" && $2 == name { print $col; exit }' "$1" 2>/dev/null || true
}

# step_index FILE ID — the position of a build step in the build's own step
# list, "" when the build carries no step with that id.
step_index() { # step_index FILE ID
  awk -F'\t' -v id="$2" '$1 == "step" && $3 == id { print $2; exit }' "$1" 2>/dev/null || true
}

# step_status FILE ID — the status Cloud Build recorded for one step.
step_status() { # step_status FILE ID
  awk -F'\t' -v id="$2" '$1 == "step" && $3 == id { print $4; exit }' "$1" 2>/dev/null || true
}

# build_digest FILE... — the digest the build's own output says it made, out of
# the two sentences the CLI prints when a build ends: `built <image>@<digest>
# (Cloud Build build <id>)`, and `smoked <image>@<digest> (…)` for a prebuilt
# entry. "" is a build that reported no digest of its own (its "digest unknown"
# sentence), which is what a row cannot attribute a catalog digest to.
build_digest() { # build_digest FILE...
  local text
  text=$(cat "$@" 2>/dev/null || true)
  printf '%s\n' "$text" | sed -n 's/.*[^ ]*@\([^ ]*\) (Cloud Build build .*/\1/p' | head -n 1 || true
}

# repos_on_entry FILE ENTRY — the repositories the project's map puts on one
# entry, comma-separated. Each is a repository name and not a map key: the
# reader has already dropped the provider, so one name mapped at two providers
# is one entry here, which is what the build's own note names.
repos_on_entry() { # repos_on_entry FILE ENTRY
  awk -F'\t' -v e="$2" '$1 == "map" && $3 == e { print $2 }' "$1" 2>/dev/null | paste -sd, - 2>/dev/null || true
}

# pick_entry FILE — the entry the map puts the most repositories on, two at
# least, as `<count>\t<entry>`; "" when no entry has two. The tie is broken by
# name so the pick is a fact about the project and not about the read's order.
pick_entry() { # pick_entry FILE
  awk -F'\t' '$1 == "map" { n[$3]++ } END { for (e in n) if (n[e] >= 2) printf "%d\t%s\n", n[e], e }' "$1" 2>/dev/null \
    | sort -t"$(printf '\t')" -k1,1nr -k2,2 | head -n 1
}

# run_bounded SECONDS KEY DIR CMD...: run one command from the directory it has
# to be run in and under a bound. A catalog entry's build is the build of the
# repository its Dockerfile belongs to — the CLI refuses it from anywhere else by
# name — and every other command here names the project through the checkout it
# is run in.
#
# The child execs, so the pid the bound kills IS the command: a hung cloud call
# would otherwise outlive the check that gave up on it, which is the one thing
# the bound exists to prevent. The ERR trap is dropped in it too: this check runs
# commands whose failing exit status is one of its own readings (a build that
# fails, a describe that answers nothing), and a trap inherited here would turn
# each of them into "an internal error" and a status of 2 before the step could
# report it.
run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  BOUNDED_TIMED_OUT=no
  ( trap - ERR; cd "$dir" || exit 1; exec "$@" ) >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
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
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check-581.sh --sandbox <a sandbox repository checkout> — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy the build's output to the terminal"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy a build's output"
}

# guard_agent_session: the check is the owner's, and it drives a billable build.
# Refusing here rather than letting each command refuse one at a time is what
# makes the block say why: a session that ran this far would report the refusals
# as findings about the release.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 581 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
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
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} (the build this check drives: the steps it reads back are the ones the release you have submits)"
}

# ---------------------------------------------------------------- the steps
# step_sandbox: the premise. The project comes from the checkout itself
# (`fugaro doctor --json`), so the check reads the project you actually work in
# rather than one it guessed, and the build is that project's.
step_sandbox() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox project)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the build this check runs is run from one"
  fi
  command -v python3 >/dev/null 2>&1 || die "Check 581 needs python3 to read the --json documents"
  say "reading the installation: fugaro doctor --json, in ${SANDBOX}"
  run_bounded "$DOCTOR_TIMEOUT" doctor "$SANDBOX" "$FUGARO_BIN" doctor --json
  # doctor exits 1 whenever any of its checks is not ok, warnings included, and
  # still prints its document; only a timeout or another status is no answer.
  if [ "$BOUNDED_TIMED_OUT" = yes ] || { [ "$BOUNDED_STATUS" != 0 ] && [ "$BOUNDED_STATUS" != 1 ]; }; then
    unmeasured sandbox "fugaro doctor --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): this check cannot read the project this checkout belongs to, and every row below is about that project. Its output is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
    return 0
  fi
  PROJECT=$(doctor_field "$SCRATCH/doctor.out" gcp_project)
  if [ -z "$PROJECT" ]; then
    unmeasured sandbox "fugaro doctor --json answered without a project block: this check cannot say which project the sandbox checkout belongs to, and the rows below are about the project, its catalog and the build. What it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  # The region the build ran in, which `gcloud builds describe` needs: a build is
  # submitted to projects/<project>/locations/<region>/builds, and a describe
  # without one reads the global location and finds nothing. The doctor document
  # carries the project's region, which is the region a build runs in unless the
  # installation names another (localcfg's BuildRegion), and CHECK581_REGION is
  # how the owner names that one.
  if [ -z "${CHECK581_REGION:-}" ]; then
    local doc_region
    doc_region=$(doctor_field "$SCRATCH/doctor.out" region)
    [ -z "$doc_region" ] || REGION=$doc_region
  fi
  case "${PROJECT}${REGION}" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${PROJECT} (region ${REGION}): this check is never run there" ;;
  esac
  fact sandbox PASS "${SANDBOX} is a Fugaro repository's checkout, and it belongs to ${PROJECT} (region ${REGION}, which is where its Cloud Build builds run and what the build is described in): the check builds this project's catalog entry and reads the build back from this project"
}

# step_catalog: what the project has today, read back rather than assumed.
# `image ls --json` is the read a job's resolver makes, and the entry this check
# builds has to be in it.
step_catalog() {
  if [ -z "$PROJECT" ]; then
    unmeasured catalog "there is no project to read a catalog from: the row above says why"
    return 0
  fi
  say "reading the catalog: fugaro image ls --json"
  run_bounded "$CLI_TIMEOUT" catalog "$SANDBOX" "$FUGARO_BIN" image ls --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured catalog "fugaro image ls --json did not answer (exit ${BOUNDED_STATUS}): this check cannot read the catalog the entry it builds has to be in. Its output is in $SCRATCH/catalog.out and $SCRATCH/catalog.err"
    return 0
  fi
  if ! read_json catalog "$SCRATCH/catalog.out" "$SCRATCH/catalog.txt"; then
    unmeasured catalog "fugaro image ls --json printed a document this check could not read: what it printed is in $SCRATCH/catalog.out"
    return 0
  fi
  # The document was read, whether or not it has an entry in it: a catalog that
  # is empty is a finding about the project, and one that was never read is a row
  # this check could not measure.
  CATALOG_OK=yes
  local count names="" shown=0 extra=0
  count=$(field "$SCRATCH/catalog.txt" count)
  while IFS=$'\t' read -r kind name isdefault tags source digest; do
    [ "$kind" = entry ] || continue
    if [ "$shown" -ge 8 ]; then
      extra=$((extra + 1))
      continue
    fi
    names="${names:+$names, }${name}(tags=${tags:-none},digest=${digest:-none})"
    shown=$((shown + 1))
  done <"$SCRATCH/catalog.txt"
  [ "$extra" -eq 0 ] || names="$names, and $extra more"
  if [ "${count:-0}" = 0 ]; then
    fact catalog FAIL "the project ${PROJECT} has no catalog entry: this check builds one entry the project's map puts two repositories on, and a project with no catalog has none. The default catalog entry is written by fugaro update (the base-image entry, R1), and the fugaro:images skill proposes the rest in your own session"
    return 0
  fi
  fact catalog PASS "the project ${PROJECT} has ${count} catalog entries: ${names} — read back with fugaro image ls --json, which is what a job's resolver reads"
}

# step_map: the pick, and the premise the whole check rests on. An entry is
# over two repositories when the project's image map puts two on it (D2301: the
# bake is the repositories the map puts on the entry being built), so the map is
# what this check reads to choose — and a project whose map puts no entry on two
# has nothing here to measure, which is a finding rather than a failure of the
# release.
step_map() {
  # The entry this check builds is decided here and nowhere else, and the row
  # starts with nothing: a map that refuses below leaves the build with no entry
  # to submit rather than the one a row just found fault with.
  local named=$ENTRY
  ENTRY=""
  if [ -z "$PROJECT" ]; then
    unmeasured map "there is no project to read a map from: the row above says why"
    return 0
  fi
  if [ "$CATALOG_OK" != yes ]; then
    unmeasured map "the catalog could not be read (the row above says why), and the entry this check builds has to be one the catalog lists: without it there is no way to tell an entry the map puts two repositories on from one no build can be submitted for. Run the check again once 'fugaro image ls' answers"
    return 0
  fi
  say "reading the map: fugaro image survey --json (read-only; nothing is cloned)"
  run_bounded "$CLI_TIMEOUT" survey "$SANDBOX" "$FUGARO_BIN" image survey --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured map "fugaro image survey --json did not answer (exit ${BOUNDED_STATUS}): the map is what says which repositories an entry serves, and the build below is chosen from it. Its output is in $SCRATCH/survey.out and $SCRATCH/survey.err"
    return 0
  fi
  if ! read_json survey "$SCRATCH/survey.out" "$SCRATCH/survey.txt"; then
    unmeasured map "fugaro image survey --json printed a document this check could not read: what it printed is in $SCRATCH/survey.out"
    return 0
  fi
  local bake picked count on_entry member
  bake=$(field "$SCRATCH/survey.txt" bake_repos)
  member=$(field "$SCRATCH/survey.txt" member)
  if [ "$bake" = false ]; then
    fact map FAIL "the project ${PROJECT} has bake_repos=false (setup.bake_repos): a build of any entry bakes nothing, so no image carries a checkout and there is no multi-repository bake to measure. Turn it on with 'fugaro settings set bake_repos true' and rebuild the entry with 'fugaro image build <entry>' — the switch is what the BUILD reads, so the images that exist keep what they were built with until they are rebuilt"
    return 0
  fi
  picked=$(pick_entry "$SCRATCH/survey.txt")
  if [ -z "$picked" ]; then
    fact map FAIL "no catalog entry of ${PROJECT} is over two repositories: the map (setup.${member:-repo_images}) puts at most one repository on every entry, and this check's subject is the entry that serves two (D2301). Map a second repository onto an entry — 'fugaro image map <the repository> <the entry>' writes this machine's map, and 'fugaro update' publishes it — or name the entry with CHECK581_ENTRY once the map serves it two"
    return 0
  fi
  count=${picked%%$'\t'*}
  picked=${picked#*$'\t'}
  if [ -n "$ENTRY_CLI_SET" ]; then
    if [ -z "$(entry_field "$SCRATCH/catalog.txt" "$named" 2)" ]; then
      fact map FAIL "this project's catalog has no entry ${named} (CHECK581_ENTRY): the entry this check builds has to be one the project's catalog lists. The entries it has: $(awk -F'\t' '$1 == "entry" { printf "%s ", $2 }' "$SCRATCH/catalog.txt")"
      return 0
    fi
    on_entry=$(repos_on_entry "$SCRATCH/survey.txt" "$named")
    if [ -z "$on_entry" ]; then
      fact map FAIL "the map (setup.${member:-repo_images}) puts no repository on the entry ${named} (CHECK581_ENTRY), so a build of it bakes nothing: D2301's bake is the repositories the map puts on the entry being built, and the entry the project's map puts the most repositories on is ${picked} with ${count}. Name that one, or map a repository onto ${named} with 'fugaro image map <the repository> ${named}' and publish it with 'fugaro update'"
      return 0
    fi
    count=$(printf '%s' "$on_entry" | awk -F, '{print NF}')
    if [ "$count" -lt 2 ]; then
      fact map FAIL "the map puts ${count} repository on the entry ${named} (CHECK581_ENTRY) — ${on_entry} — and this check's subject is an entry over two (D2301). The entry the project's map puts the most repositories on is ${picked} with $(repos_on_entry "$SCRATCH/survey.txt" "$picked" | awk -F, '{print NF}'); name it, or map a second repository onto ${named}"
      return 0
    fi
    ENTRY=$named
  else
    ENTRY=$picked
  fi
  ENTRY_REPOS=$(repos_on_entry "$SCRATCH/survey.txt" "$ENTRY")
  ENTRY_SOURCE=$(entry_field "$SCRATCH/catalog.txt" "$ENTRY" 5)
  ENTRY_DIGEST_BEFORE=$(entry_field "$SCRATCH/catalog.txt" "$ENTRY" 6)
  if [ -z "$ENTRY_SOURCE" ]; then
    ENTRY=""
    fact map FAIL "the catalog does not list the entry ${picked}, which the map puts ${ENTRY_REPOS} on: a map that names an entry the catalog has not got is one no build of it can follow. Read 'fugaro image ls' and 'fugaro image survey' together, and publish the map with 'fugaro update' once both agree"
    return 0
  fi
  MAP_REPOS=$ENTRY_REPOS
  fact map PASS "the entry ${ENTRY} is over ${count} repositories — ${ENTRY_REPOS} — read from the project's own map (setup.${member:-repo_images}), and its definition is ${ENTRY_SOURCE}. bake_repos=${bake:-true}${ENTRY_CLI_SET:+, and CHECK581_ENTRY named this entry rather than the check picking the widest one}"
}

# guard_checkout_project: the checkout the billable build is submitted from has
# to belong to the project this check is about. step_sandbox reads the project
# off the sandbox checkout and everything above the build is that project's
# catalog and map; the build, though, runs in $CHECKOUT (the sandbox checkout
# unless CHECK581_CHECKOUT names another), as the build account of the repository
# it is submitted from, and it promotes the entry in the project that repository
# belongs to. A checkout whose own doctor names another project — or one naming
# belong — would therefore run a real Cloud Build in that project and move its
# `latest`. Read here rather than assumed, and only when the two directories
# differ: the sandbox checkout has been read already. 0 when the build may run
# there, 1 when the checkout's project could not be read (the build row says so
# and no build runs).
guard_checkout_project() {
  [ "$CHECKOUT" != "$SANDBOX" ] || return 0
  say "reading the checkout the build runs from: fugaro doctor --json, in ${CHECKOUT}"
  run_bounded "$DOCTOR_TIMEOUT" checkout "$CHECKOUT" "$FUGARO_BIN" doctor --json
  # As in step_sandbox: doctor exits 1 whenever any of its checks is not ok and
  # still prints its document, so only a timeout or another status is no answer.
  if [ "$BOUNDED_TIMED_OUT" = yes ] || { [ "$BOUNDED_STATUS" != 0 ] && [ "$BOUNDED_STATUS" != 1 ]; }; then
    unmeasured build "fugaro doctor --json did not answer in ${CHECKOUT} (exit ${BOUNDED_STATUS}), the checkout the build would be submitted from: this check cannot say which project that billable build would run in, and whose latest it would move, and the build this row is about is ${PROJECT}'s. Its output is in $SCRATCH/checkout.out and $SCRATCH/checkout.err"
    return 1
  fi
  local checkout_project
  checkout_project=$(doctor_field "$SCRATCH/checkout.out" gcp_project)
  if [ -z "$checkout_project" ]; then
    stop "fugaro doctor --json answered without a project in ${CHECKOUT}, the checkout the build would be submitted from: this check cannot say which project that billable build would run in, and it submits none it cannot name"
  fi
  case "$checkout_project" in
  *belong*) stop "the checkout the build would be submitted from, $CHECKOUT, belongs to ${checkout_project}: this check is never run there, and a build from it would be a billable Cloud Build in that project, moving its latest" ;;
  esac
  if [ "$checkout_project" != "$PROJECT" ]; then
    stop "the checkout at $CHECKOUT belongs to ${checkout_project} and the sandbox checkout at $SANDBOX belongs to ${PROJECT}: a build submitted from the first would be a billable Cloud Build in ${checkout_project}, promoting that project's entry, and this check is about ${PROJECT}'s catalog, its map and its build. Point --checkout (or CHECK581_CHECKOUT) at the checkout of a repository of ${PROJECT}, or run the check with --sandbox at the checkout of ${checkout_project}"
  fi
  return 0
}

# step_build: the build that counts. One Cloud Build of the entry, from the
# checkout of the repository its Dockerfile belongs to, because the CLI refuses a
# build from another repository by name — a Dockerfile built against the wrong
# account builds the wrong thing under the wrong credential.
step_build() {
  if [ -z "$ENTRY" ]; then
    unmeasured build "there is no entry to build: the row above says why"
    return 0
  fi
  if [ ! -f "$CHECKOUT/fugaro.yaml" ]; then
    unmeasured build "--checkout carries no fugaro.yaml: $CHECKOUT is not a Fugaro repository's checkout, and a catalog build is run from the checkout of the repository the entry's Dockerfile belongs to. The entry's definition is ${ENTRY_SOURCE}, so point CHECK581_CHECKOUT at that repository's checkout"
    return 0
  fi
  # Before the build and before anything billable: the checkout the build would
  # be submitted from has to be the sandbox project's.
  guard_checkout_project || return 0
  say "building ${ENTRY} from ${CHECKOUT} (a real Cloud Build: billable)"
  run_bounded "$BUILD_TIMEOUT" build "$CHECKOUT" "$FUGARO_BIN" image build "$ENTRY"
  local text
  text=$(cat "$SCRATCH/build.out" "$SCRATCH/build.err" 2>/dev/null || true)
  BUILD_ID=$(printf '%s\n' "$text" | grep -o 'Cloud Build build [A-Za-z0-9._-]*' | head -n 1 | awk '{print $4}' || true)
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured build "the build of ${ENTRY} did not finish inside ${BUILD_TIMEOUT}s and was ended: a Cloud Build that hangs is a longer look than a check should take, and this check cannot say whether the entry builds or whether the scan runs. The build it submitted is ${BUILD_ID:-not named in its output}, and its output is in $SCRATCH/build.out and $SCRATCH/build.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact build FAIL "fugaro image build ${ENTRY} exited ${BOUNDED_STATUS} in ${CHECKOUT}: the entry the project's map puts ${MAP_REPOS} on does not build through Cloud Build, so no job of those repositories starts on what the bake is for. A refusal naming another repository means the entry's Dockerfile belongs to it — run the check with CHECK581_CHECKOUT set to that repository's checkout. What it printed is in $SCRATCH/build.out and $SCRATCH/build.err"
    return 0
  fi
  if [ -z "$BUILD_ID" ]; then
    fact build FAIL "the build of ${ENTRY} succeeded but its output names no Cloud Build build id (the line 'fugaro: Cloud Build build <id> of <image> submitted; log: <url>'): without the id the check cannot read the build's own steps, which is where the scan is. What it printed is in $SCRATCH/build.out and $SCRATCH/build.err"
    return 0
  fi
  BUILT=yes
  fact build PASS "the entry ${ENTRY} built through Cloud Build as build ${BUILD_ID} (a real, billable build, run from ${CHECKOUT}): the build this check reads its own steps back from"
}

# step_baked: what the build says it baked, out of the build's own note. Every
# repository the map puts on the entry has to be named there: the note is
# printed from the plan the definition was composed with, and the build's pin
# step verifies the definition it read is the one the submitting CLI composed, so
# a note that names both repositories and a build that succeeded is the outside
# evidence that the image carries both checkouts.
step_baked() {
  if [ "$BUILT" != yes ]; then
    unmeasured baked "no build ran, so there is no note to read: the row above says why"
    return 0
  fi
  local text note missing=""
  text=$(cat "$SCRATCH/build.out" "$SCRATCH/build.err" 2>/dev/null || true)
  note=$(printf '%s\n' "$text" | sed -n 's/.*bakes the checkouts of \(.*\), so the image carries.*/\1/p' | head -n 1)
  if [ -z "$note" ]; then
    # The singular form is the same note for one repository, and the no-bake form
    # carries the CLI's own reason and fix.
    note=$(printf '%s\n' "$text" | sed -n 's/.*bakes the checkout of \(.*\), so the image carries that repository.s source.*/\1/p' | head -n 1)
    if [ -n "$note" ]; then
      NOTE_REPOS=$(printf '%s\n' "$note" | awk -F' at ' '{print $1}')
      fact baked FAIL "the build of ${ENTRY} says it baked one repository — ${NOTE_REPOS} — while the project's map puts ${MAP_REPOS} on it: the image a job of the second repository starts on carries no checkout of it, and the definition the build composed carries no bake stage for it. The repositories the map puts on an entry are what the build bakes (D2301), so this is either a map published after the definition was composed — rebuild once 'fugaro update' has published it — or a build that read another map"
      return 0
    fi
    local nobake
    nobake=$(printf '%s\n' "$text" | grep -m1 'no checkout baked' || true)
    if [ -n "$nobake" ]; then
      fact baked FAIL "the build of ${ENTRY} baked no checkout at all, in its own words: $(printf '%s' "$nobake" | head -c 400). The map puts ${MAP_REPOS} on this entry, so the build's own repository has to be one of them and its workflow has to run this entry — a build bakes only the entry its own repository runs (imagebake.Decide), and a build run from a checkout the map does not put on ${ENTRY} bakes nothing. Run the check from the checkout of one of ${MAP_REPOS} (CHECK581_CHECKOUT)"
      return 0
    fi
    unmeasured baked "the build of ${ENTRY} printed no bake note this check could read: the note is how a build says which checkouts the image carries, and without it the row cannot be measured. What the build printed is in $SCRATCH/build.out and $SCRATCH/build.err"
    return 0
  fi
  NOTE_REPOS=$(printf '%s\n' "$note" | tr ',' '\n' | sed 's/^ *//; s/ *$//; s/ at .*//' | paste -sd, - 2>/dev/null || true)
  local repo
  while IFS= read -r repo; do
    [ -n "$repo" ] || continue
    case ",$NOTE_REPOS," in
    *",$repo,"*) ;;
    *) missing="${missing:+$missing, }$repo" ;;
    esac
  done < <(printf '%s\n' "$MAP_REPOS" | tr ',' '\n')
  if [ -n "$missing" ]; then
    fact baked FAIL "the build of ${ENTRY} names the checkouts it baked as ${NOTE_REPOS}, and the project's map puts ${MAP_REPOS} on the entry: ${missing} is not among them, so the image does not carry that repository's source and its jobs clone at start. The definition the build read is composed from the map the submitting machine holds, so publish the map ('fugaro update') and rebuild"
    return 0
  fi
  fact baked PASS "the build of ${ENTRY} bakes the checkouts of ${NOTE_REPOS}, which covers every repository the project's map puts on it (${MAP_REPOS}) — one bake stage per repository, read from the build's own note, and the build succeeded so its smoke ran against the baked checkout of the repository it runs for"
}

# step_scan: B3's scan, read out of the build's own steps. The scan is a step of
# the build rather than something this check can run over an image on its own: it
# is images/scan.sh --secrets over the digest the build pushed, and it stands
# between the smoke and the promote so an image that carries a credential of the
# build's is refused rather than moved to latest. The build is read in the region
# it was submitted to — a catalog build goes to
# projects/<project>/locations/<BuildRegion>/builds — because a `gcloud builds
# describe` without one reads the global location and finds nothing.
step_scan() {
  if [ "$BUILT" != yes ]; then
    unmeasured scan "no build ran, so there are no steps to read: the row above says why"
    return 0
  fi
  say "reading the build's steps: gcloud builds describe ${BUILD_ID} --region=${REGION} --project=${PROJECT}"
  run_bounded "$CLI_TIMEOUT" describe "$SANDBOX" gcloud builds describe "$BUILD_ID" --region="$REGION" --project="$PROJECT" --format=json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured scan "gcloud builds describe ${BUILD_ID} --region=${REGION} did not answer (exit ${BOUNDED_STATUS}): the scan is a step of that build and the build's own record of it is the only place it can be read. The region is the one the build was submitted to (projects/${PROJECT}/locations/${REGION}/builds, from fugaro doctor); if the installation names another one for builds, name it with CHECK581_REGION. Its output is in $SCRATCH/describe.out and $SCRATCH/describe.err"
    return 0
  fi
  if ! read_json build "$SCRATCH/describe.out" "$SCRATCH/describe.txt"; then
    unmeasured scan "gcloud builds describe ${BUILD_ID} printed a document this check could not read: what it printed is in $SCRATCH/describe.out"
    return 0
  fi
  local status detail scan_idx promote_idx scan_status
  status=$(field "$SCRATCH/describe.txt" status)
  detail=$(field "$SCRATCH/describe.txt" detail)
  scan_idx=$(step_index "$SCRATCH/describe.txt" catalog-scan)
  promote_idx=$(step_index "$SCRATCH/describe.txt" promote)
  scan_status=$(step_status "$SCRATCH/describe.txt" catalog-scan)
  if [ "$status" != SUCCESS ]; then
    fact scan FAIL "the build ${BUILD_ID} of ${ENTRY} ended ${status:-without a status}${detail:+, its own failure detail: $(printf '%s' "$detail" | head -c 300)}: a build that failed at or after the scan is one whose image was refused rather than promoted — which is the scan working — and a build that failed before it is one the scan never reached. Read the whole build with 'gcloud builds describe ${BUILD_ID} --region=${REGION} --project=${PROJECT}' and its log with 'gcloud builds log ${BUILD_ID} --region=${REGION} --project=${PROJECT}'. The steps this check read: $(awk -F'\t' '$1 == "step" { printf "%s ", $3 }' "$SCRATCH/describe.txt")"
    return 0
  fi
  if [ -z "$scan_idx" ]; then
    fact scan FAIL "the build ${BUILD_ID} of ${ENTRY} ran SUCCESS with no 'catalog-scan' step: the steps it did run are $(awk -F'\t' '$1 == "step" { printf "%s ", $3 }' "$SCRATCH/describe.txt"). B3's scan is a step of every catalog build, between the smoke and the promote (internal/backend/gcp's catalogBuildSteps), and an image that reached latest without it is one nothing looked at for a leftover credential of the build's"
    return 0
  fi
  if [ "$scan_status" != SUCCESS ]; then
    fact scan FAIL "the build ${BUILD_ID} of ${ENTRY} carries the step 'catalog-scan' with status ${scan_status:-none recorded}: the build's own record of that step is where the scan's outcome is, and a scan that did not succeed is one whose image must not have been promoted. The steps this check read: $(awk -F'\t' '$1 == "step" { printf "%s ", $3 }' "$SCRATCH/describe.txt")"
    return 0
  fi
  if [ -z "$promote_idx" ]; then
    fact scan FAIL "the build ${BUILD_ID} of ${ENTRY} ran 'catalog-scan' but carries no 'promote' step: the steps it did run are $(awk -F'\t' '$1 == "step" { printf "%s ", $3 }' "$SCRATCH/describe.txt"). The scan's whole point is that it stands before the promote, and a build with no promote is not the catalog build this check measures"
    return 0
  fi
  if [ "$scan_idx" -ge "$promote_idx" ]; then
    fact scan FAIL "in the build ${BUILD_ID} of ${ENTRY} the step 'catalog-scan' stands at position ${scan_idx} and 'promote' at ${promote_idx}: the scan has to run BEFORE the image is moved to latest, or an image carrying a leftover credential of the build's is promoted and only scanned afterwards (B3, R2). The steps in order: $(awk -F'\t' '$1 == "step" { printf "%s ", $3 }' "$SCRATCH/describe.txt")"
    return 0
  fi
  fact scan PASS "the build ${BUILD_ID} of ${ENTRY} ran 'catalog-scan' at step ${scan_idx} with status SUCCESS, before 'promote' at step ${promote_idx}, and the build itself ended SUCCESS — so the image the promote moved to latest was scanned for a git config credential, a .netrc, a credential-helper store and a token in the environment or in any layer, and the scan refused nothing (B3, R2)"
}

# step_digest: the digest the build recorded, read back from the catalog. The
# record step stands after the promote, so the row is only worth reading when the
# digest the entry carries is the one this build made: the build's own output
# names it (`built <image>@<digest>`), and a repeated build that recorded nothing
# leaves the digest the entry already had. A row that passed on that digest would
# report a record that never happened.
step_digest() {
  if [ "$BUILT" != yes ]; then
    unmeasured digest "no build ran, so there is no digest to read: the row above says why"
    return 0
  fi
  say "reading the digest back: fugaro image ls --json"
  run_bounded "$CLI_TIMEOUT" digests "$SANDBOX" "$FUGARO_BIN" image ls --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured digest "fugaro image ls --json did not answer after the build (exit ${BOUNDED_STATUS}), so this check cannot say what the entry recorded. Its output is in $SCRATCH/digests.out and $SCRATCH/digests.err"
    return 0
  fi
  if ! read_json catalog "$SCRATCH/digests.out" "$SCRATCH/digests.txt"; then
    unmeasured digest "fugaro image ls --json printed a document this check could not read after the build: what it printed is in $SCRATCH/digests.out"
    return 0
  fi
  local digest reported
  digest=$(entry_field "$SCRATCH/digests.txt" "$ENTRY" 6)
  reported=$(build_digest "$SCRATCH/build.out" "$SCRATCH/build.err")
  if [ -z "$digest" ]; then
    fact digest FAIL "the entry ${ENTRY} recorded no digest after the build ${BUILD_ID} ran (before it: ${ENTRY_DIGEST_BEFORE:-none}, and the build itself reported ${reported:-no digest}): the digest is written by the build's own record step, which runs after the promote, so an entry with none is one whose build did not reach the end — and what a job's record, the daily check and a rebuild compare is the digest. What fugaro image ls --json printed is in $SCRATCH/digests.out"
    return 0
  fi
  if [ -n "$reported" ]; then
    if [ "$digest" = "$reported" ]; then
      fact digest PASS "the entry ${ENTRY} records the digest ${reported} the build ${BUILD_ID} reported it made, read back from fugaro image ls --json: the record step, which stands after the promote the scan guards, ran for this build"
      return 0
    fi
    fact digest FAIL "the build ${BUILD_ID} of ${ENTRY} reported the digest ${reported} and the entry records ${digest} (before this check's build: ${ENTRY_DIGEST_BEFORE:-none}): the record step is what writes the digest the build made, so an entry carrying another one is one whose record did not take this build — and what a job's record, the daily check and a rebuild compare is the digest. Read the build with 'gcloud builds log ${BUILD_ID} --region=${REGION} --project=${PROJECT}', and what fugaro image ls --json printed is in $SCRATCH/digests.out"
    return 0
  fi
  if [ "$digest" = "${ENTRY_DIGEST_BEFORE:-}" ]; then
    unmeasured digest "the build ${BUILD_ID} of ${ENTRY} printed no digest of its own, and the entry records the same digest it had before the build (${digest}): this check cannot tell a build that recorded the digest it made from one that recorded nothing, and the digest is the one thing the record step is for. Read the build with 'gcloud builds log ${BUILD_ID} --region=${REGION} --project=${PROJECT}'; the build's own output is in $SCRATCH/build.out and $SCRATCH/build.err"
    return 0
  fi
  fact digest PASS "the entry ${ENTRY} records the digest ${digest}, where it recorded ${ENTRY_DIGEST_BEFORE:-none} before this check's build: the build ${BUILD_ID} printed no digest of its own, so the record is read from the change — the record step stands after the promote the scan guards, and it is the only thing that writes a digest there"
}

# restore: what this check changed, said rather than assumed.
restore() {
  RESTORE_VERDICT=PASS
  if [ -n "$DRY_RUN" ]; then
    RESTORE_NOTE="this was a dry run: nothing was created, nothing was run and no command was given to fugaro or gcloud"
    return 0
  fi
  local untouched="this check wrote no catalog entry, no mapping and no switch, launched no run and opened no pull request."
  if [ "$BUILT" = yes ]; then
    RESTORE_NOTE="$untouched It ran one Cloud Build of ${ENTRY} (billable) and that build promoted the entry's image — latest moved to the digest it made, which is what 'fugaro image build' does on any day and is not the check's to undo. The scratch directory held the commands' output"
  else
    RESTORE_NOTE="$untouched No image was promoted by this run — a build that was refused, failed or never started moves nothing — so there is nothing here to undo. The scratch directory held the commands' output"
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
  say "CHECK 581 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [read] fugaro doctor --json in ${SANDBOX} — the project this checkout belongs to"
  if [ "$CHECKOUT" != "$SANDBOX" ]; then
    say "  [read] fugaro doctor --json in ${CHECKOUT} — the project the checkout the build would be submitted from belongs to, which has to be the same one"
  fi
  say "  [read] fugaro image ls --json — the catalog: the entries, their digests and the default"
  say "  [network, read-only, no clone] fugaro image survey --json — the image map the entry over two repositories is chosen from"
  say "  [network, BILLABLE] one Cloud Build — fugaro image build <the entry the map puts the most repositories on> from ${CHECKOUT}"
  say "  [read] the build's own note — the repositories it says it baked"
  say "  [network, read, creates nothing] gcloud builds describe <build id> --region=<the region the build ran in> --project=<the project> — the build's steps: catalog-scan, before promote, and the build's status"
  say "  [read] fugaro image ls --json again — the digest the build recorded"
  fact sandbox INFO "planned: a real run measures this row; --dry-run did not"
  fact catalog INFO "planned: a real run measures this row; --dry-run did not"
  fact map INFO "planned: a real run measures this row; --dry-run did not"
  fact build INFO "planned: a real run measures this row; --dry-run did not"
  fact baked INFO "planned: a real run measures this row; --dry-run did not"
  fact scan INFO "planned: a real run measures this row; --dry-run did not"
  fact digest INFO "planned: a real run measures this row; --dry-run did not"
  fact dry-run PASS "the plan was printed and nothing was run: no doctor, no catalog read, no survey, no build and no describe"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(fugaro-version xtrace agent-session belong sandbox catalog map build
  baked scan digest restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 581 RESULT\n' >&3
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
  printf 'CHECK 581 RESULT END (exit %s)\n' "$status" >&3
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
ENTRY_CLI=""
CHECKOUT_CLI=""
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of a repository of the sandbox project, which is what the build is run from"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --entry)
    [ $# -ge 2 ] || die "--entry needs a catalog entry name: the entry over two repositories to build"
    ENTRY_CLI=$2
    shift 2
    ;;
  --checkout)
    [ $# -ge 2 ] || die "--checkout needs a path: the checkout of the repository the entry's Dockerfile belongs to"
    CHECKOUT_CLI=$2
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

SANDBOX=${CHECK581_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 581 needs --sandbox: the checkout of a repository of the sandbox project. The build is run from it (or from CHECK581_CHECKOUT), the catalog and the map are read through it, and fugaro doctor names the project it belongs to. Usage: bash scripts/live-checks/check-581.sh --sandbox <a sandbox repository checkout>"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
CHECKOUT=${CHECK581_CHECKOUT:-${CHECKOUT_CLI:-$SANDBOX}}
case "$CHECKOUT" in
/*) ;;
*) CHECKOUT="$PWD/$CHECKOUT" ;;
esac
ENTRY=${CHECK581_ENTRY:-${ENTRY_CLI:-}}
ENTRY_CLI_SET=""
[ -z "$ENTRY" ] || ENTRY_CLI_SET=1
DRY_RUN=${CHECK581_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK581_KEEP_SCRATCH:-}
REGION=${CHECK581_REGION:-us-east5}
DOCTOR_TIMEOUT=${CHECK581_DOCTOR_TIMEOUT_SECONDS:-180}
CLI_TIMEOUT=${CHECK581_CLI_TIMEOUT_SECONDS:-180}
BUILD_TIMEOUT=${CHECK581_BUILD_TIMEOUT_SECONDS:-1800}
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
  [ -n "$FUGARO_BIN" ] || die "Check 581 needs 'fugaro' on PATH: the check drives the build you have installed, and it is not there"
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
  step_map
  step_build
  step_baked
  step_scan
  step_digest
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
