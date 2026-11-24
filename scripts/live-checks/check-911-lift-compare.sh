#!/usr/bin/env bash
# Check 911 — compare the values Check 910 snapshotted before release A's update
# with the same read after it. Run by YOU, after `fugaro update`, with the
# release-A binary installed.
#
# L8 (docs/repo-override-rulings) moves a repository's model choice, its A/B
# rate, its rigor level and areas, its model grades and its `.fugaro/recipes/`
# into the project layer. After the lift a repository must resolve what it
# resolved before (the spec's acceptance 2), and nothing offline can settle that
# for a real installation: the values come from files on your machine and from a
# cloud the check must not touch. So Check 910 photographs the values before the
# drop and this check reads them again after it, at the refs the snapshot
# recorded. One row per criterion:
#
#   premise   the snapshot is Check 910's, of this project, and this run read
#             the same repositories again.
#   binary    the second read happened with the release-A binary: the same fugaro
#             twice would prove nothing about the upgrade.
#   plan      the update's own lines are in the snapshot, and a value that moved
#             is accepted against them.
#   raw       every lifted key of every repository still carries the text the
#             snapshot recorded: the lift is a drop in the resolver, not a
#             rewrite of the repository's file.
#   resolved  every lifted key resolves to the value the snapshot recorded. The
#             layer that decides it moving from the repository to the project is
#             the migration itself, and is reported, not a failure.
#   recipes   every `.fugaro/recipes/` file still checksums the same, and every
#             recipe a launch resolves still resolves to the same text.
#   writes    this check wrote nothing at all: the snapshot is byte-identical to
#             what it was, and its own reads went to a scratch directory it
#             removes.
#
# A value that moved is accepted only where the update named the change, and the
# rule is the shape the update's own lines have: one line per repository, led by
# the repository (`acme/app: coder claude-x -> claude-y (the project's)`), so
# that line names a change when what follows the leading `owner/name:` carries
# what changed — the key as `fugaro settings` spells it or a suffix of it with
# two or more segments, the word the update uses for the role, craft, level or
# grade the key is about (`coder`, `craft coder`, `level l2`, `strong`), `a/b`,
# `model grade`, `recipe`, or one of the two values verbatim. The repository is
# matched only where it leads the line and every token as a whole word, so
# `dimipaun/fugaro` is not named by a line about `dimipaun/fugaro-console`, and
# an `ab.rate` change is not named by the word `stable`. Anything else is a
# value that moved with nothing naming it, and this check fails on it.
#
# Usage:      bash scripts/live-checks/check-911-lift-compare.sh <project> <snapshot> [--dry-run]
#
#             <snapshot> is the file the first step printed, with the update's
#             lines appended by the third. The five owner steps, in order:
#
#               1. bash scripts/live-checks/check-910-lift-snapshot.sh <project>
#                  (before the upgrade, with the pre-L8 binary)
#               2. brew upgrade dimipaun/tap/fugaro
#               3. bash scripts/live-checks/check-910-lift-snapshot.sh <project> \
#                    --plan <the snapshot file step 1 printed>
#               4. fugaro update
#               5. bash scripts/live-checks/check-911-lift-compare.sh <project> \
#                    <the snapshot file>
#
#             --dry-run   prints the plan and calls NOTHING.
# Needs:      bash, mktemp, python3, git, fugaro, and check-910-lift-snapshot.sh
#             beside this script.
# Where:      the installation you name, its repositories' checkouts on this
#             machine, and NOWHERE else. The settings and recipe reads reach the
#             project's own cloud objects, as `fugaro settings` and `fugaro
#             recipes ls` do; nothing is written there.
# Costs:      nothing. No run is launched and no image is built.
# Runs:       by YOU. No agent runs any step of it — it refuses to start in a
#             coding agent's session (CLAUDECODE, CLAUDE_CODE_ENTRYPOINT,
#             CLAUDE_CODE_REMOTE, CURSOR_AGENT, AI_AGENT) — and its own test
#             (scripts/live-checks/check-911-lift-compare_test.sh) drives it
#             against a fake `fugaro` on PATH and real git checkouts, and touches
#             no cloud.
# Cleanup:    its scratch directory is removed from the exit trap, and the
#             `restore` row says whether it really went.
# Output:     progress on stderr, ONE block on stdout headed CHECK 911 RESULT,
#             to copy back. Its last line is the verdict, and the verdict is the
#             exit status:
#
#               CHECK 911 RESULT: PASS (exit 0)        every value matches, or
#                                                      the update named the change;
#               CHECK 911 RESULT: FAIL (exit 1)        a value moved and nothing
#                                                      named it;
#               CHECK 911 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                      could measure everything;
#               CHECK 911 RESULT: INCOMPLETE (exit 3)  it ran to the end and at
#                                                      least one row reads
#                                                      UNVERIFIED.
#
# NO secret is read at all, and none may be printed: the documents this check
# reads are the snapshot and a repository's `fugaro.yaml`, its recipes and the
# settings rows.
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
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 911: it reads the installation's own repositories and cloud objects and compares what a migration moved. Run it yourself: bash scripts/live-checks/check-911-lift-compare.sh <project> <snapshot>"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x), and its trace would print the arguments of every command this check runs. Run it without -x: bash scripts/live-checks/check-911-lift-compare.sh <project> <snapshot>"
  fi
}

usage() {
  cat <<'USAGE'
Check 911 — compare the values Check 910 snapshotted before release A's update
with the same read after it, at the refs the snapshot recorded.

Usage:
  bash scripts/live-checks/check-911-lift-compare.sh <project> <snapshot> [--dry-run]
  bash scripts/live-checks/check-911-lift-compare.sh --help

Run it after `fugaro update`, with the snapshot the three steps before it
produced: Check 910's read before the upgrade, and its --plan step after the
upgrade and before the update. Every value that matches passes; a value that
moved passes only where a line of the update's own plan names it. The block's
last line is the verdict and the exit status:

  PASS (exit 0)        every value matches, or the update named the change
  FAIL (exit 1)        a value moved and nothing named it
  INCOMPLETE (exit 2)  the run stopped before it could measure everything
  INCOMPLETE (exit 3)  at least one row reads UNVERIFIED
USAGE
}

PROJECT=""
SNAPSHOT=""
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
    stop "unknown argument $1 (usage: check-911-lift-compare.sh <project> <snapshot> [--dry-run], or --help)"
    ;;
  *)
    if [ -z "$PROJECT" ]; then
      PROJECT=$1
    elif [ -z "$SNAPSHOT" ]; then
      SNAPSHOT=$1
    else
      stop "two arguments only, a project and the snapshot file Check 910 wrote: $PROJECT and $SNAPSHOT were already given"
    fi
    shift
    ;;
  esac
done

CONFIG_HOME=${XDG_CONFIG_HOME:-${HOME:-}/.config}
SELF=${BASH_SOURCE[0]:-$0}
case "$SELF" in
*/*) SELF_DIR=${SELF%/*} ;;
*) SELF_DIR=. ;;
esac
SELF_DIR=$(cd "$SELF_DIR" && pwd)
NINE_TEN=$SELF_DIR/check-910-lift-snapshot.sh

dry_run_plan() {
  cat <<PLAN
Check 911 plan (nothing is called: no fugaro, no git, no settings read)

  project:       ${PROJECT:-<not given>}
  snapshot:      ${SNAPSHOT:-<not given>}
  reads again:   bash $NINE_TEN $PROJECT --read-to <scratch> --refs-from <snapshot>
  fugaro:        whatever is on PATH now — the release-A binary, NOT the one that
                 took the snapshot, because the same one twice proves nothing
  project file:  $CONFIG_HOME/fugaro/projects/${PROJECT:-<project>}.yaml

1. read the snapshot back: its meta (project, when, binary), its repositories, every
   raw lifted key with the repository's own text, every .fugaro/recipes/ checksum, every
   resolved settings row and every resolved recipe sum, and the update's plan lines.
2. read the same values again, through Check 910 in the --read-to mode: the same
   repositories, the same refs, the release-A binary. It writes only inside this check's
   scratch directory.
3. compare, value by value: raw, resolved and recipes. A value that moved passes only
   where a line of the update's plan names the repository and the change; anything else
   is a FAIL. The layer a resolved key is decided by moving from the repository to the
   project is the migration, and is reported rather than failed.

This check writes NOTHING outside its own scratch directory: the snapshot is read and
hashed before and after, and no file in the installation, in a repository or in the
cloud is touched.
PLAN
}

if [ "$DRY_RUN" = yes ]; then
  guard_xtrace
  guard_agent
  dry_run_plan
  exit 0
fi

[ -n "$PROJECT" ] ||
  stop "no <project> was given: name the installation Check 910 photographed, e.g. check-911-lift-compare.sh fugaro <the snapshot file>"
[ -n "$SNAPSHOT" ] ||
  stop "no <snapshot> was given: name the file Check 910 printed in its writes row, e.g. check-911-lift-compare.sh $PROJECT ./check-910-$PROJECT.snapshot"
case "$SNAPSHOT" in
/*) ;;
*) SNAPSHOT="$PWD/$SNAPSHOT" ;;
esac

guard_xtrace
guard_agent
need bash
need python3
need git
need fugaro
[ -f "$NINE_TEN" ] ||
  stop "$NINE_TEN is not there, and this check reads the values again through it: run both checks from the checkout they live in, with check-910-lift-snapshot.sh beside this script"
[ -f "$SNAPSHOT" ] ||
  stop "$SNAPSHOT is not there: name the snapshot file Check 910 wrote before the upgrade (its writes row prints the path)"

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk911.XXXXXXXX") || stop "cannot make a scratch directory"
chmod 700 "$SCRATCH"
FACTS="$SCRATCH/facts"
: >"$FACTS"
MARKER="$SCRATCH/.chk911-mine"
: >"$MARKER"

cleanup() {
  [ -n "$SCRATCH" ] || return 0
  [ -f "$MARKER" ] || { say "refusing to remove $SCRATCH: it carries no marker from this check"; return 0; }
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
  printf '\nCHECK 911 RESULT\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-14s %-11s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 911 RESULT: %s (exit %s)\n' "$(verdict "$facts")" "$status"
}

finish() {
  local status=$?
  trap - EXIT
  local facts
  facts=$(cat "$FACTS" 2>/dev/null || true)
  cleanup
  if [ -n "$ABORT" ] && ! printf '%s\n' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT"
  fi
  if [ "$REACHED_END" = no ] && ! printf '%s\n' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): the rows that were not reached were NOT measured, which is this script ending on an error rather than an answer about the migration"
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|the scratch directory is gone (it held the second read of every repository's lifted keys, its settings rows and its recipe sums). Nothing of this check was written outside it: the snapshot is the record and it was not touched"
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

# ------------------------------------------------------------------ the reader

# py runs the one python3 program this check owns. It reads two Check 910
# snapshots — the one taken before the update and the one this check has taken
# after it — and compares them record by record: `meta` validates the snapshot,
# `hash` says whether a file moved, and `compare` prints one row per criterion.
# The record grammar is Check 910's: one record per line, `<keyword>
# "<field>"...`, every field a JSON string, so a value with a space, a quote or a
# `|` in it is still one record a reader can split.
py() { # py <mode> [args...]
  python3 - "$@" <<'PY'
import hashlib, json, re, sys, time

ABSENT = "<absent>"

def die(code, msg):
    sys.stderr.write(msg + "\n")
    return code

def read_text(path):
    try:
        with open(path, "rb") as fh:
            return fh.read().decode("utf-8", "replace")
    except Exception:
        return ""

def flat(text):
    return re.sub(r"\s+", " ", text or "").strip()

def records(text):
    out = []
    for line in text.splitlines():
        if not line.strip() or line.startswith("#"):
            continue
        kw = line.split(" ", 1)[0]
        try:
            args = [json.loads(m) for m in re.findall(r'"(?:[^"\\]|\\.)*"', line)]
        except Exception:
            continue
        if not args:
            continue
        out.append((kw, args))
    return out

def meta_of(recs):
    return {f[0]: f[1] for kw, f in recs if kw == "meta" and len(f) >= 2}

def at(recs): # at(recs) -> the value a run of each repository gets, per key
    idx = {}
    for kw, f in recs:
        if kw != "resolved" or len(f) < 5:
            continue
        canon, scope, key, value, wins = f[0], f[1], f[2], f[3], f[4]
        cur = idx.get((canon, key))
        if cur is None or scope == "repo:" + canon:
            idx[(canon, key)] = (value, wins, scope)
    return idx

def raw_at(recs):
    return {(f[0], f[1]): f[2] for kw, f in recs if kw == "raw" and len(f) >= 3}

def pair_at(recs, keyword, key_i, val_i):
    return {(f[0], f[key_i]): f[val_i] for kw, f in recs
            if kw == keyword and len(f) > max(key_i, val_i)}

# ---- what a line of the update's plan can name a change by ----------------

def key_tokens(key):
    parts = [p for p in key.split(".") if p]
    toks = {key}
    for i in range(1, len(parts)):
        if len(parts) - i >= 2:
            toks.add(".".join(parts[i:]))
    if key.startswith("agent.models.") and len(parts) > 2:
        # The update's line calls a role by its own name ("coder").
        toks.add(parts[2])
    elif key.startswith("agent.crafts.") and len(parts) > 2:
        # A craft is "craft coder" and never the bare name: "review" alone
        # stands inside "senior-review" and would name the wrong craft.
        toks.add("craft " + parts[2])
    elif key.startswith("agent.levels.") and len(parts) > 2:
        toks.add("level " + parts[2])
    elif key.startswith("model_grades.") and len(parts) > 1:
        toks.add(parts[1])
        toks.update(("model grade", "grades"))
    elif key == "ab.rate":
        toks.add("a/b")
    return tuple(t.lower() for t in toks if t)

def recipe_tokens(name, path):
    toks = {"recipe", name}
    if path:
        toks.add(path)
        toks.add(path.rsplit("/", 1)[-1])
    return tuple(t.lower() for t in toks if t)

def value_tokens(*values):
    return tuple(v.strip().lower() for v in values if v and v.strip() and v.strip() != ABSENT and len(v.strip()) >= 4)

def leads_with(line, canon):
    """Whether this line is the repository's: every line the update prints for
    one is led by it, `owner/name: what changed`."""
    return re.match(r"^\s*" + re.escape(canon) + r"\s*:", line, re.IGNORECASE) is not None

def word_in(low, token):
    """Whether the token stands in the line as a word of its own.

    A substring test is what lets a token excuse a change it says nothing
    about: the name `fugaro` sits inside `fugaro-console`, and a two-letter
    token inside `stable`, `table` or `enabled`.
    """
    return re.search(r"(?<![0-9a-z_])" + re.escape(token) + r"(?![0-9a-z_])", low) is not None

def named_by(plan, canon, tokens):
    """The plan line that names this change, or ""."""
    toks = tuple(t.lower() for t in tokens if t)
    for line in plan:
        if not leads_with(line, canon):
            continue
        low = line.lower()
        if any(word_in(low, t) for t in toks):
            return flat(line)
    return ""

# ------------------------------------------------------------- the comparison

def mode_check(args):
    """Says whether this is a Check 910 snapshot of this project, before anything is read."""
    if len(args) < 2:
        return die(2, "check needs <snapshot> <project>")
    path, project = args[0], args[1]
    recs = records(read_text(path))
    if not recs:
        return die(3, "%s carries no record this reader knows: it is not a Check 910 snapshot (its writes row prints the file Check 910 wrote)" % path)
    meta = meta_of(recs)
    if meta.get("project") and meta.get("project") != project:
        return die(3, "%s is the snapshot of project %s, not of %s" % (path, meta["project"], project))
    print("\t".join((meta.get("project", ""), str(len(recs)),
                     str(sum(1 for kw, _ in recs if kw == "plan")))))
    return 0

def keep(idx, shared):
    return {k: v for k, v in idx.items() if k[0] in shared}

def moved(before, after, plan, unit, tokens_of):
    """(same, named, unnamed, examples) over one family of values."""
    same, renamed, unnamed, examples = 0, 0, 0, []
    for key in sorted(set(before) | set(after)):
        b, a = before.get(key, ABSENT), after.get(key, ABSENT)
        if b == a:
            same += 1
            continue
        if named_by(plan, key[0], tokens_of(key) + value_tokens(b, a)):
            renamed += 1
            continue
        unnamed += 1
        if len(examples) < 3:
            examples.append("%s %s (%s -> %s)" % (key[0], key[-1], flat(b), flat(a)))
    return same, renamed, unnamed, examples

def rows_of(kind, label, same, renamed, unnamed, examples, total):
    if unnamed:
        return (kind, "FAIL", "of %d %s, %d read the same and %d moved with a line of the update's plan naming it, but %d moved with NOTHING naming it: %s — a changed value is accepted only where the update named the change, and this is the comparison's whole point. If the value really did move, the update's line for it belongs in the snapshot (Check 910's --plan step, before the update)" % (
            total, label, same, renamed, unnamed, "; ".join(examples) + (" (and %d more)" % (unnamed - len(examples)) if unnamed > len(examples) else "")))
    return (kind, "PASS", "of %d %s, %d read the same%s — the migration moved no value without a line of the update's plan naming it" % (
        total, label, same, (", and the update's plan names each of the %d that moved" % renamed) if renamed else ""))

def compare(args):
    if len(args) < 3:
        return die(2, "compare needs <before snapshot> <after snapshot> <project>")
    before, after, project = args[0], args[1], args[2]
    brecs, arecs = records(read_text(before)), records(read_text(after))
    if not brecs:
        return die(3, "%s carries no record this reader knows: it is not a Check 910 snapshot" % before)
    bmeta, ameta = meta_of(brecs), meta_of(arecs)
    if bmeta.get("project") and bmeta.get("project") != project:
        return die(3, "%s is the snapshot of project %s, not of %s" % (before, bmeta["project"], project))
    plan = [flat(f[0]) for kw, f in brecs if kw == "plan" and f and flat(f[0])]

    bres, ares = at(brecs), at(arecs)
    braw, araw = raw_at(brecs), raw_at(arecs)
    bfile, afile = pair_at(brecs, "recipe-file", 1, 2), pair_at(arecs, "recipe-file", 1, 2)
    brec, arec = pair_at(brecs, "recipe", 1, 3), pair_at(arecs, "recipe", 1, 3)
    rows = []

    b_repos = [f[0] for kw, f in brecs if kw == "repo" and f]
    a_repos = [f[0] for kw, f in arecs if kw == "repo" and f]
    shared = [r for r in b_repos if r in a_repos]
    gone = [r for r in b_repos if r not in a_repos]
    new = [r for r in a_repos if r not in b_repos]
    # A repository only one of the two reads covers is not compared: the premise
    # row says so rather than a value row pretending the read happened.
    if gone or new:
        bres, ares = keep(bres, shared), keep(ares, shared)
        braw, araw = keep(braw, shared), keep(araw, shared)
        bfile, afile = keep(bfile, shared), keep(afile, shared)
        brec, arec = keep(brec, shared), keep(arec, shared)
    when = bmeta.get("when", "")
    try:
        when = time.strftime("%Y-%m-%d %H:%M", time.localtime(int(when)))
    except Exception:
        when = when or "an unrecorded moment"
    if not a_repos:
        rows.append(("premise", "UNVERIFIED", "the second read listed no repository at all, so there was nothing to compare: every repository of %s has to be checked out on this machine and enrolled in it, as Check 910's own premise row says" % project))
    elif gone or new:
        rows.append(("premise", "UNVERIFIED", "the two reads do not list the same repositories%s%s, so this is not a like-for-like comparison: make the machine read what it read before (%s) and run the check again" % (
            (" — not read again: " + ", ".join(gone[:3])) if gone else "",
            (" — only read now: " + ", ".join(new[:3])) if new else "",
            "fugaro setup records each checkout, or Check 910's premise row named it as a skip")))
    else:
        rows.append(("premise", "PASS", "%s is Check 910's snapshot of %s (taken %s with %s) and this run read the same %d repositor%s again, each at the ref the snapshot recorded" % (
            before, project, when, bmeta.get("binary") or "an unnamed fugaro", len(b_repos), "y" if len(b_repos) == 1 else "ies")))

    bbin, abin = bmeta.get("binary", ""), ameta.get("binary", "")
    if not abin:
        rows.append(("binary", "UNVERIFIED", "the second read does not say which fugaro answered it, so this comparison does not show that the migration was measured by the release-A binary. Run it with the release-A binary on PATH"))
    elif abin == bbin:
        rows.append(("binary", "UNVERIFIED", "the same fugaro (%s) took the snapshot and read the values again, so the comparison shows nothing about the upgrade: run 'brew upgrade dimipaun/tap/fugaro' between Check 910 and this check, as the owner steps say" % (bbin or "unnamed")))
    else:
        rows.append(("binary", "PASS", "the values were read again by the release-A binary (%s -> %s), so what this compares is what the migration left, not what the pre-L8 binary said twice" % (bbin or "unnamed", abin)))

    raw = moved(braw, araw, plan, "raw", lambda k: key_tokens(k[1]))
    res = moved({k: v[0] for k, v in bres.items()}, {k: v[0] for k, v in ares.items()}, plan, "resolved", lambda k: key_tokens(k[1]))
    files = moved(bfile, afile, plan, "recipe-file", lambda k: recipe_tokens("", k[1]))
    sums = moved(brec, arec, plan, "recipe", lambda k: recipe_tokens(k[1], ""))
    changed = raw[1] + raw[2] + res[1] + res[2] + files[1] + files[2] + sums[1] + sums[2]
    unnamed = raw[2] + res[2] + files[2] + sums[2]

    if not changed:
        rows.append(("plan", "PASS", "nothing moved, so nothing needed naming%s" % (" (the snapshot carries no line of fugaro update --plan-only)" if not plan else " (the snapshot carries %d line(s) of it)" % len(plan))))
    elif not plan:
        rows.append(("plan", "UNVERIFIED", "values moved and the snapshot carries no line of fugaro update --plan-only, which is what a moved value is accepted against: the --plan step belongs BEFORE the update, so take a fresh snapshot and run the five steps in order"))
    else:
        rows.append(("plan", "PASS", "the snapshot carries %d line(s) of fugaro update --plan-only: they are what a value that moved is accepted against, and %d %s named by one of them" % (
            len(plan), changed - unnamed, "value was" if changed - unnamed == 1 else "values were")))

    rows.append(rows_of("raw", "raw lifted keys", raw[0], raw[1], raw[2], raw[3], raw[0] + raw[1] + raw[2]))
    rows.append(rows_of("resolved", "resolved lifted keys", res[0], res[1], res[2], res[3], res[0] + res[1] + res[2]))
    if files[0] + files[1] + files[2] + sums[0] + sums[1] + sums[2] == 0:
        rows.append(("recipes", "UNVERIFIED", "neither read carries a recipe file or a resolved recipe, so the recipe text this migration moves was not measured: Check 910's own recipes row says why it could not read them"))
    else:
        s0, n0, u0, e0 = files
        s1, n1, u1, e1 = sums
        if u0 + u1:
            rows.append(("recipes", "FAIL", "of %d recipe value(s), %d read the same and %d moved with the update's plan naming it, but %d moved with NOTHING naming it: %s — the recipe text a launch resolves is part of what the migration must not change" % (
                s0 + n0 + u0 + s1 + n1 + u1, s0 + s1, n0 + n1, u0 + u1, "; ".join((e0 + e1)[:3]))))
        else:
            rows.append(("recipes", "PASS", "of %d recipe value(s) — the checksum of every .fugaro/recipes/ file the snapshot read and the sum of every recipe a launch resolves — %d read the same%s, so the migration moved no recipe text" % (
                s0 + n0 + s1 + n1, s0 + s1, (", and the update's plan names each of the %d that moved" % (n0 + n1)) if n0 + n1 else "")))

    if not shared:
        # No repository was read twice, so nothing under it was measured. The
        # rows say that rather than reading as a comparison that found no change.
        rows = [r for r in rows if r[0] not in ("raw", "resolved", "recipes", "plan")]
        rows.append(("values", "UNVERIFIED", "no repository was read both before and after the update, so NOTHING under it was compared: fix what the premise and after rows name (the checkouts this machine has, and the second read) and run the check again"))

    for key, state, detail in rows:
        print("%s|%s|%s" % (key, state, flat(detail)))
    return 0

def mode_hash(args):
    if not args:
        return die(2, "hash needs a file")
    try:
        with open(args[0], "rb") as fh:
            data = fh.read()
    except Exception:
        return die(3, "%s cannot be read to hash it" % args[0])
    print("sha256:" + hashlib.sha256(data).hexdigest())
    return 0

def main():
    if len(sys.argv) < 2:
        return die(2, "no mode given")
    mode, args = sys.argv[1], sys.argv[2:]
    for name, fn in (("check", mode_check), ("compare", compare), ("hash", mode_hash)):
        if mode == name:
            return fn(args)
    return die(2, "unknown mode %s" % mode)

sys.exit(main())
PY
}

# --------------------------------------------------------------- the one run

run_compare() {
  local snap=$1
  local before after mrc=0
  before=$(py hash "$snap") || stop "$snap cannot be read: name the snapshot file Check 910 wrote"
  py check "$snap" "$PROJECT" >"$SCRATCH/check.tsv" 2>"$SCRATCH/check.err" || mrc=$?
  [ "$mrc" = 0 ] || stop "$(cat "$SCRATCH/check.err")"
  say "reading the values again with the fugaro on PATH ($(fugaro --version </dev/null 2>/dev/null | head -n1))"
  after="$SCRATCH/after.snapshot"
  local arc=0
  bash "$NINE_TEN" "$PROJECT" --read-to "$after" --refs-from "$snap" </dev/null >"$SCRATCH/after.out" 2>"$SCRATCH/after.err" || arc=$?
  if [ -s "$SCRATCH/after.err" ]; then
    while IFS= read -r line; do say "  $line"; done <"$SCRATCH/after.err"
  fi
  if [ "$arc" != 0 ] || [ ! -s "$after" ]; then
    fact after UNVERIFIED "the second read did not answer (Check 910 exited $arc): $(flat_sentence "$(head -c 300 "$SCRATCH/after.err" 2>/dev/null || true)"). Every value of this comparison comes from it, so nothing was compared: fix what that read refused (its own message says which read) and run this check again"
    REACHED_END=yes
    return 0
  fi
  py compare "$snap" "$after" "$PROJECT" >"$SCRATCH/rows.txt" ||
    stop "the snapshot and the second read could not be compared (see the message above)"
  cat "$SCRATCH/rows.txt" >>"$FACTS"
  local again
  again=$(py hash "$snap") || again=""
  if [ "$again" = "$before" ]; then
    fact writes PASS "'$snap' is byte-identical to what it was ($before): this check wrote NOTHING at all outside its own scratch directory, no file in the installation, in a repository or in the cloud was touched, and no secret is read"
  else
    fact writes FAIL "'$snap' was NOT the file it was before this check ran ($before -> ${again:-unreadable}): something rewrote the snapshot, and this check only reads it"
  fi
  REACHED_END=yes
}

flat_sentence() { # flat_sentence <text>
  printf '%s' "$1" | tr '\n\t|' '   ' | tr -s ' '
}

# ------------------------------------------------------------------- the run

run_compare "$SNAPSHOT"
