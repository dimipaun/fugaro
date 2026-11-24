#!/usr/bin/env bash
# Check 910 — snapshot the lifted values of every enrolled repository, before
# release A turns them into the project's. Run by YOU, with the pre-L8 binary
# you have installed, before `fugaro update`.
#
# L8 (docs/repo-override-rulings) moves a repository's model choice, its A/B
# rate, its rigor level and areas, its model grades and its `.fugaro/recipes/`
# into the project layer. After the lift a repository must resolve what it
# resolved before (the spec's acceptance 2), and nothing offline can settle that
# for a real installation: the values come from files on your machine and from a
# cloud the check must not touch. So this check RECORDS what a repository means
# before the drop, and Check 911 compares the same read after the migration:
#
#   raw        every lifted key of every listed repository, read at the
#              default-branch ref with `git show` — the repository's own text,
#              never through a resolver, so what the drop removes is what the
#              record holds. Nothing is read from the working tree.
#   resolved   what a run of that repository resolves today: one row per lifted
#              key from `fugaro settings --repo <owner/name> --json`, with the
#              layer that decided it and the state of the read.
#   recipes    `.fugaro/recipes/` at the ref, file by file with its checksum,
#              and `fugaro recipes ls|show --json` in the checkout: the names a
#              launch resolves and the sum of each one's text.
#   plan       the lines `fugaro update --plan-only` prints — the update's own
#              naming of every change it would make. They are captured in a
#              SECOND run of this check, because they need the release-A binary:
#
#                1. bash scripts/live-checks/check-910-lift-snapshot.sh <project>
#                   (before the upgrade, with the pre-L8 binary)
#                2. brew upgrade dimipaun/tap/fugaro
#                3. bash scripts/live-checks/check-910-lift-snapshot.sh <project> \
#                     --plan <the snapshot file step 1 printed>
#                4. fugaro update
#                5. bash scripts/live-checks/check-911-lift-compare.sh <project> \
#                     <the snapshot file>
#
# Every row of the block is one criterion of the register's Check 910. The check
# reads; its --plan step is the only thing that runs `fugaro update`, and only
# with --plan-only. It writes NOTHING in the installation, in a repository or in
# the cloud: the snapshot file it is told to write (--out) is the record it
# exists to produce, and nothing else is created outside its own scratch
# directory.
#
# Usage:      bash scripts/live-checks/check-910-lift-snapshot.sh <project> \
#               [--out <file>] [--ref <ref>] [--dry-run]
#             bash scripts/live-checks/check-910-lift-snapshot.sh <project> \
#               --plan <snapshot> [--dry-run]
#             --out       where the snapshot goes (default:
#                         ./check-910-<project>.snapshot in the directory you run
#                         it from). A snapshot that already carries the update's
#                         lines is never overwritten.
#             --ref       the revision the raw values are read at, for every
#                         repository (default: origin/<the repository's base
#                         branch>, else origin/HEAD). A checkout is never
#                         fetched: fetch it yourself first if the ref is stale.
#             --plan      captures `fugaro update --plan-only --project
#                         <project>` into an existing snapshot and writes nothing
#                         else. Needs the release-A binary.
#             --read-to   the read of the snapshot run, written to this file and
#                         with no block on stdout: Check 911 runs this to read
#                         the same values again after the migration. Implies
#                         --refs-from over --ref.
#             --refs-from reads the raw values at the refs that snapshot
#                         recorded, rather than at the refs derived now (only
#                         with --read-to); it is what keeps a comparison about
#                         the migration and not about a branch that moved.
#             --dry-run   prints the plan and calls NOTHING.
# Needs:      bash, mktemp, python3, git, fugaro. Every document is read by one
#             python3 reader and every repository value by `git show`. Nothing is
#             installed, nothing is fetched.
# Where:      the installation you name, its repositories' checkouts on this
#             machine, and NOWHERE else. The settings and recipe reads reach the
#             project's own cloud objects, as `fugaro settings` and `fugaro
#             recipes ls` do; nothing is written there.
# Costs:      nothing. No run is launched and no image is built.
# Runs:       by YOU. No agent runs any step of it — the check refuses to start
#             in a coding agent's session (CLAUDECODE, CLAUDE_CODE_ENTRYPOINT,
#             CLAUDE_CODE_REMOTE, CURSOR_AGENT, AI_AGENT) — and its own test
#             (scripts/live-checks/check-910-lift-snapshot_test.sh) drives it
#             against a fake `fugaro` on PATH and real git checkouts, and touches
#             no cloud.
# Cleanup:    its scratch directory is removed from the exit trap, and the
#             `restore` row says whether it really went. The snapshot file stays:
#             it is the record Check 911 compares against.
# Output:     progress on stderr, ONE block on stdout headed CHECK 910 RESULT,
#             to copy back, and the snapshot's absolute path in the `writes` row.
#             Its last line is the verdict, and the verdict is the exit status:
#
#               CHECK 910 RESULT: PASS (exit 0)        every criterion passed;
#               CHECK 910 RESULT: FAIL (exit 1)        a criterion was measured
#                                                      and failed;
#               CHECK 910 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                      could measure everything;
#               CHECK 910 RESULT: INCOMPLETE (exit 3)  it ran to the end and at
#                                                      least one row reads
#                                                      UNVERIFIED.
#
#             The first run ends INCOMPLETE (exit 3) on purpose: the update's own
#             lines are not in the snapshot yet, and the `plan` row names the one
#             command that puts them there.
#
# NO secret is read at all, and none may be printed: the documents this check
# reads are a repository's `fugaro.yaml`, its recipes, the settings rows and
# the update's plan lines. It reads no secret value, mounts nothing, and its rows
# name keys, refs and paths.
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
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 910: it reads the installation's own repositories and cloud objects and photographs the values a migration is about to move. Run it yourself: bash scripts/live-checks/check-910-lift-snapshot.sh <project>"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x), and its trace would print the arguments of every command this check runs. Run it without -x: bash scripts/live-checks/check-910-lift-snapshot.sh <project>"
  fi
}

usage() {
  cat <<'USAGE'
Check 910 — snapshot the lifted values of every enrolled repository, before
release A turns them into the project's. Run by YOU, with the pre-L8 binary
installed.

Usage:
  bash scripts/live-checks/check-910-lift-snapshot.sh <project> [--out <file>] [--ref <ref>] [--dry-run]
  bash scripts/live-checks/check-910-lift-snapshot.sh <project> --plan <snapshot> [--dry-run]
  bash scripts/live-checks/check-910-lift-snapshot.sh --help

It reads every listed repository's raw lifted keys at its default-branch ref
with `git show` (never through a resolver), its resolved rows from
`fugaro settings --repo <owner/name> --json`, and its recipes from
`fugaro recipes ls|show --json` and `.fugaro/recipes/` at the ref. The second
form captures `fugaro update --plan-only`'s lines into the snapshot; that step
needs the release-A binary and is what Check 911 matches a change against.

Every row it prints is one criterion of the register's Check 910; the last line
of the block is the verdict and the exit status:

  PASS (exit 0)        every criterion passed
  FAIL (exit 1)        a criterion was measured and failed
  INCOMPLETE (exit 2)  the run stopped before it could measure everything
  INCOMPLETE (exit 3)  at least one row reads UNVERIFIED
USAGE
}

PROJECT=""
OUT=""
REF=""
PLAN_IN=""
READ_TO=""
REFS_FROM=""
DRY_RUN=no
while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  --out)
    [ $# -ge 2 ] || stop "--out needs a file to write the snapshot to"
    OUT=$2
    shift 2
    ;;
  --ref)
    [ $# -ge 2 ] || stop "--ref needs a revision, e.g. origin/main"
    REF=$2
    shift 2
    ;;
  --plan)
    [ $# -ge 2 ] || stop "--plan needs the snapshot file to capture into"
    PLAN_IN=$2
    shift 2
    ;;
  --read-to)
    [ $# -ge 2 ] || stop "--read-to needs a file to write the read to"
    READ_TO=$2
    shift 2
    ;;
  --refs-from)
    [ $# -ge 2 ] || stop "--refs-from needs a snapshot file to take the refs from"
    REFS_FROM=$2
    shift 2
    ;;
  --dry-run)
    DRY_RUN=yes
    shift
    ;;
  --*)
    stop "unknown argument $1 (usage: check-910-lift-snapshot.sh <project> [--out <file>] [--ref <ref>] [--plan <snapshot>], --help, or --dry-run)"
    ;;
  *)
    [ -z "$PROJECT" ] || stop "one project at a time: $PROJECT was already given"
    PROJECT=$1
    shift
    ;;
  esac
done

if [ -n "$PLAN_IN" ] && [ -n "$READ_TO" ]; then
  stop "--plan and --read-to are two different runs: the first captures the update's lines into a snapshot, the second reads the values again for Check 911"
fi
if [ -n "$PLAN_IN" ] && [ -n "$REF" ]; then
  stop "--ref and --plan are two different runs: the first reads the values, the second captures the update's lines into a snapshot the first wrote"
fi
if [ -n "$REFS_FROM" ] && [ -z "$READ_TO" ]; then
  stop "--refs-from only goes with --read-to, which is the read Check 911 makes: a snapshot run derives the refs itself"
fi

CONFIG_HOME=${XDG_CONFIG_HOME:-${HOME:-}/.config}
PROJECT_FILE=$CONFIG_HOME/fugaro/projects/${PROJECT:-<project>}.yaml

dry_run_plan() {
  cat <<PLAN
Check 910 plan (nothing is called: no fugaro, no git, no settings read)

  project:       ${PROJECT:-<not given>}
  snapshot:      ${OUT:-$PWD/check-910-${PROJECT:-<project>}.snapshot}
  raw ref:       ${REF:-origin/<the base branch of the repository>, else origin/HEAD}
  project file:  $PROJECT_FILE

1. snapshot mode (the first run, with the pre-L8 binary):
   fugaro config list --json
     -> the installation, its enrolled repositories and where each checkout is on this machine.
   per repository, at the raw ref:
     git -C <checkout> show <ref>:fugaro.yaml
     git -C <checkout> ls-tree -r --name-only <ref> -- .fugaro/recipes
     git -C <checkout> show <ref>:<each recipe file>
     -> rungs: every lifted key's own text (agent.model, agent.models.*, agent.crafts.*.model,
        agent.levels.lN.model, ab.rate, rigor.default, rigor.areas, model_grades.*.model) and
        every recipe file with its checksum. Nothing is read from the working tree.
   per repository:
     fugaro settings --repo <owner/name> --json
       -> the resolved rows: each lifted key's value, the layer that decided it, the read's state.
     (in the checkout) fugaro recipes ls --json; fugaro recipes show <name> --json
       -> the names a launch resolves and the sum of each one's text.
   -> the snapshot records all of it. It writes the snapshot file and NOTHING else: no file in
      the installation, in a repository or in the cloud is touched.

2. plan mode (the second run, with the release-A binary, after the upgrade and before the update):
   fugaro update --plan-only --project ${PROJECT:-<project>}
     -> the update's own lines, appended to the snapshot. It writes nothing else.

Check 911 then repeats the reads of step 1 with the release-A binary and compares: every value
must match, or the update's lines must name the change.
PLAN
}

if [ "$DRY_RUN" = yes ]; then
  guard_xtrace
  guard_agent
  dry_run_plan
  exit 0
fi

[ -n "$PROJECT" ] || stop "no <project> was given (or --help for the usage)"
guard_xtrace
guard_agent
need bash
need mktemp
need python3
need git
need fugaro

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk910.XXXXXXXX") || stop "cannot make a scratch directory"
chmod 700 "$SCRATCH"
FACTS="$SCRATCH/facts"
: >"$FACTS"
MARKER="$SCRATCH/.chk910-mine"
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
  printf '\nCHECK 910 RESULT\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-14s %-11s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 910 RESULT: %s (exit %s)\n' "$(verdict "$facts")" "$status"
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
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): the rows that were not reached were NOT measured, which is this script ending on an error rather than an answer about the repositories"
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|the scratch directory is gone (its documents: every repository's raw lifted keys and its recipe files at the ref, its settings rows as this CLI printed them, its resolved recipe sums, and the update's plan lines). The snapshot file is the record and stays where it says; nothing else was created"
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
  if [ "$QUIET" = yes ]; then exit "$status"; fi
  print_block "$facts" "$status"
  exit "$status"
}
trap finish EXIT

# ------------------------------------------------------------------ the reader

# py runs the one python3 program this check owns. It parses what bash collected
# — the repositories `fugaro config list --json` lists, every repository's
# `fugaro.yaml` at the ref, its settings rows, its recipes, the update's plan
# lines — and it writes the snapshot records. Each mode takes its own arguments:
# `select` prints the repositories to read, `names` prints the names in a
# `recipes ls --json` document, `record` writes the snapshot and the rows, and
# `plan-append` puts the update's lines into a snapshot. Values are written as
# JSON strings, one record per line, so a value with a space, a quote or a `|`
# in it is still one record a reader can split.
py() { # py <mode> [args...]
  python3 - "$@" <<'PY'
import hashlib, json, os, re, sys

GENERIC = {"agent", "models", "crafts", "levels", "model", "grades"}
ABSENT = "<absent>"

FIXED = [("agent", "model"), ("ab", "rate"), ("rigor", "default"), ("rigor", "areas"),
         ("agent", "levels", "l1", "model"), ("agent", "levels", "l2", "model"),
         ("agent", "levels", "l3", "model"),
         ("model_grades", "strong", "model"), ("model_grades", "strongest", "model")]

def die(code, msg):
    sys.stderr.write("check-910: %s\n" % msg)
    sys.exit(code)

def flat(text):
    return " ".join(text.replace("|", "/").split())

def quoted(*fields):
    return " ".join(json.dumps("" if f is None else str(f)) for f in fields)

def load(path, default=None):
    try:
        with open(path) as fh:
            return json.load(fh)
    except Exception:
        return default

def read_text(path):
    try:
        with open(path, errors="replace") as fh:
            return fh.read()
    except Exception:
        return ""

# ---- the YAML block reader ------------------------------------------------
#
# fugaro.yaml is a hand-edited file of nested mappings, and this check needs the
# text of one key's block, not a value through a resolver. A key's own text,
# comments dropped and blank lines gone, is compared as it stands: it is what the
# migration reads and what the drop removes, and a comparison of text is honest
# about a change a value parser would normalize away.

def cut_comment(line):
    out, q = [], None
    for i, c in enumerate(line):
        if q:
            out.append(c)
            if c == q:
                q = None
        elif c in "\"'":
            q = c
            out.append(c)
        elif c == "#" and (i == 0 or line[i - 1] in " \t"):
            break
        else:
            out.append(c)
    return "".join(out).rstrip()

def block_spans(lines):
    spans, stack = {}, []
    for i, raw in enumerate(lines):
        line = cut_comment(raw)
        text = line.strip()
        if not text or text in ("---", "..."):
            continue
        indent = len(line) - len(line.lstrip(" \t"))
        if text.startswith("-"):
            key = "-"
        else:
            m = re.match(r"^([^:\s][^:]*):(\s|$)", text)
            if not m:
                continue
            key = m.group(1).strip()
        while stack and stack[-1][0] >= indent:
            stack.pop()
        path = tuple(stack[-1][1]) + (key,) if stack else (key,)
        if path not in spans:
            spans[path] = (i, indent)
        stack.append((indent, path))
    return spans

def span_text(lines, spans, path):
    if path not in spans:
        return ABSENT
    start, indent = spans[path]
    end = len(lines)
    for j in range(start + 1, len(lines)):
        line = cut_comment(lines[j])
        if not line.strip():
            continue
        if len(line) - len(line.lstrip(" \t")) <= indent:
            end = j
            break
    body = []
    for raw in lines[start:end]:
        line = cut_comment(raw)
        if not line.strip():
            continue
        body.append(line.rstrip())
    return "\n".join(body)

def children(spans, path):
    n = len(path)
    return sorted({p for p in spans if len(p) == n + 1 and p[:n] == path and p[-1] != "-"})

def lifted_paths(spans):
    paths = list(FIXED)
    for c in children(spans, ("agent", "models")):
        paths.append(c)
    for c in children(spans, ("agent", "crafts")):
        paths.append(c + ("model",))
    return paths

def repo_names(text):
    spans = block_spans(text.splitlines())
    return ["/".join(p[1:]) for p in children(spans, ("repos",)) if len(p) == 2]

def sha256(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()

# ---- which keys of the settings read are the lifted ones -------------------

def lifted_key(key):
    if key in ("agent.model", "ab.rate", "rigor.default", "rigor.areas"):
        return True
    if key.startswith("agent.models."):
        return True
    if key.startswith("agent.crafts.") and key.endswith(".model"):
        return True
    if key.startswith("agent.levels.") and key.endswith(".model"):
        return True
    if key.startswith("model_grades.") and key.endswith(".model"):
        return True
    return False

def resolve_ref(global_ref, base_branch):
    if global_ref:
        return global_ref
    if base_branch:
        return "origin/" + base_branch
    return "origin/HEAD"

# ------------------------------------------------------------- select

def mode_select(args):
    if len(args) < 2:
        die(2, "select needs <config.json> <project> [<project file>] [<refs from>]")
    config_path, project = args[0], args[1]
    project_file = args[2] if len(args) > 2 else ""
    refs_from = args[3] if len(args) > 3 else ""
    global_ref = os.environ.get("CHK910_REF", "")
    doc = load(config_path)
    if not isinstance(doc, dict):
        die(3, "fugaro config list --json did not answer with a document")
    inst = None
    for i in doc.get("installations") or []:
        if i.get("name") == project:
            inst = i
            break
    if inst is None:
        names = [i.get("name") or "?" for i in doc.get("installations") or []]
        die(3, "fugaro config list --json lists no installation named %s (it lists: %s); run 'fugaro config list --json' yourself to see the installations this machine knows, and name one of them" % (project, ", ".join(names) if names else "none"))
    overrides = {}
    for kw, f in records(read_text(refs_from)):
        if kw == "repo" and len(f) >= 2:
            overrides[f[0]] = f[1]
    listed, seen = [], set()
    for row in inst.get("checkouts") or []:
        repo = row.get("repository") or ""
        parts = [p for p in repo.split("/") if p]
        if len(parts) < 3:
            continue
        if not row.get("enrolled"):
            continue
        canon = "/".join(parts[1:])
        if canon in seen:
            continue
        seen.add(canon)
        if not row.get("valid"):
            print("\t".join(("skip", canon, "its remembered checkout is not usable (%s)" % (row.get("reason") or "no reason given"))))
            continue
        ref = overrides.get(canon) or resolve_ref(global_ref, row.get("base_branch") or inst.get("base_branch") or "")
        print("\t".join(("repo", canon, ref, str(row.get("path")))))
    if project_file and os.path.exists(project_file):
        for name in repo_names(read_text(project_file)):
            if name in seen:
                continue
            seen.add(name)
            print("\t".join(("skip", name, "the project enrolls it and this machine has no checkout of it")))
    return 0

# ------------------------------------------------------------- names

def mode_names(args):
    if not args:
        die(2, "names needs a recipes document")
    doc = load(args[0], {})
    for row in (doc.get("recipes") or []) if isinstance(doc, dict) else []:
        name = row.get("name") or ""
        if name:
            print(name)
    return 0

# ------------------------------------------------------------- record

def collect_records(scratch, project, out_path):
    """Writes the snapshot and returns the rows as (key, state, detail)."""
    lines = ["# Check 910 snapshot — the lifted values of every enrolled repository of a project,",
             "# read before release A's `fugaro update`. Check 911 compares the same read after it.",
             "# Records: <keyword> \"<field>\"... — one per line, every field a JSON string."]
    meta = {}
    for row in read_text(os.path.join(scratch, "meta.tsv")).splitlines():
        parts = row.split("\t")
        if len(parts) >= 2:
            meta[parts[0]] = parts[1]
    lines.append("meta " + quoted("project", project))
    lines.append("meta " + quoted("when", meta.get("when", "")))
    lines.append("meta " + quoted("binary", meta.get("binary", "")))

    skips, listed, raw_rows, resolved_rows, recipe_rows = [], [], 0, 0, 0
    failed_raw, failed_settings, failed_recipes, empty_config = [], [], [], []
    counts = []
    for row in read_text(os.path.join(scratch, "collect.tsv")).splitlines():
        parts = row.split("\t")
        if len(parts) < 5:
            continue
        canon, ref, checkout, rdir, status = parts[0], parts[1], parts[2], parts[3], parts[4]
        frc, src, lrc = (status.split(",") + ["", "", ""])[:3]
        listed.append(canon)
        lines.append("repo " + quoted(canon, ref, checkout))
        if frc != "0":
            failed_raw.append("%s (git show %s:fugaro.yaml exited %s: %s)" % (canon, ref, frc, flat(read_text(os.path.join(rdir, "fugaro.err"))[:120]) or "no message"))
            continue
        text = read_text(os.path.join(rdir, "fugaro.yaml"))
        if not text.strip():
            empty_config.append(canon)
            continue
        spans = block_spans(text.splitlines())
        for path in lifted_paths(spans):
            lines.append("raw " + quoted(canon, ".".join(path), span_text(text.splitlines(), spans, path)))
            raw_rows += 1
        for row2 in read_text(os.path.join(scratch, "blobs.tsv")).splitlines():
            parts2 = row2.split("\t")
            if len(parts2) < 3 or parts2[0] != canon:
                continue
            data = read_text(parts2[2])
            lines.append("recipe-file " + quoted(canon, parts2[1], sha256(data.encode("utf-8", "replace"))))
        counts.append("raw")

    for row in read_text(os.path.join(scratch, "collect.tsv")).splitlines():
        parts = row.split("\t")
        if len(parts) < 5:
            continue
        canon, rdir, status = parts[0], parts[3], parts[4]
        src, lrc = (status.split(",") + ["", "", ""])[1:3]
        if src != "0":
            failed_settings.append("%s (%s)" % (canon, flat(read_text(os.path.join(rdir, "settings.err"))[:160]) or "fugaro settings exited %s" % src))
        else:
            doc = load(os.path.join(rdir, "settings.json"), {})
            for srow in (doc.get("settings") or []) if isinstance(doc, dict) else []:
                key = srow.get("key") or ""
                if not lifted_key(key):
                    continue
                value = srow.get("value")
                lines.append("resolved " + quoted(canon, srow.get("scope") or "", key,
                                                 ABSENT if value in (None, "") else value,
                                                 srow.get("wins") or "", srow.get("state") or ""))
                resolved_rows += 1
        if lrc != "0":
            failed_recipes.append("%s (fugaro recipes ls exited %s: %s)" % (canon, lrc, flat(read_text(os.path.join(rdir, "recipes.err"))[:160]) or "no message"))
        else:
            doc = load(os.path.join(rdir, "recipes.json"), {})
            names = [r.get("name") or "" for r in (doc.get("recipes") or []) if isinstance(doc, dict)]
            names = [n for n in names if n]
            for row3 in read_text(os.path.join(scratch, "shown.tsv")).splitlines():
                parts3 = row3.split("\t")
                if len(parts3) < 4 or parts3[0] != canon:
                    continue
                name, show_path, srcode = parts3[1], parts3[2], parts3[3]
                shown = load(show_path, {})
                source = shown.get("source") if isinstance(shown, dict) else None
                sum_ = shown.get("sha256") if isinstance(shown, dict) else None
                if srcode != "0" or not sum_:
                    source = source or ""
                    sum_ = "<unread: fugaro recipes show exited %s>" % srcode
                    if srcode == "0":
                        sum_ = ABSENT
                    failed_recipes.append("%s (%s)" % (canon, flat(read_text(os.path.join(rdir, "recipe-%s.err" % name))[:160]) or "fugaro recipes show %s exited %s" % (name, srcode)))
                lines.append("recipe " + quoted(canon, name, source or "", sum_))
                recipe_rows += 1
    for row in read_text(os.path.join(scratch, "repos.tsv")).splitlines():
        parts = row.split("\t")
        if parts[0] == "skip" and len(parts) >= 3:
            skips.append("%s (%s)" % (parts[1], parts[2]))

    tmp = out_path + ".check910"
    with open(tmp, "w") as fh:
        fh.write("\n".join(lines) + "\n")
    os.replace(tmp, out_path)

    n = len(listed)
    rows = []
    detail = "fugaro config list --json lists %s with %d enrolled repositor%s at a checkout on this machine" % (
        project, n, "y" if n == 1 else "ies")
    if skips:
        rows.append(("premise", "UNVERIFIED", "%s, and %d it counts not at all: %s — check each one out on this machine (fugaro setup records the checkout) and run the check again, or the snapshot is missing values the migration will move" % (
            detail, len(skips), "; ".join(skips[:3]) + (", and %d more" % (len(skips) - 3) if len(skips) > 3 else ""))))
    else:
        rows.append(("premise", "PASS", detail))
    if failed_raw:
        rows.append(("raw", "UNVERIFIED", "the raw values of %d of %d repositories could not be read at its own ref: %s — fetch the checkout (%s) and run the check again; nothing is read from the working tree on purpose, so the ref is the only place these values exist" % (
            len(failed_raw), n, "; ".join(failed_raw[:3]), "git -C <checkout> fetch origin")))
    elif empty_config:
        rows.append(("raw", "UNVERIFIED", "no fugaro.yaml at the ref of %s: %s — a repository the project enrolls and whose default branch carries no fugaro.yaml is one this check cannot read the lifted keys of" % (
            ", ".join(empty_config[:3]), "check the ref (--ref) and the checkout")))
    else:
        rows.append(("raw", "PASS", "every lifted key of %d repositor%s was read at its own ref with `git show`, never through a resolver — %d raw keys, each with the repository's own text for it, and every file of .fugaro/recipes/ with its checksum" % (
            n, "y" if n == 1 else "ies", raw_rows)))
    if failed_settings:
        rows.append(("resolved", "UNVERIFIED", "fugaro settings --repo could not read %d of %d repositories, so what a run of each resolves is NOT measured there: %s" % (
            len(failed_settings), n, "; ".join(failed_settings[:3]))))
    else:
        rows.append(("resolved", "PASS", "fugaro settings --repo <owner/name> --json answered for %d repositor%s — %d lifted rows, each with the value it resolves to, the layer that decided it and the state of the read" % (
            n, "y" if n == 1 else "ies", resolved_rows)))
    if failed_recipes:
        rows.append(("recipes", "UNVERIFIED", "fugaro recipes could not be read in some checkouts, so the resolved recipe text is NOT measured there: %s" % (
            "; ".join(sorted(set(failed_recipes))[:3]))))
    else:
        rows.append(("recipes", "PASS", "fugaro recipes ls --json and recipes show --json answered in %d checkout(s) — %d resolved recipe(s) with the sum of each one's text, beside every recipe file at the ref" % (
            n, recipe_rows)))
    return rows, n

def mode_record(args):
    if len(args) < 3:
        die(2, "record needs <scratch> <out> <project>")
    scratch, out_path, project = args[0], args[1], args[2]
    rows, n = collect_records(scratch, project, out_path)
    for key, state, detail in rows:
        print("%s|%s|%s" % (key, state, flat(detail)))
    return 0

# ------------------------------------------------------------- plan-append

def mode_plan_append(args):
    if len(args) < 3:
        die(2, "plan-append needs <snapshot> <plan file> <project>")
    snap, plan_path, project = args[0], args[1], args[2]
    if not os.path.exists(snap):
        die(3, "the snapshot %s is not there: the first run of Check 910 writes it (run it before the upgrade)" % snap)
    text = read_text(snap)
    recs = records(text)
    if not recs:
        die(3, "%s is not a Check 910 snapshot" % snap)
    meta = {f[0]: f[1] for kw, f in recs if kw == "meta" and len(f) >= 2}
    if meta.get("project") and meta.get("project") != project:
        die(3, "%s is the snapshot of project %s, not of %s" % (snap, meta["project"], project))
    kept = [ln for ln in text.splitlines() if not ln.startswith("plan ")]
    plan = [ln for ln in read_text(plan_path).splitlines() if ln.strip()]
    for ln in plan:
        kept.append("plan " + quoted(ln))
    tmp = snap + ".check910"
    with open(tmp, "w") as fh:
        fh.write("\n".join(kept) + "\n")
    os.replace(tmp, snap)
    print("\t".join((str(len(plan)), str(len(recs)))))
    return 0

def mode_snapshot_meta(args):
    if not args:
        die(2, "snapshot-meta needs a snapshot")
    recs = records(read_text(args[0]))
    if not recs:
        die(3, "%s carries no record this reader knows" % args[0])
    meta = {f[0]: f[1] for kw, f in recs if kw == "meta" and len(f) >= 2}
    if not meta.get("project"):
        die(3, "%s carries no meta project record" % args[0])
    print("\t".join((meta["project"], str(len(recs)), str(sum(1 for kw, _ in recs if kw == "plan")))))
    return 0

# ---- reading a snapshot back ----------------------------------------------

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
        out.append((kw, args))
    return out

def main():
    if len(sys.argv) < 2:
        die(2, "no mode given")
    mode, args = sys.argv[1], sys.argv[2:]
    for name, fn in (("select", mode_select), ("names", mode_names), ("record", mode_record),
                     ("plan-append", mode_plan_append), ("snapshot-meta", mode_snapshot_meta)):
        if mode == name:
            return fn(args)
    die(2, "unknown mode %s" % mode)

sys.exit(main())
PY
}

# -------------------------------------------------------------- the two runs

run_snapshot() { # run_snapshot <out file> <snapshot to take the refs from, or "">
  local out=$1 refs_from=$2
  if [ -e "$out" ] && [ -z "$refs_from" ]; then
    local meta plan_lines
    meta=$(py snapshot-meta "$out" 2>/dev/null || true)
    [ -n "$meta" ] ||
      stop "$out exists and is not a Check 910 snapshot this python3 can read: name another --out, or move that file aside"
    plan_lines=$(printf '%s' "$meta" | cut -f3)
    [ "$plan_lines" = "0" ] ||
      stop "$out already carries the update's lines, and this run would take them back out of it: name another --out, or compare that snapshot with Check 911"
  fi
  say "reading the repositories $PROJECT enrolls (premise, raw, resolved, recipes)"
  CHK910_REF="$REF" py select "$SCRATCH/config.json" "$PROJECT" "$PROJECT_FILE" "$refs_from" >"$SCRATCH/repos.tsv" ||
    stop "the repositories of $PROJECT could not be read from fugaro config list --json"
  : >"$SCRATCH/collect.tsv"
  : >"$SCRATCH/blobs.tsv"
  : >"$SCRATCH/shown.tsv"
  {
    printf 'when\t%s\n' "$(date +%s)"
    printf 'binary\t%s\n' "$(fugaro --version </dev/null 2>/dev/null | head -n1)"
  } >"$SCRATCH/meta.tsv"
  local i=0 kind canon ref path rdir rc frc src lrc p n name
  while IFS=$'\t' read -r kind canon ref path; do
    [ "$kind" = repo ] || continue
    i=$((i + 1))
    rdir="$SCRATCH/r/$i"
    mkdir -p "$rdir"
    rc=0
    git -C "$path" show "$ref:fugaro.yaml" >"$rdir/fugaro.yaml" 2>"$rdir/fugaro.err" </dev/null || rc=$?
    frc=$rc
    rc=0
    git -C "$path" ls-tree -r --name-only "$ref" -- .fugaro/recipes >"$rdir/recipes.list" 2>"$rdir/recipes.list.err" </dev/null || rc=$?
    n=0
    while IFS= read -r p; do
      [ -n "$p" ] || continue
      n=$((n + 1))
      git -C "$path" show "$ref:$p" >"$rdir/recipe-$n.blob" 2>"$rdir/recipe-$n.err" </dev/null || true
      printf '%s\t%s\t%s\n' "$canon" "$p" "$rdir/recipe-$n.blob" >>"$SCRATCH/blobs.tsv"
    done <"$rdir/recipes.list"
    rc=0
    fugaro settings --repo "$canon" --json >"$rdir/settings.json" 2>"$rdir/settings.err" </dev/null || rc=$?
    src=$rc
    rc=0
    (cd "$path" && fugaro recipes ls --json </dev/null) >"$rdir/recipes.json" 2>"$rdir/recipes.err" || rc=$?
    lrc=$rc
    if [ "$lrc" = 0 ]; then
      while IFS= read -r name; do
        [ -n "$name" ] || continue
        rc=0
        (cd "$path" && fugaro recipes show "$name" --json </dev/null) >"$rdir/show-$name.json" 2>"$rdir/recipe-$name.err" || rc=$?
        printf '%s\t%s\t%s\t%s\n' "$canon" "$name" "$rdir/show-$name.json" "$rc" >>"$SCRATCH/shown.tsv"
      done < <(py names "$rdir/recipes.json" 2>/dev/null || true)
    fi
    printf '%s\t%s\t%s\t%s\t%s,%s,%s\n' "$canon" "$ref" "$path" "$rdir" "$frc" "$src" "$lrc" >>"$SCRATCH/collect.tsv"
  done <"$SCRATCH/repos.tsv"
  py record "$SCRATCH" "$out" "$PROJECT" >"$SCRATCH/rows.txt" ||
    stop "the snapshot of $PROJECT could not be written to $out"
  say "the snapshot is at $out"
}

run_plan() { # run_plan <snapshot> <file the plan output goes to>
  local snap=$1 planfile=$2 rc=0 lines added kept
  local meta
  meta=$(py snapshot-meta "$snap" 2>/dev/null || true)
  [ -n "$meta" ] || stop "$snap could not be read as a Check 910 snapshot by this python3"
  say "capturing fugaro update --plan-only's lines into $snap (plan, writes)"
  fugaro update --plan-only --project "$PROJECT" >"$planfile" 2>"$SCRATCH/plan.err" </dev/null || rc=$?
  lines=$(grep -c . "$planfile" 2>/dev/null || true)
  [ -n "$lines" ] || lines=0
  if [ "$rc" != 0 ]; then
    fact plan UNVERIFIED "fugaro update --plan-only --project $PROJECT exited $rc: $(flat_sentence "$(head -c 300 "$SCRATCH/plan.err" 2>/dev/null || true)"). Its lines are the update's own naming of every change it would make, before any write, and they are what Check 911 accepts a changed value against: without them a change the update named reads as one nobody named. Fix what the update refused and run this step again"
  else
    py plan-append "$snap" "$planfile" "$PROJECT" >"$SCRATCH/appended.tsv" ||
      stop "the update's lines could not be appended to $snap"
    added=$(cut -f1 "$SCRATCH/appended.tsv")
    kept=$(cut -f2 "$SCRATCH/appended.tsv")
    fact plan PASS "fugaro update --plan-only --project $PROJECT printed $lines line(s) and they are in the snapshot ($kept records, with the $added plan line(s) added and every earlier record kept as it was). They are the update's own naming of every change it would make, before any write"
  fi
  fact writes PASS "'$snap' is the only file this step wrote, and only its plan records moved: `fugaro update --plan-only` applies nothing, no file in the installation, in a repository or in the cloud was touched, and nothing this step ran reads a secret"
  REACHED_END=yes
}

flat_sentence() { # flat_sentence <text>
  printf '%s' "$1" | tr '\n\t|' '   ' | tr -s ' '
}

# ------------------------------------------------------------------- the run

if [ -n "$PLAN_IN" ]; then
  run_plan "$PLAN_IN" "$SCRATCH/plan.out"
  exit 0
fi

if [ -z "$OUT" ]; then
  OUT="$PWD/check-910-$PROJECT.snapshot"
fi
case "$OUT" in
/*) ;;
*) OUT="$PWD/$OUT" ;;
esac

fugaro config list --json >"$SCRATCH/config.json" 2>"$SCRATCH/config.err" </dev/null ||
  stop "fugaro config list --json did not answer: $(flat_sentence "$(head -c 300 "$SCRATCH/config.err" 2>/dev/null || true)"). This check lists the project's repositories with it; fix that read and run the check again"

if [ -n "$READ_TO" ]; then
  QUIET=yes
  run_snapshot "$READ_TO" "$REFS_FROM"
  REACHED_END=yes
  exit 0
fi

run_snapshot "$OUT" ""
cat "$SCRATCH/rows.txt" >>"$FACTS"
fact plan UNVERIFIED "the update's own lines are NOT in the snapshot yet: they need the release-A binary, so after 'brew upgrade dimipaun/tap/fugaro' and BEFORE 'fugaro update', run 'bash scripts/live-checks/check-910-lift-snapshot.sh $PROJECT --plan $OUT'. Check 911 accepts a changed value only where those lines name it, so this half is what makes the comparison mean something"
fact writes PASS "'$OUT' is the only file written: every command this check ran was a read (fugaro config list, $(grep -c . "$SCRATCH/collect.tsv" 2>/dev/null || printf 0) fugaro settings reads, $(grep -c . "$SCRATCH/shown.tsv" 2>/dev/null || printf 0) fugaro recipes reads), no file in the installation, in a repository or in the cloud was touched, and no secret is read at all"
REACHED_END=yes
