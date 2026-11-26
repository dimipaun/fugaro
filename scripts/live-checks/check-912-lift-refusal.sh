#!/usr/bin/env bash
# Check 912 — after release B, that `fugaro validate` refuses a lifted key on
# the default branch and only names it on a follow-up ref. Run by YOU, with the
# release-B binary installed.
#
# L8 (docs/repo-override-rulings) moves a repository's model choice, its A/B
# rate, its rigor level and areas, its model grades and its `.fugaro/recipes/`
# into the project. Release A drops a lifted key and names it; release B refuses
# it, with the fix, on the default branch alone (XB1, XB6): on any other ref — a
# follow-up's base, a run's own `--ref` — it stays a drop, named and not refused,
# or every follow-up of a branch cut before the migration would be refused. The
# sentence a person reads is `LiftedKey.Why` (C1, the one refusal text), and
# release B prints it as a problem where it refuses and as a note where it only
# names. Nothing offline settles any of that for a real installation, so this
# check measures it where it lives: one repository's own checkout, read at two
# refs.
#
#   premise   the checkout is an enrolled repository of the project, and both
#             refs it measures are there: the default branch, and the follow-up
#             ref (the default branch's own parent, unless --older-ref names
#             another).
#   default   `fugaro validate` in a checkout ON the default branch refuses: the
#             file is invalid, and every lifted sentence is a problem that names
#             the key and the command or the project path that states it in its
#             new home.
#   older     the same read in a checkout at the follow-up ref names the same
#             keys as notes and refuses nothing: the file is valid, exit 0.
#   recipes   a `.fugaro/recipes/` folder at the default branch is refused with
#             PR5's cleanup line — `git rm -r .fugaro/recipes` where every copy
#             is stored, `fugaro recipes publish <file>` first otherwise.
#   update    `fugaro update --plan-only` names this repository with the fix and
#             migrates nothing (XB5) — the half of release B you meet first.
#   writes    it launched nothing: one `fugaro validate` per ref and one
#             `fugaro update --plan-only`, each with stdin closed, no image
#             built, and no secret read at all.
#   restore   the two worktrees it added under its scratch directory, and the
#             local branch it created for them if any, are gone.
#
# The two refs are read through two `git worktree`s of your own checkout, under
# this check's scratch directory, because `validate` decides by the branch the
# checkout it reads is on (XB6): a detached or renamed checkout is not "the
# default branch", and would measure the naming mode twice. A worktree shares
# your checkout's objects and its `origin`, so what `validate` reads is really
# this repository at that ref. They are the only thing this check adds to the
# repository, they carry no commit of yours and they change no file of your
# working tree, and the `restore` row says whether they really went.
#
# Usage:      bash scripts/live-checks/check-912-lift-refusal.sh <project> <checkout> \
#               [--older-ref <ref>] [--dry-run]
#             --older-ref the ref the follow-up half is read at (default:
#                         origin/<the checkout's base branch>~1 — the commit the
#                         default branch was at before its last, which a
#                         follow-up's base is). A checkout is never fetched:
#                         fetch it yourself first if the ref is stale.
#             --dry-run   prints the plan and calls NOTHING.
# Needs:      bash, mktemp, python3, git, fugaro.
# Where:      the installation you name and the checkout you name, and NOWHERE
#             else. `fugaro update --plan-only` reads the project's own cloud
#             objects, as it does for you; nothing is written there.
# Costs:      nothing. No run is launched and no image is built.
# Runs:       by YOU. No agent runs any step of it — the check refuses to start
#             in a coding agent's session (CLAUDECODE, CLAUDE_CODE_ENTRYPOINT,
#             CLAUDE_CODE_REMOTE, CURSOR_AGENT, AI_AGENT) — and its own test
#             (scripts/live-checks/check-912-lift-refusal_test.sh) drives it
#             against a fake `fugaro` on PATH and real git checkouts, and touches
#             no cloud.
# Cleanup:    its two worktrees and its scratch directory are removed from the
#             exit trap, and the `restore` row says whether they really went.
# Output:     progress on stderr, ONE block on stdout headed CHECK 912 RESULT,
#             to copy back. Its last line is the verdict, and the verdict is the
#             exit status:
#
#               CHECK 912 RESULT: PASS (exit 0)        every criterion passed;
#               CHECK 912 RESULT: FAIL (exit 1)        a criterion was measured
#                                                      and failed;
#               CHECK 912 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                      could measure everything;
#               CHECK 912 RESULT: INCOMPLETE (exit 3)  it ran to the end and at
#                                                      least one row reads
#                                                      UNVERIFIED.
#
# NO secret is read at all, and none may be printed: every row about a document
# names a key, a ref, a path or a line of a plan. No token is minted, nothing is
# mounted, and no command this check runs writes anything.
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
QUIET=no
WT_DEFAULT=""
WT_OLDER=""
HELPER_BRANCH=""
BASE_BRANCH=""
CHECKOUT=""
MADE_BASE_BRANCH=no

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
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 912: it reads the installation's own repository and the update's plan. Run it yourself: bash scripts/live-checks/check-912-lift-refusal.sh <project> <checkout>"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x), and its trace would print the arguments of every command this check runs. Run it without -x: bash scripts/live-checks/check-912-lift-refusal.sh <project> <checkout>"
  fi
}

usage() {
  cat <<'USAGE'
Check 912 — after release B, that `fugaro validate` refuses a lifted key on the
default branch and only names it on a follow-up ref. Run by YOU, with the
release-B binary installed.

Usage:
  bash scripts/live-checks/check-912-lift-refusal.sh <project> <checkout> [--older-ref <ref>] [--dry-run]
  bash scripts/live-checks/check-912-lift-refusal.sh --help

It reads the checkout you name at two refs — the default branch, and the
follow-up ref (the default branch's parent unless --older-ref names another) —
and runs `fugaro validate` once in each, through a git worktree of that checkout
under its own scratch directory. The default branch must refuse every lifted key
the file writes, with the fix, and the follow-up ref must name the same keys and
refuse nothing. `fugaro update --plan-only` must name the repository too.

Every row it prints is one criterion of the register's Check 912; the last line
of the block is the verdict and the exit status:

  PASS (exit 0)        every criterion passed
  FAIL (exit 1)        a criterion was measured and failed
  INCOMPLETE (exit 2)  the run stopped before it could measure everything
  INCOMPLETE (exit 3)  at least one row reads UNVERIFIED
USAGE
}

OLDER_REF=""
DRY_RUN=no
POSITIONAL=()
while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  --older-ref)
    [ $# -ge 2 ] || stop "--older-ref needs a revision, e.g. origin/main~1"
    OLDER_REF=$2
    shift 2
    ;;
  --dry-run)
    DRY_RUN=yes
    shift
    ;;
  --*)
    stop "unknown argument $1 (usage: check-912-lift-refusal.sh <project> <checkout> [--older-ref <ref>], --help, or --dry-run)"
    ;;
  *)
    POSITIONAL+=("$1")
    shift
    ;;
  esac
done
[ "${#POSITIONAL[@]}" -ge 1 ] || stop "no <project> was given (or --help for the usage)"
[ "${#POSITIONAL[@]}" -ge 2 ] || stop "no <checkout> was given: name the repository checkout this check reads (or --help for the usage)"
[ "${#POSITIONAL[@]}" -le 2 ] || stop "two arguments at most: <project> <checkout> (got ${#POSITIONAL[@]})"
PROJECT=${POSITIONAL[0]}
CHECKOUT=${POSITIONAL[1]}

CONFIG_HOME=${XDG_CONFIG_HOME:-${HOME:-}/.config}
PROJECT_FILE=$CONFIG_HOME/fugaro/projects/${PROJECT:-<project>}.yaml

dry_run_plan() {
  cat <<PLAN
Check 912 plan (nothing is called: no fugaro, no git)

  project:       ${PROJECT:-<not given>}
  checkout:      ${CHECKOUT:-<not given>}
  project file:  $PROJECT_FILE
  follow-up ref: ${OLDER_REF:-origin/<the base branch of the checkout>~1}

1. read the installation (one call):
   fugaro config list --json
     -> <checkout>'s owner/name, whether the project enrolls it, and its base branch.
   git -C <checkout> rev-parse --verify <base>^{commit}; the same for the follow-up ref.
     -> both refs are there. Nothing is fetched.

2. two worktrees under this check's scratch directory, removed at exit:
   git -C <checkout> worktree add --force <scratch>/wt-default <base>
   git -C <checkout> worktree add -b <scratch>/wt-followup <follow-up ref>
     -> one checkout ON the default branch, one ON a follow-up branch. They share
        the checkout's objects and its origin; no file of your working tree moves.

3. the read (one call per ref):
   (in each worktree) fugaro validate --json
     -> the default branch must refuse every lifted key the file writes, with the
        fix, as a problem; the follow-up ref must name the same keys as notes,
        refuse none, and be valid.

4. the update's own view (one call, writes nothing):
   fugaro update --plan-only --project <project>
     -> its lines must name this repository with the fix.

No run is launched, no image is built, and no secret is read.
PLAN
}

if [ "$DRY_RUN" = yes ]; then
  guard_xtrace
  guard_agent
  dry_run_plan
  exit 0
fi

guard_xtrace
guard_agent
need bash
need mktemp
need python3
need git
need fugaro

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk912.XXXXXXXX") || stop "cannot make a scratch directory"
chmod 700 "$SCRATCH"
FACTS="$SCRATCH/facts"
: >"$FACTS"
MARKER="$SCRATCH/.chk912-mine"
: >"$MARKER"

combine() { # combine <facts> <FAIL|UNVERIFIED|aborted>
  case "$2" in
  aborted) case "$1" in aborted\|*|*$'\n'aborted\|*) return 0 ;; esac ;;
  *) case "$1" in *"|$2|"*) return 0 ;; esac ;;
  esac
  return 1
}

verdict() { # verdict <facts>
  if combine "$1" aborted; then
    printf 'INCOMPLETE'
  elif combine "$1" FAIL; then
    printf 'FAIL'
  elif combine "$1" UNVERIFIED; then
    printf 'INCOMPLETE'
  else
    printf 'PASS'
  fi
}

print_block() { # print_block <facts> <status>
  local facts=$1 status=$2
  printf '\nCHECK 912 RESULT\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-14s %-11s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 912 RESULT: %s (exit %s)\n' "$(verdict "$facts")" "$status"
}

# remove_worktrees takes back the two worktrees, the branch the check made for
# them, and any local branch `git worktree add` created for the default half
# where the checkout had none: the worktree entry and the branch are the only
# things this check adds to the repository, and RESTORE_DETAIL is what the
# `restore` row reports. It runs from the exit trap, so it says what is left
# rather than failing the run: a worktree it cannot remove is reported as a
# FAIL by the caller.
RESTORE_DETAIL=""
remove_worktrees() {
  local where left=""
  for where in "$WT_DEFAULT" "$WT_OLDER"; do
    [ -n "$where" ] || continue
    git -C "$CHECKOUT" worktree remove --force "$where" >/dev/null 2>&1 || true
    [ -e "$where" ] && left="${left}${left:+, }$where"
  done
  [ -z "$WT_DEFAULT" ] && [ -z "$WT_OLDER" ] || git -C "$CHECKOUT" worktree prune >/dev/null 2>&1 || true
  if [ -n "$HELPER_BRANCH" ]; then
    git -C "$CHECKOUT" branch -D "$HELPER_BRANCH" >/dev/null 2>&1 || true
  fi
  if [ "$MADE_BASE_BRANCH" = yes ] && [ -n "$BASE_BRANCH" ]; then
    git -C "$CHECKOUT" branch -D "$BASE_BRANCH" >/dev/null 2>&1 || true
  fi
  if [ -n "$left" ]; then
    RESTORE_DETAIL="the worktree(s) $left are STILL THERE — remove them yourself: git -C $CHECKOUT worktree remove --force <path>"
  else
    RESTORE_DETAIL="the two worktrees under $SCRATCH are gone, the follow-up branch this check made is deleted, and no entry of yours was left in .git/worktrees"
  fi
}

cleanup() {
  [ -n "$SCRATCH" ] || return 0
  remove_worktrees
  if [ -f "$MARKER" ]; then
    rm -rf "$SCRATCH" 2>/dev/null || true
    [ -e "$SCRATCH" ] || SCRATCH_REMOVED=yes
  else
    say "refusing to remove $SCRATCH: it carries no marker from this check"
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    RESTORE_DETAIL="$RESTORE_DETAIL; its scratch directory is gone"
  else
    RESTORE_DETAIL="the scratch directory $SCRATCH is STILL THERE — remove it yourself: rm -rf $SCRATCH"
  fi
}

finish() {
  local status=$?
  trap - EXIT
  local facts
  facts=$(cat "$FACTS" 2>/dev/null || true)
  cleanup
  if [ -n "$ABORT" ] && ! combine "$facts" aborted; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT"
  fi
  if [ "$REACHED_END" = no ] && ! combine "$facts" aborted; then
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): the rows that were not reached were NOT measured, which is this script ending on an error rather than an answer about the repository"
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ -n "$RESTORE_DETAIL" ]; then
    case "$RESTORE_DETAIL" in
    *STILL\ THERE*) facts="${facts}"$'\n'"restore|FAIL|$RESTORE_DETAIL" ;;
    *) facts="${facts}"$'\n'"restore|PASS|$RESTORE_DETAIL" ;;
    esac
  fi
  if [ "$REACHED_END" = yes ] && [ "$status" -eq 0 ]; then
    case "$(verdict "$facts")" in
    PASS) status=0 ;;
    FAIL) status=1 ;;
    *) status=3 ;;
    esac
  fi
  if [ "$QUIET" = yes ]; then exit "$status"; fi
  print_block "$facts" "$status"
  exit "$status"
}
trap finish EXIT

# ------------------------------------------------------------------ the reader

# py runs the one python3 program this check owns. It reads two documents and
# answers in four modes: `select` picks the checkout's own row out of
# `fugaro config list --json`, `valid` prints a `fugaro validate --json`
# document's verdict, `lifted` prints one line per lifted sentence it carries —
# the channel it is on (problem, note, warning), the key it names and whether it
# also carries a fix — and `msgs` prints a channel's messages flattened, for a
# row a person reads.
#
# A lifted sentence is recognised by its shape rather than by a list of keys
# this script would have to keep in step with `config.LiftedKeys`: the contract
# says each one opens with the key's own path, and every one of the five reads
# "... is the project's ..." (C1). So the key is what stands before that phrase,
# and the fix is the backticked command or project path that follows it.
py() { # py <mode> [args...]
  python3 - "$@" <<'PY'
import json, os, sys

MARK = " is the project's"

def die(code, msg):
    sys.stderr.write("check-912: %s\n" % msg)
    sys.exit(code)

def flat(text):
    return " ".join(str(text).replace("|", "/").split())

def load(path):
    try:
        with open(path) as fh:
            return json.load(fh)
    except Exception:
        return None

def lifted_sentence(row):
    """(key, has_fix) for a Problem whose message is a lifted key's sentence."""
    msg = flat(row.get("message") or "")
    i = msg.find(MARK)
    if i <= 0:
        return None
    key = msg[:i].strip()
    if not key or " " in key:
        return None
    rest = msg[i + len(MARK):].strip()
    return key, ("`" in rest and "`" in rest[rest.index("`") + 1:])

CHANNELS = ("problems", "notes", "warnings")

def mode_select(args):
    if len(args) < 3:
        die(2, "select needs <config.json> <project> <checkout>")
    config_path, project, checkout = args[0], args[1], args[2]
    doc = load(config_path)
    if not isinstance(doc, dict):
        die(3, "fugaro config list --json did not answer with a document")
    want = os.path.realpath(checkout)
    inst = None
    for i in doc.get("installations") or []:
        if i.get("name") == project:
            inst = i
            break
    if inst is None:
        names = [i.get("name") or "?" for i in doc.get("installations") or []]
        die(3, "fugaro config list --json lists no installation named %s (it lists: %s); run 'fugaro config list --json' yourself and name one of them" % (project, ", ".join(names) if names else "none"))
    seen = []
    for row in inst.get("checkouts") or []:
        path = row.get("path") or ""
        seen.append(path or "<no path>")
        if os.path.realpath(path) != want:
            continue
        if not row.get("enrolled"):
            die(3, "%s is a checkout of %s, and the project does not enroll that repository: enroll it (fugaro repos add) and run the check again" % (checkout, row.get("repository") or "?"))
        repo = row.get("repository") or ""
        parts = [p for p in repo.split("/") if p]
        if len(parts) < 3:
            die(2, "fugaro config list --json names %s, which is not an <owner>/<name> this check can read" % repo)
        base = row.get("base_branch") or inst.get("base_branch") or ""
        print("\t".join(("/".join(parts[1:]), base)))
        return 0
    die(3, "%s is not a checkout fugaro config list --json knows (it lists: %s); run 'fugaro setup' for it, or name the path it remembers" % (checkout, ", ".join(seen) if seen else "none"))

def mode_valid(args):
    if not args:
        die(2, "valid needs a validate document")
    doc = load(args[0])
    if not isinstance(doc, dict) or "valid" not in doc:
        die(3, "%s is not a `fugaro validate --json` document" % args[0])
    print("true" if doc.get("valid") else "false")
    return 0

def mode_lifted(args):
    if not args:
        die(2, "lifted needs a validate document")
    doc = load(args[0])
    if not isinstance(doc, dict) or "valid" not in doc:
        die(3, "%s is not a `fugaro validate --json` document" % args[0])
    for channel in CHANNELS:
        for row in doc.get(channel) or []:
            found = lifted_sentence(row)
            if found:
                print("\t".join((channel, found[0], "yes" if found[1] else "no")))
    return 0

def mode_msgs(args):
    if len(args) < 2:
        die(2, "msgs needs <validate document> <channel>")
    doc = load(args[0])
    if not isinstance(doc, dict) or "valid" not in doc:
        die(3, "%s is not a `fugaro validate --json` document" % args[0])
    out = []
    for row in doc.get(args[1]) or []:
        msg = flat(row.get("message") or "")
        path = flat(row.get("path") or "")
        out.append(("%s: %s" % (path, msg)) if path and not msg.startswith(path) else msg)
    print(flat("; ".join(out)))
    return 0

def main():
    if len(sys.argv) < 2:
        die(2, "no mode given")
    mode, args = sys.argv[1], sys.argv[2:]
    for name, fn in (("select", mode_select), ("valid", mode_valid),
                     ("lifted", mode_lifted), ("msgs", mode_msgs)):
        if mode == name:
            return fn(args)
    die(2, "unknown mode %s" % mode)

sys.exit(main())
PY
}

flat_sentence() { # flat_sentence <text>
  printf '%s' "$1" | tr '\n\t|' '   ' | tr -s ' '
}

first_lines() { # first_lines <file> <n>
  head -n "${2:-2}" "$1" 2>/dev/null | tr '\n' ' ' || true
}

# keys_of prints the keys of one channel of a validate document, sorted and
# unique, so two sides can be compared as sets rather than as orders.
keys_of() { # keys_of <doc> <channel>
  py lifted "$1" | awk -F'\t' -v c="$2" '$1 == c { print $2 }' | sort -u
}

count_of() { # count_of <doc> <channel>
  keys_of "$1" "$2" | grep -c . || true
}

# ------------------------------------------------------------------- the run

say "reading $PROJECT's view of $CHECKOUT (premise)"
fugaro config list --json >"$SCRATCH/config.json" 2>"$SCRATCH/config.err" </dev/null ||
  stop "fugaro config list --json did not answer: $(flat_sentence "$(first_lines "$SCRATCH/config.err")"). This check reads the checkout's owner/name and base branch with it; fix that read and run the check again"

SELECTED=$(py select "$SCRATCH/config.json" "$PROJECT" "$CHECKOUT" 2>"$SCRATCH/select.err") ||
  stop "$(flat_sentence "$(cat "$SCRATCH/select.err" 2>/dev/null)")"
REPO=$(printf '%s' "$SELECTED" | cut -f1)
BASE_BRANCH=$(printf '%s' "$SELECTED" | cut -f2)
[ -n "$REPO" ] || stop "fugaro config list --json answered without an owner/name for $CHECKOUT"
[ -n "$BASE_BRANCH" ] || stop "fugaro config list --json names no base branch for $REPO: the installation's config says which branch is its default, and this check reads it to know which ref to refuse at"
say "the checkout is $REPO, whose base branch is $BASE_BRANCH"
OLDER=${OLDER_REF:-origin/$BASE_BRANCH~1}

# BASE_REF is the ref the default branch is read at: the local branch where the
# checkout has one, and origin's where it does not — `git worktree add` makes
# the local branch from origin's, which is the one thing this check then has to
# take back (MADE_BASE_BRANCH).
BASE_REF=""
BASE_SHA=""
if git -C "$CHECKOUT" rev-parse --verify --quiet "refs/heads/$BASE_BRANCH^{commit}" >/dev/null 2>&1; then
  BASE_REF=$BASE_BRANCH
  BASE_SHA=$(git -C "$CHECKOUT" rev-parse "refs/heads/$BASE_BRANCH^{commit}")
elif git -C "$CHECKOUT" rev-parse --verify --quiet "origin/$BASE_BRANCH^{commit}" >/dev/null 2>&1; then
  BASE_REF=origin/$BASE_BRANCH
  BASE_SHA=$(git -C "$CHECKOUT" rev-parse "origin/$BASE_BRANCH^{commit}")
  MADE_BASE_BRANCH=yes
fi
OLDER_SHA=$(git -C "$CHECKOUT" rev-parse --verify --quiet "$OLDER^{commit}" 2>/dev/null || true)

if [ -z "$BASE_SHA" ]; then
  fact premise UNVERIFIED "the checkout $CHECKOUT carries no commit for the default branch $BASE_BRANCH, so this check cannot read the ref release B refuses at: fetch the checkout (git -C $CHECKOUT fetch origin) and run the check again"
elif [ -z "$OLDER_SHA" ]; then
  fact premise UNVERIFIED "the checkout carries no commit for the follow-up ref $OLDER, so the half that must be named and not refused cannot be read: fetch the checkout (git -C $CHECKOUT fetch origin), or name a ref it has with --older-ref"
else
  fact premise PASS "the checkout $CHECKOUT is $REPO, an enrolled repository of $PROJECT; its default branch $BASE_BRANCH is $BASE_SHA and the follow-up ref $OLDER is $OLDER_SHA (nothing is fetched)"
fi

if [ -n "$BASE_SHA" ] && [ -n "$OLDER_SHA" ]; then
  HELPER_BRANCH="chk912-followup-$PROJECT"
  if git -C "$CHECKOUT" rev-parse --verify --quiet "refs/heads/$HELPER_BRANCH" >/dev/null 2>&1; then
    stop "a local branch named $HELPER_BRANCH is already in $CHECKOUT, and this check makes its follow-up worktree with that name: delete it or rename it, then run the check again"
  fi
  [ -n "$(git -C "$CHECKOUT" rev-parse --verify --quiet "refs/heads/$BASE_BRANCH" 2>/dev/null || true)" ] || MADE_BASE_BRANCH=yes

  WT_DEFAULT=$SCRATCH/wt-default
  WT_OLDER=$SCRATCH/wt-followup
  say "adding two worktrees under $SCRATCH (the default branch, and $OLDER as the follow-up ref)"
  if ! git -C "$CHECKOUT" worktree add --force "$WT_DEFAULT" "$BASE_BRANCH" >"$SCRATCH/wt-default.log" 2>&1; then
    stop "git worktree add --force for the default branch $BASE_BRANCH failed: $(flat_sentence "$(first_lines "$SCRATCH/wt-default.log")"). This check reads the default branch through a worktree of your own checkout, so its objects have to be there: fetch it and run the check again"
  fi
  if ! git -C "$CHECKOUT" worktree add -b "$HELPER_BRANCH" "$WT_OLDER" "$OLDER" >"$SCRATCH/wt-followup.log" 2>&1; then
    stop "git worktree add at $OLDER failed: $(flat_sentence "$(first_lines "$SCRATCH/wt-followup.log")")"
  fi

  DEFAULT_RC=0
  (cd "$WT_DEFAULT" && fugaro validate --json </dev/null) >"$SCRATCH/default.json" 2>"$SCRATCH/default.err" || DEFAULT_RC=$?
  OLDER_RC=0
  (cd "$WT_OLDER" && fugaro validate --json </dev/null) >"$SCRATCH/older.json" 2>"$SCRATCH/older.err" || OLDER_RC=$?

  for side in default older; do
    file=$SCRATCH/$side.json
    case "$side" in
    default) rc=$DEFAULT_RC ;;
    older) rc=$OLDER_RC ;;
    esac
    if [ ! -s "$file" ] || ! py valid "$file" >"$SCRATCH/$side.valid" 2>"$SCRATCH/$side.readerr"; then
      stop "fugaro validate --json did not answer in the $side worktree (exit $rc): $(flat_sentence "$(first_lines "$SCRATCH/$side.err")")"
    fi
  done

  DEFAULT_VALID=$(cat "$SCRATCH/default.valid")
  OLDER_VALID=$(cat "$SCRATCH/older.valid")
  DEFAULT_PROBLEM_KEYS=$(keys_of "$SCRATCH/default.json" problems)
  OLDER_PROBLEM_KEYS=$(keys_of "$SCRATCH/older.json" problems)
  OLDER_NOTE_KEYS=$(keys_of "$SCRATCH/older.json" notes)
  NOFIX=$(py lifted "$SCRATCH/default.json" | awk -F'\t' '$1 == "problems" && $3 == "no" { print $2 }' | sort -u)
  OLDER_NOFIX=$(py lifted "$SCRATCH/older.json" | awk -F'\t' '$1 == "notes" && $3 == "no" { print $2 }' | sort -u)

  # The two refs usually carry the same fugaro.yaml: the migration drops a
  # lifted key in the resolver and never rewrites the repository's file, so the
  # owner's own removal is what takes it out, and the tip and its parent still
  # read the same document. Where they do, the two halves have to name the same
  # keys, which is what makes this a measurement of the mode and not of two
  # documents that happen to differ.
  SAME_DOC=no
  if git -C "$CHECKOUT" show "$BASE_REF:fugaro.yaml" >"$SCRATCH/default.yaml" 2>/dev/null &&
    git -C "$CHECKOUT" show "$OLDER:fugaro.yaml" >"$SCRATCH/older.yaml" 2>/dev/null &&
    cmp -s "$SCRATCH/default.yaml" "$SCRATCH/older.yaml"; then
    SAME_DOC=yes
  fi

  if [ "$DEFAULT_VALID" != false ] || [ "$DEFAULT_RC" = 0 ]; then
    fact default FAIL "fugaro validate on the default branch ($BASE_BRANCH, in a worktree of $CHECKOUT) did not refuse: it exited $DEFAULT_RC with valid=$DEFAULT_VALID. Release B refuses a lifted key its file writes, with the fix (the sentence C1 pins), so either this binary is not release B — 'fugaro --version' says $(fugaro --version </dev/null 2>/dev/null | head -n1) — or it stopped refusing: install release B (brew upgrade dimipaun/tap/fugaro), or reconcile the binary you have. What it printed: $(py msgs "$SCRATCH/default.json" problems)$(py msgs "$SCRATCH/default.json" notes)"
  elif [ -z "$DEFAULT_PROBLEM_KEYS" ]; then
    fact default FAIL "fugaro validate refused the default branch ($BASE_BRANCH) and named no lifted key: no problem carries a sentence of the shape C1 pins ('<key> is the project's ...'), so what it refused on is not the lift. What it printed: $(py msgs "$SCRATCH/default.json" problems)"
  elif [ -n "$NOFIX" ]; then
    fact default FAIL "fugaro validate refused $(printf '%s' "$DEFAULT_PROBLEM_KEYS" | tr '\n' ' ')on the default branch ($BASE_BRANCH), and the sentence of $(printf '%s' "$NOFIX" | tr '\n' ' ')carries no fix: release B refuses with the sentence that says where the key lives now — the command or the project path that states it (C1). What it printed: $(py msgs "$SCRATCH/default.json" problems)"
  else
    fact default PASS "fugaro validate in a checkout ON the default branch $BASE_BRANCH ($BASE_SHA) refused: valid=false, exit $DEFAULT_RC, and each of the $(count_of "$SCRATCH/default.json" problems) lifted key(s) it writes — $(printf '%s' "$DEFAULT_PROBLEM_KEYS" | tr '\n' ' ')— is a problem whose sentence names the key and the command or the project path that states it now"
  fi

  if [ -n "$OLDER_PROBLEM_KEYS" ]; then
    fact older FAIL "fugaro validate refused $(printf '%s' "$OLDER_PROBLEM_KEYS" | tr '\n' ' ')in a checkout at the follow-up ref $OLDER ($OLDER_SHA): release B refuses a lifted key on the default branch alone, and a follow-up's base — a run cut from a branch older than the migration — must be named and not refused (XB1, XB6). What it printed: $(py msgs "$SCRATCH/older.json" problems)"
  elif [ -z "$OLDER_NOTE_KEYS" ]; then
    fact older FAIL "fugaro validate at the follow-up ref $OLDER ($OLDER_SHA) named no lifted key at all: it neither refused nor named one, so the file it read writes no lifted key, or the binary no longer names them. What it printed: $(py msgs "$SCRATCH/older.json" notes)"
  elif [ -n "$OLDER_NOFIX" ]; then
    fact older FAIL "fugaro validate at the follow-up ref $OLDER ($OLDER_SHA) named $(printf '%s' "$OLDER_NOFIX" | tr '\n' ' ')as notes carrying no fix, which is the sentence C1 pins minus the half that says where the key lives now. What it printed: $(py msgs "$SCRATCH/older.json" notes)"
  elif [ "$OLDER_VALID" != true ] || [ "$OLDER_RC" != 0 ]; then
    fact older FAIL "fugaro validate at the follow-up ref $OLDER ($OLDER_SHA) named the lifted keys as notes but did not come out clean: valid=$OLDER_VALID, exit $OLDER_RC. It refuses nothing there, so the file has to be valid and the command exit 0. What it printed: $(py msgs "$SCRATCH/older.json" problems)"
  elif [ "$SAME_DOC" = yes ] && [ "$(printf '%s' "$DEFAULT_PROBLEM_KEYS")" != "$(printf '%s' "$OLDER_NOTE_KEYS")" ]; then
    fact older FAIL "the two refs carry the same fugaro.yaml and the two halves do not name the same keys: the default branch refuses $(printf '%s' "$DEFAULT_PROBLEM_KEYS" | tr '\n' ' ')and the follow-up ref names $(printf '%s' "$OLDER_NOTE_KEYS" | tr '\n' ' '). One document, read at two refs, is what this check measures: the mode must follow the ref, not the file"
  else
    if [ "$SAME_DOC" = yes ]; then
      SAME="; both refs carry the same fugaro.yaml, and the $(count_of "$SCRATCH/older.json" notes) key(s) it writes are named on this side exactly as the default branch refuses them"
    else
      SAME="; the two refs' fugaro.yaml differ, so the keys of the two sides are compared each to its own document and not to each other"
    fi
    fact older PASS "fugaro validate in a checkout at the follow-up ref $OLDER ($OLDER_SHA) refused nothing: valid=true, exit $OLDER_RC, and the $(count_of "$SCRATCH/older.json" notes) lifted key(s) it writes — $(printf '%s' "$OLDER_NOTE_KEYS" | tr '\n' ' ')— are notes naming the key and the fix$SAME"
  fi

  say "reading .fugaro/recipes/ at the default branch (recipes)"
  if ! git -C "$CHECKOUT" ls-tree -r --name-only "$BASE_REF" -- .fugaro/recipes >"$SCRATCH/recipes.list" 2>"$SCRATCH/recipes.err"; then
    fact recipes UNVERIFIED "the .fugaro/recipes/ folder of $BASE_BRANCH could not be listed: $(flat_sentence "$(first_lines "$SCRATCH/recipes.err")")"
  elif [ ! -s "$SCRATCH/recipes.list" ]; then
    fact recipes PASS "$BASE_BRANCH holds no .fugaro/recipes/, so there is no cleanup diff to refuse and nothing this criterion had to measure (the lifted keys above are the whole of the refusal here)"
  else
    FILES=$(grep -c . "$SCRATCH/recipes.list" || true)
    if case "$(py msgs "$SCRATCH/default.json" problems) $(py msgs "$SCRATCH/default.json" warnings) $(py msgs "$SCRATCH/default.json" notes)" in
    *"git rm -r .fugaro/recipes"* | *"fugaro recipes publish"*) true ;;
    *) false ;;
    esac; then
      fact recipes PASS "$BASE_BRANCH holds .fugaro/recipes/ ($FILES file(s)) and refusing it carries PR5's cleanup line — the diff git rm -r .fugaro/recipes where every copy is stored, or fugaro recipes publish <file> first where one is not. What it printed: $(py msgs "$SCRATCH/default.json" notes)$(py msgs "$SCRATCH/default.json" warnings)$(py msgs "$SCRATCH/default.json" problems)"
    else
      fact recipes FAIL "$BASE_BRANCH holds .fugaro/recipes/ ($FILES file(s)) and the refusal carries no cleanup line: release B refuses the folder with the fix PR5 pins — the diff git rm -r .fugaro/recipes where every copy is stored, or fugaro recipes publish <file> first where one is not — and nothing it printed names either. What it printed: $(py msgs "$SCRATCH/default.json" problems)"
    fi
  fi
fi

say "reading the update's own plan (update, writes)"
UPDATE_RC=0
fugaro update --plan-only --project "$PROJECT" </dev/null >"$SCRATCH/plan.out" 2>"$SCRATCH/plan.err" || UPDATE_RC=$?
if [ "$UPDATE_RC" != 0 ]; then
  fact update UNVERIFIED "fugaro update --plan-only --project $PROJECT exited $UPDATE_RC: $(flat_sentence "$(first_lines "$SCRATCH/plan.err")"). Its lines are the update's own naming of every change it would make, before any write, and release B names this repository there: without them the update's half of the refusal is not measured. Fix what it refused and run the check again"
else
  # The line has to LEAD with the repository: `acme/app-console` is not
  # `acme/app`, and a line about another repository names nothing here.
  awk -v r="$REPO" 'substr($0, 1, length(r)) == r { c = substr($0, length(r) + 1, 1); if (c == "" || c == ":" || c == " " || c == "\t") print }' \
    "$SCRATCH/plan.out" >"$SCRATCH/plan-repo.out" 2>/dev/null || true
  if [ ! -s "$SCRATCH/plan-repo.out" ]; then
    fact update FAIL "fugaro update --plan-only exited 0 and names $REPO nowhere: this repository's default branch still writes a lifted key (or holds .fugaro/recipes/), and release B names every listed repository that does, with the fix and PR5's lines, before any write (XB5). Its lines: $(flat_sentence "$(first_lines "$SCRATCH/plan.out" 4)")"
  elif ! grep -qE "is the project's|git rm -r \.fugaro/recipes|fugaro recipes publish" "$SCRATCH/plan-repo.out"; then
    fact update FAIL "fugaro update --plan-only names $REPO and carries neither a lifted key's sentence nor PR5's cleanup line: $(flat_sentence "$(head -n 3 "$SCRATCH/plan-repo.out")"). The line about this repository has to say what is wrong with it and the fix"
  else
    fact update PASS "fugaro update --plan-only --project $PROJECT printed $(grep -c . "$SCRATCH/plan.out" || true) line(s) and names $REPO with the fix: $(flat_sentence "$(head -n 2 "$SCRATCH/plan-repo.out")"). It applies nothing — --plan-only writes no file, in the installation, in a repository or in the cloud — so the migration's half of release B is named here before anything runs"
  fi
fi

fact writes PASS "no run was launched and no image was built: this check ran 'fugaro config list --json' once, 'fugaro validate --json' once per worktree, and 'fugaro update --plan-only' once, each with stdin closed, each read-only. The two worktrees under $SCRATCH are the only thing it added to the repository — the restore row says whether they went — and no secret is read at all"

REACHED_END=yes
