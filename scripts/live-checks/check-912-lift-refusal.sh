#!/usr/bin/env bash
# Check 912 — after release B, `fugaro validate` refuses a lifted key on the
# default branch with its fix, names the same key on a follow-up ref instead of
# refusing it, and `fugaro update --plan-only` names it too, with the same fix,
# and converges everything else. Run by YOU, with the release-B binary installed.
#
# L8 (docs/repo-override-rulings) moves a repository's model choice, its A/B
# rate, its rigor level and areas and its model grades into the project layer.
# Release A drops those keys in the resolver and names them (its acceptance 3);
# release B is the release that refuses them on the default branch — at
# `validate` and at a launch (its acceptance 4, XB1/XB2/XB6) — and that names
# them, with the fix, at `update` (XB5's own arm, the whole update converging)
# and on an older ref, where a follow-up still carries them.
# The sentence is `config.LiftedKey.Why`, one per key, and it names the command
# that states the key in its new home; release A's own reads still pass, so this
# check tells release B from release A by what the refusal does.
#
# Nothing offline can settle it: the refusal is what a real installation's
# binary prints about a real repository's file. One row per criterion:
#
#   premise  the checkout is a git checkout whose default branch carries a
#            fugaro.yaml that writes a lifted key — the state release B is built
#            to refuse. Where the file writes none, the smallest model key is
#            written into this check's own copy of that tree, and the row says
#            so.
#   refuse   `fugaro validate` on that tree, on the default branch, is REFUSED
#            (a non-zero exit) and the refusal carries the key's own path and the
#            fix — a refusal that names no fix, or that answers for some other
#            reason, is not the refusal this check is about.
#   name     the same tree on a non-default ref, the shape of a follow-up's
#            branch, is NOT refused (exit 0) and the key is named anyway: a
#            follow-up cut before the cleanup must not be stopped by it.
#   plan     `fugaro update --plan-only --project <project>` NAMES the key with
#            the same sentence, before any write, and converges everything else:
#            acceptance 4's update arm is read under the HARD RULE as named,
#            never as a stop of the whole update. A plan that never names the key
#            is a FAIL, one that names it without a fix is a FAIL, and one that
#            stops over it is a FAIL. No launch is made, and no other update form
#            is run. `update` reads the repositories a project enrolls at their
#            default branches through the
#            git provider and never a working directory (internal/repofiles), so
#            this row is measured only where the repository's own default branch
#            writes the key; where the check had to write one into its copy, the
#            row is UNVERIFIED rather than an accusation about a file `update`
#            never read.
#   writes   the checkout, its HEAD, its branch and its file are exactly what
#            they were: the two `validate` reads happen in a copy of the default
#            branch's tree inside its own scratch directory, and the owner's
#            checkout is never switched, never written.
#   restore  the scratch directory is gone from the exit trap.
#
# Every judging is of the text the real binary printed: this check holds no
# release of its own, and it reads no secret and prints none.
#
# Usage:   bash scripts/live-checks/check-912-lift-refusal.sh <project> <checkout> [--dry-run]
#
#          <checkout> is any checkout of a repository the project enrolls, on
#          this machine. Its default branch's tree is copied into a scratch
#          directory and the two `validate` reads happen there; the copy's
#          `origin` is the checkout's own remote URL, so the repository a read is
#          about is the same one. The checkout itself is not touched.
#
#          --dry-run   prints the plan and calls NOTHING.
# Needs:   bash, mktemp, python3, git, tar, and a release-B `fugaro` on PATH
#          (CHECK912_FUGARO names another binary).
# Where:   the checkout you name and the project's local config, read; the
#          project's cloud objects as `fugaro validate` reads them. No run is
#          launched and nothing is built.
# Costs:   nothing.
# Runs:    by YOU. No agent runs any step of it — it refuses to start in a
#          coding agent's session (CLAUDECODE, CLAUDE_CODE_ENTRYPOINT,
#          CLAUDE_CODE_REMOTE, CURSOR_AGENT, AI_AGENT) and refuses a shell
#          started with -x. Its own test
#          (scripts/live-checks/check-912-lift-refusal_test.sh) drives it against
#          a fake `fugaro` on PATH and real git checkouts, and touches no cloud.
# Cleanup: the scratch directory, and the copy of the tree in it, are removed
#          from the exit trap; the `restore` row says whether it really went.
# Output:  progress on stderr, ONE block on stdout headed CHECK 912 RESULT, to
#          copy back. Its last line is the verdict, and the verdict is the exit
#          status:
#
#            CHECK 912 RESULT: PASS (exit 0)        every criterion held;
#            CHECK 912 RESULT: FAIL (exit 1)        one of them did not — on a
#                                                   release-A binary the `refuse`
#                                                   row is the one that fails;
#            CHECK 912 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                   could judge;
#            CHECK 912 RESULT: INCOMPLETE (exit 3)  it judged, and a read it
#                                                   needed did not answer
#                                                   (UNVERIFIED).
set -euo pipefail

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }

XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null

# The shared YAML reader (lib/lift_yaml.py), which this check and Check 910 both
# read a fugaro.yaml with. Exported, because the python3 program below is a
# heredoc on stdin and finds the module through it.
LIVE_CHECK_LIB=${LIVE_CHECK_LIB:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib}
export LIVE_CHECK_LIB

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
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 912: it reads a real repository's file and a real installation's refusals with the owner's binary. Run it yourself: bash scripts/live-checks/check-912-lift-refusal.sh <project> <checkout>"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x), and its trace would print the arguments of every command this check runs. Run it without -x: bash scripts/live-checks/check-912-lift-refusal.sh <project> <checkout>"
  fi
}

usage() {
  cat <<'USAGE'
Check 912 — release B refuses a lifted key on the default branch with its fix,
names it on a follow-up ref instead of refusing it, and names it, with the same
fix, in the plan `fugaro update --plan-only` prints, converging everything else.

Usage:
  bash scripts/live-checks/check-912-lift-refusal.sh <project> <checkout> [--dry-run]
  bash scripts/live-checks/check-912-lift-refusal.sh --help

Run it after release B is installed, with a checkout of any repository the
project enrolls: `fugaro validate` is run twice on a copy of that checkout's
default branch — once on the default branch, where the key must be REFUSED with
the fix, and once on a non-default ref, where it must be NAMED and not refused —
and `fugaro update --plan-only` must name it with the same fix. The block's last
line is the verdict and the exit status:

  PASS (exit 0)        the key is refused with its fix, named on the other ref
                       and named by the plan
  FAIL (exit 1)        one of the three did not hold — on a release-A binary
                       the `refuse` row fails, and the `plan` row with it where
                       that release's own update has not migrated the repository
  INCOMPLETE (exit 2)  the run stopped before it could judge
  INCOMPLETE (exit 3)  at least one row reads UNVERIFIED
USAGE
}

PROJECT=""
CHECKOUT=""
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
    stop "unknown argument $1 (usage: check-912-lift-refusal.sh <project> <checkout> [--dry-run], or --help)"
    ;;
  *)
    if [ -z "$PROJECT" ]; then
      PROJECT=$1
    elif [ -z "$CHECKOUT" ]; then
      CHECKOUT=$1
    else
      stop "two arguments only, a project and a checkout: $PROJECT and $CHECKOUT were already given"
    fi
    shift
    ;;
  esac
done

FUGARO_BIN=${CHECK912_FUGARO:-fugaro}
FOLLOWUP=${CHECK912_BRANCH:-check912-followup}
CONFIG_HOME=${XDG_CONFIG_HOME:-${HOME:-}/.config}
PROJECT_FILE=$CONFIG_HOME/fugaro/projects/${PROJECT:-<project>}.yaml

dry_run_plan() {
  cat <<PLAN
Check 912 plan (nothing is called: no fugaro, no git, no read)

  project:       ${PROJECT:-<not given>}
  checkout:      ${CHECKOUT:-<not given>}
  fugaro:        $FUGARO_BIN — the release-B binary, whose refusal is the thing
                 this check reads
  follow-up ref: $FOLLOWUP
  project file:  $PROJECT_FILE

1. read the checkout's default branch (its origin/HEAD, else its own branch) and
   copy that branch's tree — git archive into this check's scratch directory —
   then give the copy the checkout's own origin URL and refs/remotes/origin/HEAD,
   so the repository a read is about is the same one. The checkout is not
   switched and not written.
2. read the lifted keys the copied fugaro.yaml writes. Where it writes none, the
   smallest model key is written into this check's own copy and the premise row
   says so, because the state release B refuses is a file that still writes one.
3. run, in the copy:
     fugaro validate <copy>/fugaro.yaml   on the default branch
                                          -> must be REFUSED, with the fix
     fugaro validate <copy>/fugaro.yaml   on $FOLLOWUP
                                          -> must be NAMED, not refused
     fugaro update --plan-only --project $PROJECT
                                          -> must NAME the lifted key with the
                                             fix, and converge the rest
   and judge each answer's own text: the key's path and a fix for it. validate
   takes the fugaro.yaml and not its directory. update reads the default branches
   of the repositories the project enrolls through the git provider and never a
   working directory, so where step 2 had to write the key into the copy the plan
   read is UNVERIFIED and no update is run.

No launch is made, no image is built, and no other update form is run. The two
validate reads happen in the copy: this check writes NOTHING outside its scratch
directory, and the restore row says the copy went with it.
PLAN
}

if [ "$DRY_RUN" = yes ]; then
  guard_xtrace
  guard_agent
  dry_run_plan
  exit 0
fi

[ -n "$PROJECT" ] ||
  stop "no <project> was given: name the installation whose binary is release B, e.g. check-912-lift-refusal.sh fugaro <a checkout>"
[ -n "$CHECKOUT" ] ||
  stop "no <checkout> was given: name a checkout of a repository the project enrolls, e.g. check-912-lift-refusal.sh $PROJECT ~/src/<owner>/<name>"
case "$CHECKOUT" in
/*) ;;
*) CHECKOUT="$PWD/$CHECKOUT" ;;
esac

guard_xtrace
guard_agent
need bash
need mktemp
need python3
need git
need tar
need "$FUGARO_BIN"

[ -d "$CHECKOUT" ] ||
  stop "$CHECKOUT is not a directory: name a checkout of a repository the project enrolls"
git -C "$CHECKOUT" rev-parse --git-dir >/dev/null 2>&1 ||
  stop "$CHECKOUT is not a git checkout, and this check reads the default branch's own tree with git: name a clone of the repository (fugaro setup records the checkout it made)"
[ -f "$PROJECT_FILE" ] ||
  stop "$PROJECT_FILE is not there: this check reads the project's own repositories through the local config fugaro setup writes, and no local config names a project $PROJECT"

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk912.XXXXXXXX") || stop "cannot make a scratch directory"
chmod 700 "$SCRATCH"
FACTS="$SCRATCH/facts"
: >"$FACTS"
MARKER="$SCRATCH/.chk912-mine"
: >"$MARKER"
CLONE="$SCRATCH/clone"

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
  printf '\nCHECK 912 RESULT\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-14s %-11s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 912 RESULT: %s (exit %s)\n' "$(verdict "$facts")" "$status"
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
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): the rows that were not reached were NOT measured, which is this script ending on an error rather than an answer about release B"
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|the scratch directory is gone, and with it the copy of the default branch's tree the two \`validate\` reads were run on. Nothing outside it was written: the checkout, its HEAD, its branch and its file are the ones the writes row read back"
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

# py runs the one python3 program this check owns. It reads a repository's own
# fugaro.yaml as text (the same reading Check 910 makes: nested mappings, a
# key's block by its indentation) and judges the text a real binary printed.
# `keys` lists the lifted keys the file writes, `add` writes the smallest one
# into this check's own copy of the tree, `hash` says whether a file moved, and
# `refuse`, `name` and `plan` turn one read's own answer and exit status into a
# row. Every judgement is over whitespace-flattened text, because a refusal the
# terminal wrapped is the same sentence.
py() { # py <mode> [args...]
  # -B because the shared reader is imported: a bytecode cache written beside it
  # would be a file this check put in the repository it reads.
  python3 -B - "$@" <<'PY'
import hashlib, os, sys

# The fix each sentence names, one per key of config.LiftedKeys (C1): the command
# that states the key in its new home. A refusal that names no fix for the key it
# refuses is not the refusal release B prints.
FIXES = ("fugaro models use --layer project", "ab.repos.",
         "fugaro rigor set", "defaults.model_grades.")

FIXED = [("ab", "rate"), ("agent", "model"), ("rigor", "default"), ("rigor", "areas"),
         ("model_grades", "strong", "model"), ("model_grades", "strongest", "model")]

INJECT_KEY = "agent.models.coder"

def die(code, msg):
    sys.stderr.write("check-912: %s\n" % msg)
    sys.exit(code)

# The YAML block reader, `flat`, `read_text` and the path walk are Check 910's
# too, so both read a file of this shape the same way:
# scripts/live-checks/lib/lift_yaml.py, put on the path by the
# LIVE_CHECK_LIB this script exports.
lib = os.environ.get("LIVE_CHECK_LIB", "")
if lib not in sys.path:
    sys.path.insert(0, lib)
try:
    from lift_yaml import block_spans, children, cut_comment, flat, read_text
except ImportError:
    die(2, "the shared YAML reader lib/lift_yaml.py was not found (LIVE_CHECK_LIB is %r): run this check from scripts/live-checks/" % lib)

def window(text, key, around=360):
    i = text.find(key)
    if i < 0:
        return text[:around]
    return text[max(0, i - 90):i + around].strip()

def lifted_paths(spans):
    paths = list(FIXED)
    for c in children(spans, ("agent", "models")):
        paths.append(c)
    for c in children(spans, ("agent", "crafts")):
        paths.append(c + ("model",))
    for c in children(spans, ("agent", "levels")):
        paths.append(c + ("model",))
    return [p for p in paths if p in spans]

# ------------------------------------------------------------- the modes

def mode_keys(args):
    if not args:
        die(2, "keys needs a fugaro.yaml")
    text = read_text(args[0])
    spans = block_spans(text.splitlines())
    for path in lifted_paths(spans):
        print(".".join(path))
    return 0

def mode_add(args):
    """Writes the smallest lifted key into a copy of a file that writes none."""
    if not args:
        die(2, "add needs a fugaro.yaml")
    path = args[0]
    value = args[1] if len(args) > 1 else "claude-sonnet-5"
    lines = read_text(path).splitlines()
    if not lines:
        die(3, "%s is empty, and a fugaro.yaml this check can add a key to is one it can read" % path)
    top, indent_of = None, None
    for i, raw in enumerate(lines):
        line = cut_comment(raw)
        if line.strip() == "agent:" and line == line.lstrip(" \t"):
            top, indent_of = i, 0
            break
    if top is None:
        lines = lines + ["", "agent:", "  models:", "    coder: " + value]
    else:
        models = None
        for j in range(top + 1, len(lines)):
            line = cut_comment(lines[j])
            if not line.strip():
                continue
            indent = len(line) - len(line.lstrip(" \t"))
            if indent <= indent_of:
                break
            if line.strip() == "models:":
                models = (j, indent)
                break
        if models is None:
            lines = (lines[:top + 1] +
                     [" " * (indent_of + 2) + "models:", " " * (indent_of + 4) + "coder: " + value] +
                     lines[top + 1:])
        else:
            j, indent = models
            lines = lines[:j + 1] + [" " * (indent + 2) + "coder: " + value] + lines[j + 1:]
    tmp = path + ".check912"
    with open(tmp, "w") as fh:
        fh.write("\n".join(lines) + "\n")
    os.replace(tmp, path)
    sys.stdout.write(INJECT_KEY + "\n")
    return 0

def mode_hash(args):
    if not args:
        die(2, "hash needs a file")
    data = read_text(args[0])
    if not data and not os.path.exists(args[0]):
        print("<absent>")
        return 0
    print("sha256:" + hashlib.sha256(data.encode("utf-8", "replace")).hexdigest())
    return 0

def judge(args, kind):
    if len(args) < 3:
        die(2, "%s needs <answer file> <key> <exit status>" % kind)
    answer, key, rc = flat(read_text(args[0])), args[1], args[2]
    named, fixed = key in answer, any(f in answer for f in FIXES)
    if kind == "refuse":
        if rc == "0":
            return "FAIL", "fugaro validate ACCEPTED a file that still writes %s on the default branch, and release B is the release that refuses it there: the repository keeps a value L8 moved into the project, and nothing tells its owner to remove it" % key
        if named and fixed:
            return "PASS", "fugaro validate refused it (exit %s) and the refusal names the key and its fix: %s" % (rc, window(answer, key))
        if named:
            return "FAIL", "fugaro validate refused %s (exit %s) but the refusal names no fix for it — not one of the commands that state it in its new home — so its owner is told a key moved and not what to do: %s" % (key, rc, window(answer, key))
        return "UNVERIFIED", "fugaro validate exited %s, and its answer does not name %s at all, so what it refused is something else and this row measures nothing: %s" % (rc, key, answer[:360])
    if kind == "name":
        if rc != "0":
            return "FAIL", "on the ref %s fugaro validate exited %s: release B refuses a lifted key on the default branch and only NAMES it on another ref, so a follow-up cut before the cleanup is stopped by a key its own file was never asked to drop: %s" % (args[3] if len(args) > 3 else "the follow-up", rc, window(answer, key))
        if named:
            return "PASS", "on the ref %s fugaro validate exited 0 and named the key anyway, which is what a follow-up of a repository that has not cleaned its file must get: %s" % (args[3] if len(args) > 3 else "the follow-up", window(answer, key))
        return "FAIL", "fugaro validate exited 0 on the ref and named NOTHING: a file that still writes %s is one release B must tell its owner about even where it does not refuse it, and a follow-up that never learns the key moved is a repository nobody cleans" % key
    if rc != "0" and not named:
        return "UNVERIFIED", "fugaro update --plan-only exited %s and its answer does not name %s, so it stopped for another reason and this row measures nothing: %s" % (rc, key, answer[:360])
    if not named:
        return "FAIL", "fugaro update --plan-only planned without naming %s: release B names every repository whose default branch still writes a lifted key, with the fix, and converges everything else, so a plan that never names it leaves the owner of a value the project decides with nothing to act on" % key
    if not fixed:
        return "FAIL", "fugaro update --plan-only named %s but no fix for it — not one of the commands that states it in its new home — so its owner is told a key moved and not what to do: %s" % (key, window(answer, key))
    if rc != "0":
        return "FAIL", "fugaro update --plan-only named %s and then exited %s: acceptance 4's update arm is read under the HARD RULE as named, never as a stop of the whole update, so an update that stops over a key it was to name stops on a value the project decides: %s" % (key, rc, window(answer, key))
    return "PASS", "fugaro update --plan-only named it with the fix and converged everything else (exit 0), before any write: %s" % window(answer, key)

def main():
    if len(sys.argv) < 2:
        die(2, "no mode given")
    mode, args = sys.argv[1], sys.argv[2:]
    if mode == "keys":
        return mode_keys(args)
    if mode == "add":
        return mode_add(args)
    if mode == "hash":
        return mode_hash(args)
    if mode in ("refuse", "name", "plan"):
        state, detail = judge(args, mode)
        print("%s|%s" % (state, detail))
        return 0
    die(2, "unknown mode %s" % mode)

sys.exit(main())
PY
}

flat_sentence() { # flat_sentence <text>
  printf '%s' "$1" | tr '\n\t|' '   ' | tr -s ' '
}

# ------------------------------------------------------------------- the run

DEFAULT=$(git -C "$CHECKOUT" symbolic-ref --short refs/remotes/origin/HEAD 2>/dev/null || true)
DEFAULT=${DEFAULT#origin/}
SOURCE="the checkout's origin/HEAD"
if [ -z "$DEFAULT" ]; then
  DEFAULT=$(git -C "$CHECKOUT" rev-parse --abbrev-ref HEAD 2>/dev/null || true)
  SOURCE="the checkout's own branch, its origin/HEAD not being there"
fi
[ -n "$DEFAULT" ] && [ "$DEFAULT" != HEAD ] ||
  stop "the checkout at $CHECKOUT names no default branch (its origin/HEAD is not set and it is on no branch): fetch it (git -C $CHECKOUT fetch origin) and run the check again"

REF="origin/$DEFAULT"
if ! git -C "$CHECKOUT" rev-parse --verify --quiet "refs/remotes/origin/$DEFAULT" >/dev/null 2>&1; then
  if git -C "$CHECKOUT" rev-parse --verify --quiet "refs/heads/$DEFAULT" >/dev/null 2>&1; then
    REF="$DEFAULT"
  else
    REF=HEAD
  fi
fi
say "copying $REF of $CHECKOUT into the scratch directory (premise)"

git -C "$CHECKOUT" archive --format=tar "$REF" -- fugaro.yaml .fugaro >"$SCRATCH/tree.tar" 2>"$SCRATCH/tree.err" || {
  git -C "$CHECKOUT" archive --format=tar "$REF" >"$SCRATCH/tree.tar" 2>>"$SCRATCH/tree.err" || true
}
[ -s "$SCRATCH/tree.tar" ] || {
  fact premise UNVERIFIED "git archive $REF of $CHECKOUT read nothing (git said: $(flat_sentence "$(head -c 200 "$SCRATCH/tree.err" 2>/dev/null || true)")), and the default branch's own tree is what release B refuses a key in. Fetch the checkout and run the check again"
  REACHED_END=yes
  exit 0
}
mkdir -p "$CLONE"
tar -xf "$SCRATCH/tree.tar" -C "$CLONE" 2>"$SCRATCH/untar.err" || {
  fact premise UNVERIFIED "the tree of $REF could not be unpacked into the scratch directory ($(flat_sentence "$(head -c 200 "$SCRATCH/untar.err" 2>/dev/null || true)"))"
  REACHED_END=yes
  exit 0
}

git -C "$CLONE" init -q
git -C "$CLONE" config user.email check912@example.invalid
git -C "$CLONE" config user.name check912
git -C "$CLONE" config commit.gpgsign false
ORIGIN_URL=$(git -C "$CHECKOUT" remote get-url origin 2>/dev/null || true)
if [ -z "$ORIGIN_URL" ]; then
  ORIGIN_URL_NOTE="and this checkout names no origin remote, so the copy's reads resolve no repository of their own"
else
  ORIGIN_URL_NOTE="with the checkout's own origin remote, whose URL this block never prints (an origin can carry a token, and this block is what you copy back)"
  git -C "$CLONE" remote add origin "$ORIGIN_URL"
fi

if [ ! -f "$CLONE/fugaro.yaml" ]; then
  fact premise UNVERIFIED "$REF of $CHECKOUT carries no fugaro.yaml: a repository the project enrolls whose default branch has no file of its own to read is one this check cannot read a lifted key in, and release B has nothing to refuse here"
  REACHED_END=yes
  exit 0
fi

KEYS=$(py keys "$CLONE/fugaro.yaml" 2>"$SCRATCH/keys.err" || true)
ADDED=no
if [ -z "$KEYS" ]; then
  KEY=$(py add "$CLONE/fugaro.yaml" 2>"$SCRATCH/add.err") || {
    fact premise UNVERIFIED "the fugaro.yaml of $REF writes no lifted key, and the smallest model key could not be written into this check's own copy of it ($(flat_sentence "$(head -c 200 "$SCRATCH/add.err" 2>/dev/null || true)")): name a checkout whose file still writes a lifted key, which is the state release B refuses"
    REACHED_END=yes
    exit 0
  }
  ADDED=yes
  ORIGIN_NOTE="none of the lifted keys (the file writes none), so $KEY was written into this check's own copy of the tree; the default branch itself is untouched"
else
  KEY=$(printf '%s\n' "$KEYS" | head -n1)
  ORIGIN_NOTE="the key its own file writes (it writes $(printf '%s\n' "$KEYS" | tr '\n' ' ' | sed 's/ $//'))"
fi

say "the key judged is $KEY ($ORIGIN_NOTE)"
if [ "$ADDED" = yes ]; then
  git -C "$CLONE" add -A
  git -C "$CLONE" commit -qm "Check 912: the default branch's tree, with one lifted key written into it"
else
  git -C "$CLONE" add -A
  git -C "$CLONE" commit -qm "Check 912: the default branch's tree"
fi
git -C "$CLONE" update-ref "refs/remotes/origin/$DEFAULT" "$(git -C "$CLONE" rev-parse HEAD)"
git -C "$CLONE" symbolic-ref "refs/remotes/origin/HEAD" "refs/remotes/origin/$DEFAULT"
git -C "$CLONE" checkout -q -B "$DEFAULT"

BEFORE_HEAD=$(git -C "$CHECKOUT" rev-parse HEAD 2>/dev/null || true)
BEFORE_BRANCH=$(git -C "$CHECKOUT" branch --show-current 2>/dev/null || true)
BEFORE_STATUS=$(git -C "$CHECKOUT" status --porcelain 2>/dev/null || true)
BEFORE_FILE=$(py hash "$CHECKOUT/fugaro.yaml" 2>/dev/null || true)
BEFORE_PROJECT=$(py hash "$PROJECT_FILE" 2>/dev/null || true)

fact premise PASS "$CHECKOUT is on $DEFAULT ($SOURCE), and its $REF tree was copied into this check's scratch directory $ORIGIN_URL_NOTE, with refs/remotes/origin/HEAD at it. The key judged is $KEY: $ORIGIN_NOTE"

# ----------------------------------------------------- the reads and the plan

say "fugaro validate on the default branch ($DEFAULT) — it must refuse $KEY"
rc=0
(cd "$CLONE" && "$FUGARO_BIN" validate "$CLONE/fugaro.yaml" </dev/null) >"$SCRATCH/refuse.out" 2>&1 || rc=$?
{ printf '|'; py refuse "$SCRATCH/refuse.out" "$KEY" "$rc"; } >"$SCRATCH/refuse.row"
state=$(cut -d'|' -f2 "$SCRATCH/refuse.row")
detail=$(cut -d'|' -f3- "$SCRATCH/refuse.row")
fact refuse "$state" "$detail"

say "fugaro validate on $FOLLOWUP — it must name $KEY and NOT refuse it"
git -C "$CLONE" checkout -q -B "$FOLLOWUP" "$DEFAULT"
rc=0
(cd "$CLONE" && "$FUGARO_BIN" validate "$CLONE/fugaro.yaml" </dev/null) >"$SCRATCH/name.out" 2>&1 || rc=$?
{ printf '|'; py name "$SCRATCH/name.out" "$KEY" "$rc" "$FOLLOWUP"; } >"$SCRATCH/name.row"
state=$(cut -d'|' -f2 "$SCRATCH/name.row")
detail=$(cut -d'|' -f3- "$SCRATCH/name.row")
fact name "$state" "$detail"

if [ "$ADDED" = yes ]; then
  fact plan UNVERIFIED "the key $KEY was written into this check's own copy of the tree because $REF's own file writes no lifted key, and \`fugaro update\` reads the default branches of the repositories the project enrolls through the git provider and never a working directory: the copy's key never reaches it, and judging the plan here would accuse a release B that read a file this check made up. Run the check against a checkout whose default branch still writes a lifted key"
else
  say "fugaro update --plan-only --project $PROJECT — it must name $KEY with its fix"
  rc=0
  (cd "$CLONE" && "$FUGARO_BIN" update --plan-only --project "$PROJECT" </dev/null) >"$SCRATCH/plan.out" 2>&1 || rc=$?
  { printf '|'; py plan "$SCRATCH/plan.out" "$KEY" "$rc"; } >"$SCRATCH/plan.row"
  state=$(cut -d'|' -f2 "$SCRATCH/plan.row")
  detail=$(cut -d'|' -f3- "$SCRATCH/plan.row")
  fact plan "$state" "$detail"
fi

# ------------------------------------------------------------- what was left

AFTER_HEAD=$(git -C "$CHECKOUT" rev-parse HEAD 2>/dev/null || true)
AFTER_BRANCH=$(git -C "$CHECKOUT" branch --show-current 2>/dev/null || true)
AFTER_STATUS=$(git -C "$CHECKOUT" status --porcelain 2>/dev/null || true)
AFTER_FILE=$(py hash "$CHECKOUT/fugaro.yaml" 2>/dev/null || true)
AFTER_PROJECT=$(py hash "$PROJECT_FILE" 2>/dev/null || true)

if [ "$AFTER_HEAD" = "$BEFORE_HEAD" ] && [ "$AFTER_BRANCH" = "$BEFORE_BRANCH" ] &&
  [ "$AFTER_STATUS" = "$BEFORE_STATUS" ] && [ "$AFTER_FILE" = "$BEFORE_FILE" ] &&
  [ "$AFTER_PROJECT" = "$BEFORE_PROJECT" ]; then
  fact writes PASS "the checkout at $CHECKOUT is exactly what it was: the same HEAD ($BEFORE_HEAD), the same branch ($BEFORE_BRANCH), the same \`git status\` and the same fugaro.yaml, and the project's local config is unchanged too. The two \`validate\` reads were run on the copy in $SCRATCH, which the restore row removes with everything written into it, and the plan read answers about the project's enrolled repositories and writes nothing here"
else
  fact writes FAIL "the checkout at $CHECKOUT is not what it was: HEAD $BEFORE_HEAD -> $AFTER_HEAD, branch $BEFORE_BRANCH -> $AFTER_BRANCH, fugaro.yaml $BEFORE_FILE -> $AFTER_FILE, the project's local config $BEFORE_PROJECT -> $AFTER_PROJECT, and \`git status\` was '$BEFORE_STATUS' and is now '$AFTER_STATUS'. This check switches no branch and writes no file of the checkout's, so something else did"
fi

REACHED_END=yes
exit 0
