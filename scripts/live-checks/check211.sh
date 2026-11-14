#!/usr/bin/env bash
# Check 211 — the judge's input is blind.
#
# Plan task A5.3 writes this check; what it settles is docs/model-ab-testing/
# implementation-plan.md's "A5.3 Check 211" — "the stored judge input names no
# run ID, branch, model or commit trailer (U1); and how often a judge asked
# 'which model wrote A?' names it (U2), recorded as a rate, not a pass/fail" —
# and the plan's Global constraints table row "The judge never sees a run ID,
# branch name, model ID or commit metadata" (A5.1's
# TestBundleCarriesNoIdentifier, Check 211).
#
# What it measures, on the sandbox installation:
#
#   * ONE judge run — `fugaro run --recipe ab-judge`, the command
#     docs/how-to/model-ab-testing.md names and the plan's own sentence spells —
#     launched from the sandbox checkout, over at least five known pairs: the
#     launcher's manifest lists the complete, unjudged pairs of the repository's
#     last fortnight, so the check says how many pairs the run took and which
#     models wrote their two arms (A5.2, D569);
#   * the objects the run left under its OWN prefix — `ab/manifest.json` and
#     `ab/verdicts.json` (runstore.ManifestName, runstore.VerdictsName) — read
#     out of the runs bucket as the owner reads them, and then each judged
#     pair's stored input at the key its verdict names
#     (`ab/inputs/<sha256(pair ID)[:8] hex>.json`, A5.3's own object);
#   * U1, PASS or FAIL, over those stored inputs and nothing else: neither
#     pass's system text or prompt may carry either arm's run ID, either arm's
#     branch, the pair ID or either ARM's model (their IDs, their names and
#     their vendor family — A5.1's redaction vocabulary, whose shortest word is
#     four bytes) or a run-ID-shaped string. The judge's own model is not
#     graded: A5.1 is handed the two arms' models alone and the stored input
#     names the judge model in its own `judge_model` field (the note on the
#     needles below says why). A5.1 refuses a
#     pair whose diff names a run and redacts a model word to one placeholder,
#     so a hit here is the anonymiser not holding on a real pair. The plan's U1
#     also names a commit trailer, and the object graded here cannot carry one:
#     A5.1 anonymises a tree diff, which holds no commit metadata at all (the
#     note on the vocabulary below says what a trailer-shaped line in one is);
#   * U2, RECORDED and never graded: how often a judge names a model. The
#     plan's literal question — "which model wrote A?" — has no surface in this
#     release, and the check says so in the row rather than inventing one: the
#     judge's call is fixed by ee/ab/judge.go (two diffs, one closed vocabulary,
#     no question of the check's own), no command in this release asks a chosen
#     model a question, and a recipe cannot pin one. So the row records the
#     numbers the judge's own stored inputs do support — how many of its answers
#     name a model, and how its verdicts split across the two arms it was shown
#     blind — and the plan's gap is reported in the row, in the checklist
#     section and to the owner rather than papered over;
#   * and the run's own row, so the reading is of a judge run that ended and
#     published nothing: no branch and no pull request (A5.2).
#
# It creates one judge run and nothing else, and a judge run leaves no branch
# and no pull request to clean up: the teardown stops a judge run that has not
# ended (`fugaro cancel --now`, read back as ended) and leaves the run's own
# objects where the report reads them (A5.4). Nothing is deleted.
#
# Usage:      bash scripts/live-checks/check211.sh --sandbox <a sandbox repository checkout> --yes
#             bash scripts/live-checks/check211.sh --sandbox <path> --yes --min-pairs 5
#             bash scripts/live-checks/check211.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check211.sh --help
# Needs:      bash, mktemp, date, python3 (the `--json` documents and the stored
#             judge inputs are read with it), gcloud (authenticated as YOU, and
#             able to read the runs bucket) and fugaro (the installed release:
#             the whole point is the build you have).
# Where:      your own machine and the **sandbox project** and the **sandbox
#             repository** only. A project or region naming belong is refused by
#             name, and so is a coding agent's session: every command this check
#             runs is one you could type yourself.
# Creates:    ONE judge run on the sandbox project, which writes its manifest,
#             its verdicts and one stored input per judged pair under its own
#             prefix in the runs bucket and nothing anywhere else — no branch,
#             no pull request, no comment. On disk: one scratch directory,
#             removed at the end, holding the commands' output.
# Costs:      one judge run: two model calls per pair it takes, up to twenty-five
#             pairs (judgeManifestMax), on `ab.judge_model` through the project's
#             model credential and budget — so up to fifty calls, plus the
#             launch's own listing. `--yes` is required.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check211_test.sh) drives it against fake tools only.
# Cleanup:    a judge run that has not ended, stopped with `fugaro cancel --now`
#             and read back as ended, so that no run of this check's own
#             outlives it; and the scratch directory, removed from the exit trap
#             even on a failed assertion or a Ctrl-C. The run's own objects stay
#             in the bucket: they are what the report reads (A5.4), and a check
#             that deleted them would delete the reading.
# Output:     progress on stderr, ONE block on stdout headed CHECK 211 RESULT,
#             then CHECK 211 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold — a build without the judge, a layer
#             naming no judge model, fewer than five pairs, a verdict that does
#             not match its manifest, or a stored input that names a run, a
#             branch, a model or a run ID's shape. 2: the check could not
#             measure — a tool is missing, the checkout is not one, a `--json`
#             document could not be read, a launch was refused, or the judge run
#             had not ended. A key the run never reached reads `FAIL — not
#             reached`.
#
# A NOTE ON WHAT IS NEVER PRINTED: no credential value reaches this script's
# stdout, its stderr, the block or the scratch directory. The rows carry run
# IDs, pair IDs, branch names, model IDs and object keys — which is what makes
# the block a reading of the judge's own objects rather than a summary of it.
#
# Knobs (all CHECK211_*): DRY_RUN, KEEP_SCRATCH, SANDBOX (the checkout, when you
# would rather set it in the environment), YES, MIN_PAIRS (5: the plan's "at
# least five known pairs"), RELEASE (0.24.0: the release the judge run ships
# in, evaluateSince), REGION (us-east5), RUN_TIMEOUT_SECONDS (1800),
# POLL_SECONDS (15), CLI_TIMEOUT_SECONDS (180), LAUNCH_TIMEOUT_SECONDS (600),
# DOCTOR_TIMEOUT_SECONDS (300), CANCEL_TIMEOUT_SECONDS (300). --sandbox and
# --min-pairs are the same two values as flags, and a value you type as a flag
# beats the same value in the environment: the environment is how a script sets
# a knob, and a run that quietly used the other one would spend the sandbox's
# money at a bound you did not choose.
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
# — and the check ends as one that could not measure (exit 2). U2's row is
# deliberately NOT one of these: the plan records it as a number and never as a
# pass or a fail, so a rate the check could not take is said in the row.
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
  command -v bash >/dev/null 2>&1 || die "Check 211 needs bash"
  local t
  for t in mktemp date python3 gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 211 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk211-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
PROJECT=""
REF=""
RUN_ID=""
JUDGE_MODEL=""
LAUNCHED=0
# PREMISE_RECIPE_OK and PREMISE_LAYER_OK are set by the two rows that establish
# the state a launch is a reading of — the judge recipe this build carries and
# the published layer's judge model. They are TWO flags rather than one: each
# row is a reading of a different thing, and a launch after either failed would
# spend the sandbox's money on a reading nobody could grade, with the FAIL row
# above already the finding.
PREMISE_RECIPE_OK=no
PREMISE_LAYER_OK=no
# PAIRS_OK is the same for the two rows read off the manifest: without a
# manifest with pairs in it there is no key to read a stored input at.
PAIRS_OK=no
# VERDICTS_OK is the same for the rows read off the verdicts object.
VERDICTS_OK=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk211.XXXXXXXX") || die "Check 211 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  # ${HOME:-}: this guard keeps the removal from ever reaching the owner's home,
  # and reading it unguarded would abort cleanup under `set -u` on a machine
  # where HOME is unset, leaving the scratch directory behind.
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
    say "CHECK211_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The documents this check reads, in one program: a live check on the owner's
# machine has python3 and no jq, and a row that read a document with grep would
# report a run ID the release did not write the day the document is re-wrapped.
# Every reader prints `key=value` lines or `\t`-separated rows, so a shell can
# hold them without a second parser.
#
# The one document that is not JSON is the published project layer, which
# `fugaro config layer --json` hands over as YAML TEXT: `judge_model` lives in
# its `ab:` block (D553) and no command resolves the block to a value, so the
# check reads the block here, as the owner reads it in the file they published.
# It is a text read and the row says so.
#
# `judged` is the one query that takes more than a document: the stored judge
# input (A5.3's own object), the launcher's manifest and the pair's id. The
# needles are built HERE rather than in the shell because they are A5.1's own
# vocabulary — the arms' run IDs, their branches, the pair ID, the arms' models
# with the words their names are built from — and one program holding both halves
# is what keeps the scan and the redaction from drifting.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import json
import re
import sys


def load(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def yes(value):
    return "true" if value else "false"


def flat(value):
    return " ".join(str(value or "").split())


def strip_comment(s):
    out = []
    quote = None
    i = 0
    while i < len(s):
        c = s[i]
        if quote:
            if c == quote:
                quote = None
            out.append(c)
        elif c in "\"'":
            quote = c
            out.append(c)
        elif c == "#" and (not out or out[-1] in " \t"):
            break
        else:
            out.append(c)
        i += 1
    return "".join(out).strip()


def unquote(s):
    s = s.strip()
    if len(s) >= 2 and s[0] == s[-1] and s[0] in "\"'":
        return s[1:-1]
    return s


def split_flow(inner):
    """Split a flow sequence or mapping body on its own top-level commas."""
    parts = []
    depth = 0
    quote = None
    cur = []
    for c in inner:
        if quote:
            if c == quote:
                quote = None
            cur.append(c)
            continue
        if c in "\"'":
            quote = c
            cur.append(c)
            continue
        if c in "[{":
            depth += 1
        elif c in "]}":
            depth -= 1
        if c == "," and depth == 0:
            parts.append("".join(cur))
            cur = []
            continue
        cur.append(c)
    if "".join(cur).strip():
        parts.append("".join(cur))
    return [p.strip() for p in parts if p.strip()]


def flow_mapping(body):
    out = {}
    for part in split_flow(body):
        key, sep, val = part.partition(":")
        if not sep:
            continue
        out[unquote(key)] = unquote(val)
    return out


def ab_block(text):
    """The top-level `ab:` block of a layer, as a mapping, or None when the
    layer carries no such key. Both spellings a layer file may use are read:
    the block form (indented keys under `ab:`) and the flow form (`ab: {...}`),
    which is the shape the config tests write."""
    lines = text.splitlines()
    for i, line in enumerate(lines):
        s = strip_comment(line)
        if not re.match(r"^ab:\s*(.*)$", s):
            continue
        rest = re.match(r"^ab:\s*(.*)$", s).group(1).strip()
        if rest.startswith("{"):
            return flow_mapping(rest.strip("{}"))
        if rest:
            return {}
        out = {}
        for line2 in lines[i + 1:]:
            s2 = strip_comment(line2)
            if not s2:
                continue
            if not re.match(r"^\s", line2):
                break
            m = re.match(r"^([A-Za-z_][A-Za-z0-9_-]*):\s*(.*)$", s2)
            if m:
                out[m.group(1)] = unquote(m.group(2).strip())
        return out
    return None


def top_keys(text):
    """The layer's top-level keys, for a row that has to say what it read."""
    out = []
    for line in text.splitlines():
        if not re.match(r"^[A-Za-z_]", line):
            continue
        m = re.match(r"^([A-Za-z_][A-Za-z0-9_-]*):", strip_comment(line))
        if m:
            out.append(m.group(1))
    return out


# A5.1's own shape, spelled here because the release's is unexported: the run
# ID's shape as an unanchored search (bundle.go's runIDGuess).
RUNID_RE = re.compile(r"(?i)[0-9]{8}-[0-9]{6}-[0-9a-f]{4}")

# The plan's U1 also names a commit trailer, and there is nothing of that kind
# here to grade: the object A5.1 anonymises is a TREE diff, the change between
# the run's start commit and its final head, so it carries no commit metadata at
# all (bundle.go's Patch.Diff says so) and nothing strips any. A line in one
# that looks like a trailer is diff body text — this repository alone has 22
# tracked files carrying "generated with" and 5 carrying "noreply@" — so a row
# that graded those strings would fail a correct release on a hunk that mentions
# its own tooling and call it the anonymiser not holding.


def version_word(w):
    """A word of a model's name that is its version rather than part of what the
    model is called ('5' in claude-opus-5-5, 'v4' in deepseek-v4-pro), and where
    A5.1's walk of the name stops (bundle.go's versionWord)."""
    if not w:
        return True
    if w[0].isdigit():
        return True
    return len(w) > 1 and w[0] in "vV" and w[1].isdigit()


def model_words(mid):
    """A model ID and every word A5.1's vocabulary holds for it (bundle.go's
    modelWords): the ID whole, the name it is served under, the vendor family it
    belongs to (config.ModelFamily — 'anthropic' for a Claude ID, the prefix for
    a provider ID) and the words its name is built from UP TO its version, where
    the walk stops. A word is at least four bytes (minModelWord), which is the
    line that keeps the vocabulary from eating ordinary text.

    The stop is what leaves a variant word in the diff on purpose: 'flash' in
    deepseek-v4-flash names no model on its own, so a diff saying "flash" is not
    a leak, while a diff saying "deepseek" is — and a reader that kept going past
    the version would report the first as the anonymiser not holding."""
    out = []

    def put(word):
        word = (word or "").lower()
        if len(word) >= 4 and word not in out:
            out.append(word)

    mid = mid.strip()
    if not mid:
        return out
    put(mid)
    name = mid.rsplit("/", 1)[-1]
    put(name)
    if "/" in mid:
        put(mid.split("/", 1)[0])
    elif mid.startswith("claude-"):
        put("anthropic")
    for w in [part for part in re.split(r"[-._@]", name) if part]:
        if version_word(w):
            break
        put(w)
    return out


def needles_for(manifest, pair_id):
    """Every string one pair's stored input must not carry: the arms' run IDs,
    their branches, the pair ID, and the ARMS' models — and not the judge's.

    The judge model is not one of the pair's identifiers and A5.1 never redacts
    it: Bundle is handed the two arms' models alone (bundle.go's
    modelWords(champion.Model, challenger.Model)) and the stored input names the
    judge model itself in its own top-level `judge_model` field, which A5.2's
    own test pins. A row that graded a diff for naming the judge's model would
    fail a correct release after a billed run and blame the anonymiser for
    something it was never asked to remove."""
    out = []
    for pair in manifest.get("pairs") or []:
        if flat(pair.get("pair_id")) != pair_id:
            continue
        out.append(("pair", pair_id))
        for side in ("champion", "challenger"):
            arm = pair.get(side) or {}
            for kind, key in (("run", "run_id"), ("branch", "branch"), ("model", "model")):
                value = flat(arm.get(key))
                if value:
                    out.append((kind, value))
    return out


def scan(text, needles):
    """Every hit in one piece of text, as (kind, needle, line) triples. One word
    is one hit whatever named it: two models of the same family share the words
    their names are built from, and a reader that counted 'claude' twice would
    report one occurrence as two."""
    if not text:
        return
    low = text.lower()
    seen = set()
    for kind, value in needles:
        words = model_words(value) if kind == "model" else [value]
        for word in words:
            key = (kind, word.lower())
            if key in seen:
                continue
            seen.add(key)
            if word.lower() not in low:
                continue
            line = ""
            for candidate in text.splitlines():
                if word.lower() in candidate.lower():
                    line = flat(candidate).replace("|", "/")[:160]
                    break
            yield kind, word, line
    for m in RUNID_RE.finditer(text):
        # A string the pair's own identifiers already named is reported once:
        # the arms' run IDs are what the shape search is a backstop for.
        if any(m.group(0) == value for _, value in needles):
            continue
        yield "run-id shape", m.group(0), ""


what = sys.argv[1]
if what == "doctor":
    doc = load(sys.argv[2])
    # `fugaro doctor --json` carries the installation under `project`
    # (doctorOutput.Project, doctorProject.GCPProject): a top-level read here
    # would find no project on every run and report the premise unmeasured.
    print("gcp_project=%s" % flat((doc.get("project") or {}).get("gcp_project")))
elif what == "layer":
    doc = load(sys.argv[2])
    print("where=%s" % flat(doc.get("where")))
    print("generation=%s" % doc.get("generation") or 0)
    print("sha256=%s" % flat(doc.get("sha256")))
    text = doc.get("yaml") or ""
    ab = ab_block(text)
    print("has_ab=%s" % yes(ab is not None))
    print("judge_model=%s" % flat((ab or {}).get("judge_model")))
    print("ab_keys=%s" % ",".join(sorted((ab or {}).keys())))
    print("top_keys=%s" % ",".join(top_keys(text)))
elif what == "recipes":
    doc = load(sys.argv[2])
    rows = doc.get("recipes") or []
    print("count=%d" % len(rows))
    for row in rows:
        steps = ",".join(flat(s.get("kind")) for s in row.get("steps") or [])
        print("recipe\t%s\t%s\t%s\t%s" % (
            flat(row.get("name")),
            flat(row.get("source")),
            yes(row.get("launchable")),
            steps,
        ))
elif what == "launch":
    doc = load(sys.argv[2])
    print("run_id=%s" % flat(doc.get("run_id")))
    print("repo=%s" % flat(doc.get("repo")))
    print("status=%s" % flat(doc.get("status")))
    print("recipe=%s" % flat((doc.get("recipe") or {}).get("name")))
    print("branch=%s" % flat(doc.get("branch")))
elif what == "rows":
    doc = load(sys.argv[2])
    rows = doc.get("runs") or []
    print("count=%d" % len(rows))
    for row in rows:
        print("row\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s" % (
            row.get("run_id") or "",
            row.get("status") or "",
            yes(row.get("terminal")),
            row.get("batch") or "",
            row.get("run") or "",
            row.get("branch") or "",
            row.get("pr") or 0,
            row.get("outcome") or "",
            row.get("repo") or "",
        ))
elif what == "manifest":
    doc = load(sys.argv[2])
    print("version=%s" % doc.get("version") or 0)
    print("judge_model=%s" % flat(doc.get("judge_model")))
    pairs = doc.get("pairs") or []
    print("pairs=%d" % len(pairs))
    for pair in pairs:
        for side in ("champion", "challenger"):
            arm = pair.get(side) or {}
            print("arm\t%s\t%s\t%s\t%s\t%s\t%s" % (
                flat(pair.get("pair_id")),
                side,
                flat(arm.get("run_id")),
                flat(arm.get("branch")),
                flat(arm.get("model")),
                flat(arm.get("patch_object")),
            ))
elif what == "verdicts":
    doc = load(sys.argv[2])
    print("version=%s" % doc.get("version") or 0)
    print("judge_model=%s" % flat(doc.get("judge_model")))
    pairs = doc.get("pairs") or []
    print("pairs=%d" % len(pairs))
    print("judged=%d" % len([p for p in pairs if flat(p.get("input"))]))
    print("skipped=%d" % len([p for p in pairs if flat(p.get("skipped"))]))
    for pair in pairs:
        print("verdict\t%s\t%s\t%s\t%s\t%s" % (
            flat(pair.get("pair_id")),
            flat(pair.get("winner")),
            flat(pair.get("reason")),
            flat(pair.get("skipped")),
            flat(pair.get("input")),
        ))
        for i, ps in enumerate(pair.get("passes") or [], 1):
            print("pass\t%s\t%d\t%s\t%s\t%s\t%s" % (
                flat(pair.get("pair_id")),
                i,
                yes(ps.get("swap")),
                flat(ps.get("label")),
                flat(ps.get("arm")),
                flat(ps.get("reason")),
            ))
elif what == "judged":
    # judged INPUT MANIFEST PAIR ID: one stored judge input, read against the
    # pair's own identifiers. Every `hit` line is a string the input carries
    # that A5.1's bundle exists to remove; the shell grades the ones in the
    # system text and the prompt (U1) and counts the ones in the answers (U2).
    doc = load(sys.argv[2])
    manifest = load(sys.argv[3])
    pair_id = sys.argv[4]
    print("pair_id=%s" % flat(doc.get("pair_id")))
    print("judge_model=%s" % flat(doc.get("judge_model")))
    passes = doc.get("passes") or []
    print("passes=%d" % len(passes))
    needles = needles_for(manifest, pair_id)
    total = 0
    for i, ps in enumerate(passes, 1):
        system = ps.get("system") or ""
        prompt = ps.get("prompt") or ""
        answer = ps.get("answer") or ""
        total += len(system) + len(prompt)
        print("pass\t%d\t%s\t%d\t%s" % (i, yes(ps.get("swap")), len(prompt), flat(answer)))
        for where, text in (("system", system), ("prompt", prompt), ("answer", answer)):
            for kind, needle, line in scan(text, needles):
                print("hit\t%d\t%s\t%s\t%s\t%s" % (i, where, kind, needle, line))
    print("chars=%d" % total)
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

# read_json QUERY FILE DEST: answer one query into a file, and fail the step
# when the document is not the document it should be.
read_json() { # read_json QUERY FILE DEST
  python3 "$SCRATCH/read.py" "$1" "$2" >"$3" 2>"$3.err"
}

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# rows_row FILE RUNID — the `fugaro runs ls --json` row for one run, as the tab
# row it is. The run's own id is the only row that counts: a list read by
# position alone could be another run's.
rows_row() { # rows_row FILE RUNID
  awk -F'\t' -v id="$2" '$1 == "row" && $2 == id { print; exit }' "$1" 2>/dev/null
}

rcol() { # rcol ROW N — one column of a tab row, "" when there is none
  [ -n "$1" ] || return 0
  printf '%s\n' "$1" | awk -F'\t' -v n="$2" '{ print $n }'
}

# row_field LINE N — one column of a tab row read whole, with awk rather than
# `read`: with IFS=$'\t' a run of tabs is ONE delimiter, so a row with an empty
# column in the middle (a verdict that skipped nothing) would shift every column
# after it and the row would be read as another row's.
row_field() { # row_field LINE N
  printf '%s\n' "$1" | awk -F'\t' -v n="$2" '{ print $n }'
}

# row_tag LINE — the first column of a tab row, "" when the line has none.
row_tag() { # row_tag LINE
  case "$1" in
  *$'\t'*) printf '%s\n' "${1%%$'\t'*}" ;;
  *) printf '%s\n' "" ;;
  esac
}

# The two facts run_bounded reports are declared here, before any step: a step
# that names them in a message after a read that never started would otherwise
# abort under `set -u` instead of reporting what it found.
BOUNDED_STATUS=0
BOUNDED_TIMED_OUT=no

# run_bounded SECONDS KEY DIR CMD...: run one command from the directory it has
# to be run in and under a bound. Every command here names the project through
# the checkout it is run in, and the bucket reads are the exception.
#
# The child execs, so the pid the bound kills IS the command: a hung cloud call
# would otherwise outlive the check that gave up on it, which is the one thing
# the bound exists to prevent. The ERR trap is dropped in it too: this check runs
# commands whose failing exit status is one of its own readings (a launch that is
# refused, a run that is not in the list), and a trap inherited here would turn
# each of them into "an internal error" and a status of 2 before the step could
# report it.
run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  BOUNDED_TIMED_OUT=no
  # Truncated, not appended: a poll calls the same key again and again, and a
  # `--json` read takes the call's own document — two documents in one file is a
  # parse error, not a second reading.
  ( trap - ERR; cd "$dir" || exit 1; exec "$@" ) >"$SCRATCH/$key.out" 2>"$SCRATCH/$key.err" &
  # The poll is a tenth of a second and the bound is counted in tenths: a read
  # that answers at once — every one of them, when the installation is healthy —
  # would otherwise cost this check a whole second each, and the owner watches
  # the check rather than the clock.
  local pid=$! ticks=0 max=$((secs * 10))
  while kill -0 "$pid" 2>/dev/null; do
    [ "$ticks" -lt "$max" ] || break
    sleep 0.1
    ticks=$((ticks + 1))
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
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check211.sh --sandbox <a sandbox repository checkout> --yes — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy the run's own output to the terminal"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the run's output"
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
    stop "${marker} is set, so this is a coding agent's session: Check 211 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

guard_belong() {
  case "${ACCOUNT:-}${PROJECT:-}${REGION}" in
  *belong*) stop "the account, project or region this check would use names belong: this check is never run there" ;;
  esac
  fact belong PASS "the project this check reads is ${PROJECT:-the one the sandbox checkout belongs to} and the account it runs as is yours; nothing here names belong"
}

# version_at_least HAVE NEED: 0 when HAVE is a release number at or above NEED,
# 1 when it is a release number below it, 2 when HAVE is not a release number at
# all (a dev build, a commit) — the third answer is why this is not a comparison
# alone: a build that names no version is decided by the feature probe below and
# never by a guess at what "dev" sorts as.
version_at_least() { # version_at_least HAVE NEED
  awk -v have="$1" -v need="$2" 'BEGIN {
    n = split(have, h, ".")
    m = split(need, k, ".")
    for (i = 1; i <= n; i++) {
      if (h[i] !~ /^[0-9]+$/) exit 2
    }
    for (i = 1; i <= (n > m ? n : m); i++) {
      a = (i <= n ? h[i] + 0 : 0)
      b = (i <= m ? k[i] + 0 : 0)
      if (a > b) exit 0
      if (a < b) exit 1
    }
    exit 0
  }'
}

# step_release: the gate every check of a released feature carries. The judge
# run ships in 0.24.0 — `evaluateSince` is that release, so an older job image
# refuses the launch outright — and both probes are offline: `fugaro version`,
# and the usage line `fugaro run` prints, whose `--recipe` flag names the
# catalog's recipes as THIS build has them. The `ab-judge` recipe is registered
# by ee/register, so a core build's catalog does not carry it and its usage line
# does not name it.
step_release() {
  local v feature=no
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing}"
  if "$FUGARO_BIN" run --help 2>/dev/null | grep -q -- 'ab-judge'; then feature=yes; fi
  if [ "$feature" = no ]; then
    fact release FAIL "this fugaro's own 'run --help' does not name the 'ab-judge' recipe, so this build carries no judge: the recipe is registered by the Subscription's ee/register, and a core build's catalog has no such name (D522). Check 211 needs release ${RELEASE} (docs/releases/v0.24.0.md) or later, built with the features it ships. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again on the build it leaves you"
    stop "the installed fugaro carries no judge: this check needs release ${RELEASE}"
  fi
  local rc=0
  version_at_least "$v" "$RELEASE" || rc=$?
  case "$rc" in
  0)
    fact release PASS "fugaro ${v} is release ${RELEASE} or later and its own usage line names the ab-judge recipe: this build carries the judge run, its step and its manifest, and the job image gate (evaluateSince ${RELEASE}) lets the launch through"
    ;;
  1)
    fact release FAIL "fugaro ${v} predates release ${RELEASE}, the release the judge run ships in: a launch of it reaches a job whose runner refuses a mode: evaluate task, and the rows below would measure a build nobody shipped this for. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again"
    stop "the installed fugaro is ${v}, older than ${RELEASE}"
    ;;
  *)
    fact release PASS "fugaro reports '${v}', which is not a release number (a dev build or a commit): the feature probe decides, and the build's own usage line names the ab-judge recipe, so this build carries the judge"
    ;;
  esac
}

# step_bound: the spend this check is about to commit, said before it commits
# it. `--yes` is the owner's answer to it; a dry run prints the same sentence
# and asks for nothing.
step_bound() {
  fact bound PASS "ONE judge run over up to ${JUDGE_MANIFEST_MAX} of the sandbox repository's unjudged pairs — two model calls per pair on ab.judge_model, so up to $((JUDGE_MANIFEST_MAX * 2)) calls on the project's model credential and budget — and nothing else. The run publishes no branch and opens no pull request, so there is nothing to close afterwards; --yes is required to launch"
}

# ---------------------------------------------------------------- the steps
# step_sandbox: the premise. The project comes from the checkout itself
# (`fugaro doctor --json`), so the check reads the project you actually work in
# rather than one it guessed, and every launch and bucket read below is that
# project's.
step_sandbox() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox project)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the judge run this check launches is launched from one"
  fi
  say "reading the installation: fugaro doctor --json, in ${SANDBOX}"
  run_bounded "$DOCTOR_TIMEOUT" doctor "$SANDBOX" "$FUGARO_BIN" doctor --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured sandbox "fugaro doctor --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): this check cannot read the project this checkout belongs to, and every row below is about that project. Its output is in $SCRATCH/doctor.out and $SCRATCH/doctor.err"
    return 0
  fi
  if ! read_json doctor "$SCRATCH/doctor.out" "$SCRATCH/doctor.txt"; then
    unmeasured sandbox "fugaro doctor --json printed a document this check could not read: what it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  PROJECT=$(field "$SCRATCH/doctor.txt" gcp_project)
  if [ -z "$PROJECT" ]; then
    unmeasured sandbox "fugaro doctor --json answered without a GCP project: this check cannot say which project the sandbox checkout belongs to, and the rows below are about the project, the runs bucket and the judge run's own prefix. What it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  case "$PROJECT" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${PROJECT}: this check is never run there" ;;
  esac
  fact sandbox PASS "${SANDBOX} is a Fugaro repository's checkout, and it belongs to ${PROJECT}: the judge run is launched from this checkout and its objects are read in this project's runs bucket"
}

# step_recipe: the judge recipe this build carries, read from the command that
# lists recipes — `fugaro recipes ls --json` — rather than from the usage line
# alone: the row says which recipe the launch below resolves, that it is
# launchable, and that its one step is the registered `ab_judge` kind.
step_recipe() {
  if [ -z "$PROJECT" ]; then
    unmeasured recipe "there is no project to list the recipes of: the row above says why"
    return 0
  fi
  say "reading the catalog: fugaro recipes ls --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" recipes "$SANDBOX" "$FUGARO_BIN" recipes ls --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured recipe "fugaro recipes ls --json did not answer (exit ${BOUNDED_STATUS}): this check cannot say whether this build carries the ab-judge recipe, and a launch without it would be refused after it was paid for. Its output is in $SCRATCH/recipes.out and $SCRATCH/recipes.err"
    return 0
  fi
  if ! read_json recipes "$SCRATCH/recipes.out" "$SCRATCH/recipes.txt"; then
    unmeasured recipe "fugaro recipes ls --json printed a document this check could not read: what it printed is in $SCRATCH/recipes.out"
    return 0
  fi
  local row
  row=$(awk -F'\t' '$1 == "recipe" && $2 == "ab-judge" { print; exit }' "$SCRATCH/recipes.txt")
  if [ -z "$row" ]; then
    fact recipe FAIL "fugaro recipes ls --json lists $(field "$SCRATCH/recipes.txt" count) recipes and none of them is ab-judge: this build's catalog has no judge recipe, so 'fugaro run --recipe ab-judge' below would be refused as an unknown recipe. The judge recipe is registered by the Subscription's ee/register (A5.2); check the build is the one 'fugaro update' leaves you and that this installation carries the features it ships"
    return 0
  fi
  local source launchable steps
  source=$(rcol "$row" 3)
  launchable=$(rcol "$row" 4)
  steps=$(rcol "$row" 5)
  if [ "$launchable" != true ] || [ "$steps" != "ab_judge" ]; then
    fact recipe FAIL "fugaro recipes ls --json carries ab-judge from ${source:-an unknown source} but it is not the judge this check launches: launchable=${launchable:-false} and its steps are '${steps:-none}' where the judge recipe is one registered ab_judge step (A5.2, ee/ab/recipe/ab-judge.yaml). A recipe of that name from the repository or the project shadows the catalog's, and a shadow that is not the judge measures something else"
    return 0
  fi
  PREMISE_RECIPE_OK=yes
  fact recipe PASS "this build's catalog carries ab-judge from ${source:-the catalog}, launchable, with the one registered ab_judge step: 'fugaro run --recipe ab-judge' below resolves to the judge this check measures (A5.2)"
}

# step_layer: the other half of the premise. `ab.judge_model` lives in the
# published project layer's `ab:` block (D553), and a project that publishes
# none has nothing to judge with: the launcher refuses the launch with
# no-judge-model rather than starting a run that cannot call anything.
step_layer() {
  if [ -z "$PROJECT" ]; then
    unmeasured layer "there is no project to read the layer for: the row above says why"
    return 0
  fi
  say "reading the published project layer: fugaro config layer --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" layer "$SANDBOX" "$FUGARO_BIN" config layer --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured layer "fugaro config layer --json did not answer (exit ${BOUNDED_STATUS}): the published project layer is where ab.judge_model lives, and without it there is no judge model to launch a run for. Its output is in $SCRATCH/layer.out and $SCRATCH/layer.err — a project with no published layer answers here too, and then this check's premise is one 'fugaro config publish' away"
    return 0
  fi
  if ! read_json layer "$SCRATCH/layer.out" "$SCRATCH/layer.txt"; then
    unmeasured layer "fugaro config layer --json printed a document this check could not read: what it printed is in $SCRATCH/layer.out"
    return 0
  fi
  local where judge keys
  where=$(field "$SCRATCH/layer.txt" where)
  judge=$(field "$SCRATCH/layer.txt" judge_model)
  keys=$(field "$SCRATCH/layer.txt" ab_keys)
  if [ "$(field "$SCRATCH/layer.txt" has_ab)" != true ]; then
    fact layer FAIL "the published project layer carries no top-level ab: block (its where is ${where:-unknown}); its top-level keys are $(field "$SCRATCH/layer.txt" top_keys), and this check read the whole layer text for the block. A project with no ab: block names no judge model (D550, D553), so the launch below would be refused with no-judge-model and nothing would be judged. Add judge_model to the project's layer file and publish it with 'fugaro config publish'"
    return 0
  fi
  if [ -z "$judge" ]; then
    fact layer FAIL "the published project layer (${where:-unknown}) has an ab: block whose keys are ${keys:-none} and it names no judge_model: a judge run has nothing to call, and the launcher refuses the launch rather than starting one to fail (D553, ReasonNoJudgeModel). Name the model the judge run calls in the layer's ab: block and publish the layer again"
    return 0
  fi
  PREMISE_LAYER_OK=yes
  fact layer PASS "the published project layer (${where:-unknown}, generation $(field "$SCRATCH/layer.txt" generation), sha256 $(field "$SCRATCH/layer.txt" sha256)) names ab.judge_model: ${judge}, so the judge run below has a model to call. This is a text read of the layer's own ab: block — no command in this release resolves the block to a value"
}

# step_launch: the one run. The plan's sentence is one `fugaro run --recipe
# ab-judge`, and the launcher's own `--json` names the run it started; the ref
# below is read from `fugaro runs ls` rather than built here, because the
# objects the rows read are at the key that row carries.
step_launch() {
  if [ "$PREMISE_RECIPE_OK" != yes ] || [ "$PREMISE_LAYER_OK" != yes ]; then
    unmeasured launch "nothing was launched: a premise row above did not establish the state this launch is a reading of (this build's judge recipe, or the layer's judge model), and a judge run launched without it would spend the sandbox's money on a reading nobody could grade. Settle that row and run this check again"
    return 0
  fi
  say "launching the judge run: fugaro run --repo ${SANDBOX} --recipe ab-judge --json"
  run_bounded "$LAUNCH_TIMEOUT" launch "$SANDBOX" "$FUGARO_BIN" run --repo "$SANDBOX" --recipe ab-judge --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured launch "the judge launch did not answer (exit ${BOUNDED_STATUS}): the run was refused or the launch timed out, and nothing below can be read without it. Its output is in $SCRATCH/launch.out and $SCRATCH/launch.err — the launcher refuses a second judge run while one has not finished (D569, 'a judge run is already running'), so read the error there before running this check again"
    return 0
  fi
  if ! read_json launch "$SCRATCH/launch.out" "$SCRATCH/launch.txt"; then
    unmeasured launch "fugaro run --json printed a document this check could not read: what it printed is in $SCRATCH/launch.out"
    return 0
  fi
  RUN_ID=$(field "$SCRATCH/launch.txt" run_id)
  if [ -z "$RUN_ID" ]; then
    unmeasured launch "fugaro run --json answered without a run id: this check cannot say which run it launched, and every row below is read at that run's own prefix. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  LAUNCHED=1
  say "launched the judge run ${RUN_ID}; waiting for it to end (up to ${RUN_TIMEOUT}s)"
  fact launch PASS "one judge run launched from ${SANDBOX}: ${RUN_ID} ($(field "$SCRATCH/launch.txt" status), recipe $(field "$SCRATCH/launch.txt" recipe)), on the sandbox project's own budget. It is the only run this check starts, and its objects are read at runs/<the slug>/${RUN_ID}/ below"
}

# step_end: the run's own row, read from the runs list. Two things are read
# here and nowhere else: that the run ENDED — an input read from a run still
# going would be a reading of half a manifest — and that it published nothing,
# which is A5.2's TestJudgeRunPublishesNothing read from the installation rather
# than from the test.
step_end() {
  if [ "$LAUNCHED" != 1 ]; then
    unmeasured run-end "there is no judge run to wait for: the launch row above says why"
    unmeasured publishes-nothing "there is no judge run to read a branch and a pull request off: the launch row above says why"
    return 0
  fi
  local waited=0 row=""
  while [ "$waited" -lt "$RUN_TIMEOUT" ]; do
    run_bounded "$CLI_TIMEOUT" rows "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$SANDBOX" --json --limit "$LS_LIMIT"
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
      unmeasured run-end "fugaro runs ls --json did not answer (exit ${BOUNDED_STATUS}): this check cannot say whether the judge run it launched has ended. Its output is in $SCRATCH/rows.out and $SCRATCH/rows.err"
      unmeasured publishes-nothing "the runs list could not be read, so the judge run's own row was not read either: the row above says why"
      return 0
    fi
    if ! read_json rows "$SCRATCH/rows.out" "$SCRATCH/rows.txt"; then
      unmeasured run-end "fugaro runs ls --json printed a document this check could not read: what it printed is in $SCRATCH/rows.out"
      unmeasured publishes-nothing "the runs list could not be read, so the judge run's own row was not read either: the row above says why"
      return 0
    fi
    row=$(rows_row "$SCRATCH/rows.txt" "$RUN_ID")
    if [ -n "$row" ] && [ "$(rcol "$row" 4)" = true ]; then break; fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ -z "$row" ]; then
    unmeasured run-end "the judge run ${RUN_ID} is not in 'fugaro runs ls --repo ${SANDBOX}' after ${RUN_TIMEOUT}s: this check cannot read the run it launched, and every row below is read at its prefix. The list is in $SCRATCH/rows.out"
    unmeasured publishes-nothing "the judge run is not in the runs list, so its own row was not read: the row above says why"
    return 0
  fi
  if [ "$(rcol "$row" 4)" != true ]; then
    unmeasured run-end "the judge run ${RUN_ID} had not ended after ${RUN_TIMEOUT}s (status $(rcol "$row" 3)): a judge run makes two model calls per pair, and this one was still going when the check gave up. The teardown below cancels it and reads it back; raise CHECK211_RUN_TIMEOUT_SECONDS and run the check again to read its objects"
    unmeasured publishes-nothing "the judge run had not ended, so nothing about what it published was read: the row above says why"
    return 0
  fi
  REF=$(rcol "$row" 6)
  if [ -z "$REF" ]; then
    unmeasured run-end "the judge run's row carries no run ref (the '<slug>/<run id>' key its objects are stored under), so the rows below have no prefix to read. The list is in $SCRATCH/rows.out"
    unmeasured publishes-nothing "the judge run's row carries no ref, so its branch and pull request were not read: the row above says why"
    return 0
  fi
  fact run-end PASS "the judge run ${RUN_ID} has ended (status $(rcol "$row" 3), outcome $(rcol "$row" 9)) after ${waited}s, and its objects are under runs/${REF}/ — the prefix the rows below read. A judge run's own manifest lists the pairs it took, so the reading below is of a run that finished its work"
  local branch pr lock
  branch=$(rcol "$row" 7)
  pr=$(rcol "$row" 8)
  # An evaluate-mode run writes r.rec.Branch = "fugaro/<its own run id>" and
  # never pushes it: that string is the branch LOCK's key and nothing else
  # (internal/runner's runEvaluateMode, whose arm says so; review mode's is the
  # same), so a healthy judge run's own row carries it and a row that graded any
  # branch as a published one would fail every correct release. What such a run
  # may not carry is a pull request, or a branch that is not that key.
  lock="fugaro/${RUN_ID}"
  if [ "${pr:-0}" != 0 ]; then
    fact publishes-nothing FAIL "the judge run's row carries pull request ${pr}: a judge run publishes nothing — it holds no git credential, pushes no branch, opens no pull request and comments on nothing (A5.2, TestJudgeRunPublishesNothing) — and a judge run with one is a finding about the release rather than about the sandbox. The row is in $SCRATCH/rows.out"
    return 0
  fi
  if [ -n "$branch" ] && [ "$branch" != "$lock" ]; then
    fact publishes-nothing FAIL "the judge run's row carries branch '${branch}', which is not '${lock}' — the branch lock's key this run's own mode writes and never pushes (internal/runner's runEvaluateMode) — so it is a branch the run pushed, and a judge run publishes nothing: no branch of its own, no pull request, no comment (A5.2, TestJudgeRunPublishesNothing). The row is in $SCRATCH/rows.out"
    return 0
  fi
  fact publishes-nothing PASS "the judge run's row carries no pull request and no branch of its own (branch '${branch:-none}': an evaluate-mode run writes '${lock}' as the branch lock's key alone and never pushes it, and any other name would have been a push), so the run judged and wrote its own objects and pushed nothing anywhere (outcome $(rcol "$row" 9)) — the teardown below therefore has no branch to delete and no pull request to close"
}

# bucket_cat KEY DEST: one object out of the runs bucket at the judge run's own
# prefix. The prefix is the ref the runs list carries, so a key read here is a
# key the release itself named.
bucket_cat() { # bucket_cat KEY DEST
  run_bounded "$CLI_TIMEOUT" "$2" "$SANDBOX" gcloud storage cat "gs://fugaro-runs-${PROJECT}/${1}" --project="${PROJECT}"
  [ "$BOUNDED_TIMED_OUT" = no ] && [ "$BOUNDED_STATUS" = 0 ]
}

# step_prefix: the two objects the run leaves under its own prefix, read before
# anything is graded: the launcher's manifest (A5.2) and the verdicts the run
# wrote. A judge run that ended without them is a run whose work is nowhere,
# and every row below is read through them.
step_prefix() {
  if [ -z "$REF" ]; then
    unmeasured prefix "there is no judge run to read a prefix of: a row above says why"
    return 0
  fi
  local missing="" found=""
  if bucket_cat "runs/${REF}/ab/manifest.json" manifest; then
    found="${found}${found:+, }ab/manifest.json"
  else
    missing="${missing}${missing:+, }ab/manifest.json"
  fi
  if bucket_cat "runs/${REF}/ab/verdicts.json" verdicts; then
    found="${found}${found:+, }ab/verdicts.json"
  else
    missing="${missing}${missing:+, }ab/verdicts.json"
  fi
  if [ -n "$missing" ]; then
    fact prefix FAIL "the judge run's own prefix (runs/${REF}/) does not hold ${missing}: a judge run writes its manifest and its verdicts under its OWN prefix and nowhere else (A5.2, D559), and the rows below are read through them. What was found is ${found:-nothing}; the reads' errors are in $SCRATCH/manifest.err and $SCRATCH/verdicts.err"
    return 0
  fi
  if ! read_json manifest "$SCRATCH/manifest.out" "$SCRATCH/manifest.txt"; then
    fact prefix FAIL "the judge run's manifest at runs/${REF}/ab/manifest.json is not a document this check could read: what it holds is in $SCRATCH/manifest.out"
    return 0
  fi
  if ! read_json verdicts "$SCRATCH/verdicts.out" "$SCRATCH/verdicts.txt"; then
    fact prefix FAIL "the judge run's verdicts at runs/${REF}/ab/verdicts.json are not a document this check could read: what they hold is in $SCRATCH/verdicts.out"
    return 0
  fi
  JUDGE_MODEL=$(field "$SCRATCH/manifest.txt" judge_model)
  fact prefix PASS "the judge run's own prefix runs/${REF}/ holds ${found} — the launcher's manifest (version $(field "$SCRATCH/manifest.txt" version)) and the run's verdicts (version $(field "$SCRATCH/verdicts.txt" version)) — and nothing below reads a key outside it"
}

# step_pairs: the manifest's pairs, and the two arms each one hands the judge.
# The plan's premise is "at least five known pairs": the launcher lists the
# repository's complete, unjudged pairs of its last fortnight (judgePairWindow),
# so a manifest with fewer than MIN_PAIRS in it is a sandbox whose A/B work has
# not happened yet, not a finding about the judge — the row says which, and the
# input row below grades whatever pairs there are.
step_pairs() {
  if [ ! -s "$SCRATCH/manifest.txt" ]; then
    unmeasured pairs "there is no manifest to read pairs from: a row above says why"
    return 0
  fi
  local count pairs judge incomplete="" pair_id side run_id branch model key arms
  pairs=$(field "$SCRATCH/manifest.txt" pairs)
  judge=$(field "$SCRATCH/manifest.txt" judge_model)
  if [ -z "$judge" ]; then
    fact pairs FAIL "the manifest names no judge_model: the model a judge run calls comes from the layer's ab.judge_model through JudgeAdmit, and the manifest records it for the verdicts (D553). The manifest is in $SCRATCH/manifest.out"
    return 0
  fi
  if [ "${pairs:-0}" -lt "$MIN_PAIRS" ]; then
    fact pairs FAIL "the launcher's manifest holds ${pairs:-0} pair(s) and this check's premise is ${MIN_PAIRS} (the plan's 'at least five known pairs'): the launcher takes the repository's complete, unjudged pairs of its last fortnight (A5.2, D569), so this is a sandbox whose sampled pairs are fewer than the reading needs — or one whose pairs a previous judge run already judged. Sample more pairs (Check 210 launches sandbox runs until one is sampled) and run this check again"
    return 0
  fi
  count=0
  while IFS=$'\t' read -r tag pair_id side run_id branch model key; do
    [ "$tag" = arm ] || continue
    count=$((count + 1))
    if [ -z "$run_id" ] || [ -z "$model" ] || [ -z "$key" ]; then
      incomplete="${incomplete}${incomplete:+, }${pair_id}/${side}"
    fi
  done <"$SCRATCH/manifest.txt"
  if [ -n "$incomplete" ]; then
    fact pairs FAIL "the manifest's arms are incomplete for ${incomplete}: each arm carries the run that wrote the patch, the branch it worked on, the model it ran and the key of its patch object (A5.2's judgeArm), and the bundle those four are redacted from is what the judge is handed (A5.1). The manifest is in $SCRATCH/manifest.out"
    return 0
  fi
  PAIRS_OK=yes
  fact pairs PASS "the launcher's manifest hands the judge ${pairs} pair(s) — ${count} arms, each naming its run, its branch, its model and its patch object — judged by ${judge}: this is the plan's 'at least five known pairs', and the models the arms differ in are what the input row below scans the stored judge input for"
}

# step_verdicts: the run's own verdicts against the manifest it was handed: one
# entry per pair, in the manifest's order, with the judge model the manifest
# named, and a stored input key for every pair it judged. A verdict for a pair
# the manifest does not hold, or a judged pair whose input key is empty, is a
# reading nobody can join to the pair's two arms (A5.4).
step_verdicts() {
  if [ "$PAIRS_OK" != yes ]; then
    unmeasured verdicts "there is no manifest to read the verdicts against: the pairs row above says why"
    return 0
  fi
  local pairs judged skipped unknown="" noinput="" pair_id
  pairs=$(field "$SCRATCH/verdicts.txt" pairs)
  judged=$(field "$SCRATCH/verdicts.txt" judged)
  skipped=$(field "$SCRATCH/verdicts.txt" skipped)
  if [ "$pairs" != "$(field "$SCRATCH/manifest.txt" pairs)" ]; then
    fact verdicts FAIL "the verdicts carry ${pairs} entries where the manifest hands the judge $(field "$SCRATCH/manifest.txt" pairs) pairs: a judge run writes one entry per pair of its manifest, in the manifest's order, whether the pair was judged or skipped (A5.2, D559), and a reader joining the two objects by pair ID would read a pair nobody decided. Both objects are in $SCRATCH/manifest.out and $SCRATCH/verdicts.out"
    return 0
  fi
  if [ "$(field "$SCRATCH/verdicts.txt" judge_model)" != "$JUDGE_MODEL" ]; then
    fact verdicts FAIL "the verdicts name judge_model '$(field "$SCRATCH/verdicts.txt" judge_model)' where the manifest names '${JUDGE_MODEL}': the judge model is recorded on the verdict as well as on the manifest (D553), and two objects disagreeing about which model judged the pairs are two objects a reader cannot join. Both are in $SCRATCH/manifest.out and $SCRATCH/verdicts.out"
    return 0
  fi
  local line=""
  while IFS= read -r line; do
    [ "$(row_tag "$line")" = verdict ] || continue
    pair_id=$(row_field "$line" 2)
    skip=$(row_field "$line" 5)
    input=$(row_field "$line" 6)
    if [ -z "$(awk -F'\t' -v id="$pair_id" '$1 == "arm" && $2 == id { print $2; exit }' "$SCRATCH/manifest.txt")" ]; then
      unknown="${unknown}${unknown:+, }${pair_id}"
      continue
    fi
    if [ -z "$input" ] && [ -z "$skip" ]; then
      noinput="${noinput}${noinput:+, }${pair_id}"
    fi
  done <"$SCRATCH/verdicts.txt"
  if [ -n "$unknown" ]; then
    fact verdicts FAIL "the verdicts carry entries for pairs the manifest does not hold: ${unknown}. A verdict is joined to the pair's two records by its pair ID (A5.4), so an entry for a pair that was never handed to this run is a verdict no reader can score"
    return 0
  fi
  if [ -n "$noinput" ]; then
    fact verdicts FAIL "${noinput} was neither judged nor skipped: the entry carries no stored input key and no skip reason, and a pair in neither state is one this check cannot say what the judge saw of. Every judged pair stores its input under the run's own prefix and the verdict names it (A5.3's object, verdict.Input); a pair the launcher could not judge carries one of the skip words instead. The verdicts are in $SCRATCH/verdicts.out"
    return 0
  fi
  if [ "${judged:-0}" = 0 ]; then
    fact verdicts FAIL "the judge run judged none of its $(field "$SCRATCH/manifest.txt" pairs) pair(s): every entry was skipped (${skipped} of them), so no stored judge input exists and U1 has nothing to read. A run that skips every pair says why per pair in its verdicts — truncated, too-large, unreadable and the like (A5.2) — and the verdicts are in $SCRATCH/verdicts.out"
    return 0
  fi
  VERDICTS_OK=yes
  fact verdicts PASS "the run's verdicts carry one entry per manifest pair (${pairs}), all of them naming pairs the manifest handed it, judged by the same judge model the manifest names (${JUDGE_MODEL}), and ${judged} of them judged with a stored input key (${skipped} skipped): the stored inputs the rows below grade are named by the verdicts themselves"
}

# step_inputs: the reading. U1 is graded over every stored judge input the
# verdicts name — the object the judge saw, read from the run's own prefix — and
# U2's numbers are taken from the same read. The scan is A5.1's own vocabulary:
# either arm's run ID, either arm's branch, the pair ID, the arms' models with
# the words their names are built from, and a run-ID-shaped string.
step_inputs() {
  if [ "$VERDICTS_OK" != yes ]; then
    unmeasured input-blind "there are no verdicts naming the stored judge inputs, so nothing was scanned: the row above says why"
    fact judge-label-guess INFO "not recorded: no stored judge input was read, and the row above says why"
    fact judge-pass-agreement INFO "not recorded: no verdicts were read, and the row above says why"
    return 0
  fi
  local total=0 read_n=0 chars=0 answers=0 named=0 hits="" chars_here=0
  local pair_id input_key row passes n
  local line=""
  while IFS= read -r line; do
    [ "$(row_tag "$line")" = verdict ] || continue
    pair_id=$(row_field "$line" 2)
    input_key=$(row_field "$line" 6)
    [ -n "$input_key" ] || continue
    total=$((total + 1))
    if ! bucket_cat "runs/${REF}/${input_key}" "input-${total}"; then
      hits="${hits}${hits:+; }the stored input ${input_key} of pair ${pair_id} could not be read out of the bucket"
      continue
    fi
    if ! python3 "$SCRATCH/read.py" judged "$SCRATCH/input-${total}.out" "$SCRATCH/manifest.out" "$pair_id" >"$SCRATCH/input-${total}.txt" 2>"$SCRATCH/input-${total}.err"; then
      hits="${hits}${hits:+; }the stored input ${input_key} of pair ${pair_id} is not a document this check could read (what it holds is in $SCRATCH/input-${total}.out)"
      continue
    fi
    read_n=$((read_n + 1))
    chars_here=$(field "$SCRATCH/input-${total}.txt" chars)
    chars=$((chars + ${chars_here:-0}))
    while IFS=$'\t' read -r kind idx where what needle line; do
      case "$kind" in
      pass)
        answers=$((answers + 1))
        ;;
      hit)
        if [ "$where" = answer ]; then
          named=$((named + 1))
          continue
        fi
        hits="${hits}${hits:+; }pair ${pair_id} pass ${idx}: the ${where} carries the ${what} '${needle}'"
        ;;
      esac
    done <"$SCRATCH/input-${total}.txt"
  done <"$SCRATCH/verdicts.txt"
  if [ "$read_n" = 0 ]; then
    unmeasured input-blind "not one of the ${total} stored judge inputs the verdicts name could be read: the reads' output is in $SCRATCH/input-*.out and their errors in $SCRATCH/input-*.err, and the reasons are: ${hits:-none given}"
    fact judge-label-guess INFO "not recorded: no stored judge input was read, so the judge's answers were not read either"
    fact judge-pass-agreement INFO "not recorded: the verdicts were read but no stored input was, so no reading of the two passes was made"
    return 0
  fi
  if [ -n "$hits" ]; then
    fact input-blind FAIL "the stored judge input names something it must not: ${hits}. A5.1's bundle is what keeps this from happening — it REFUSES a diff carrying a run ID, a branch or the pair ID and REDACTES every model word to one placeholder — and a judge run over real pairs is where that promise is read rather than asserted (TestBundleCarriesNoIdentifier). ${read_n} stored input(s) were read (${chars} characters of system text and prompt); the objects themselves are under runs/${REF}/ab/inputs/ and the read is in $SCRATCH/input-*.out"
  else
    fact input-blind PASS "all ${read_n} stored judge input(s) the verdicts name — ${chars} characters of system text and prompt, every pass of every judged pair — carry no run ID of either arm, no branch, no pair ID, no arm's model (its ID, its name or a word either is built from — the vocabulary A5.1 redacts with) and no run-ID-shaped string. The judge's own model is not graded here: A5.1 is handed the two arms' models alone, and the stored input names the judge model in its own judge_model field. This is U1 read from the object the judge was handed rather than from the anonymiser's own test"
  fi
  local challenger=0 champion=0 tie=0 winner=""
  while IFS= read -r line; do
    [ "$(row_tag "$line")" = verdict ] || continue
    winner=$(row_field "$line" 3)
    case "$winner" in
    challenger) challenger=$((challenger + 1)) ;;
    champion) champion=$((champion + 1)) ;;
    tie) tie=$((tie + 1)) ;;
    esac
  done <"$SCRATCH/verdicts.txt"
  # U2 is the plan's rate and never a pass or a fail: it is recorded here with
  # the gap the release leaves, because the plan's own question ("which model
  # wrote A?") has no surface a check can ask — the judge's call is fixed by
  # ee/ab/judge.go, no command asks a chosen model a question, and a recipe
  # cannot pin one.
  fact judge-label-guess INFO "the judge named a model in ${named} of the ${answers} answers it left, over ${read_n} stored input(s): that is the rate this release can record for U2, and it is recorded and never graded. The plan's own question — a judge asked 'which model wrote A?' — cannot be asked here: the judge's two calls per pair are fixed (two diffs, one closed vocabulary, no question of the check's own), no command in this release asks a chosen model a question, and a recipe cannot pin a model. So the row reports what the judge's own stored answers do say: it named a model ${named} time(s). Of the $(field "$SCRATCH/verdicts.txt" judged) judged pair(s) its verdicts named the challenger's arm ${challenger} time(s), the champion's ${champion} and a tie ${tie} — a split that is a reading of the judge's own answers and not of its ability to tell the two models apart"
  local agree=0 disagree=0 pairs_n=0 arm1="" arm=""
  while IFS= read -r line; do
    [ "$(row_tag "$line")" = pass ] || continue
    idx=$(row_field "$line" 3)
    arm=$(row_field "$line" 6)
    if [ "$idx" = 1 ]; then
      arm1="$arm"
      pairs_n=$((pairs_n + 1))
      continue
    fi
    if [ "$arm" = "$arm1" ] && [ -n "$arm" ]; then
      agree=$((agree + 1))
    else
      disagree=$((disagree + 1))
    fi
  done <"$SCRATCH/verdicts.txt"
  fact judge-pass-agreement INFO "the judge's two swapped passes named the same arm in ${agree} of the ${pairs_n} pair(s) it read and disagreed in ${disagree}: the order is swapped on the second pass and only an arm both passes agree on is decided, so a disagreement is a tie by the design (D552) and not a finding. This is a rate, recorded and never graded"
}

# --------------------------------------------------------------- the teardown
# restore: what this check changed, said rather than assumed. It creates one
# judge run and no branch and no pull request, so there is nothing to close and
# nothing to delete — the one thing that must not outlive the check is a judge
# run that has not ended, because a run still going writes its verdicts after a
# teardown that read only what it had left so far. The run's own objects stay in
# the bucket: they are what the report reads (A5.4), and deleting them would
# delete the reading.
restore() {
  RESTORE_VERDICT=PASS
  local row status="" cancel_status=""
  if [ "$LAUNCHED" != 1 ] || [ -z "$PROJECT" ]; then
    RESTORE_NOTE="nothing was created: no judge run was launched, no branch was pushed and no pull request was opened, and the scratch directory held the commands' output"
    return 0
  fi
  run_bounded "$CLI_TIMEOUT" teardown "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$SANDBOX" --json --limit "$LS_LIMIT"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ] || ! read_json rows "$SCRATCH/teardown.out" "$SCRATCH/teardown.txt"; then
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="the judge run this check launched is ${RUN_ID} and 'fugaro runs ls --json' could not be read back (exit ${BOUNDED_STATUS}; its output is in $SCRATCH/teardown.out), so whether it is still going is unknown. Read it with 'fugaro runs show ${RUN_ID}' and stop it with 'fugaro cancel ${RUN_ID} --now' if it is; a judge run leaves no branch and no pull request, so there is nothing else to settle"
    return 0
  fi
  row=$(rows_row "$SCRATCH/teardown.txt" "$RUN_ID")
  if [ -z "$row" ]; then
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="the judge run this check launched (${RUN_ID}) is not in 'fugaro runs ls --repo ${SANDBOX}' at all, so whether it is still going is unknown. Read it with 'fugaro runs show ${RUN_ID}' and stop it with 'fugaro cancel ${RUN_ID} --now' if it is; running this check again does not settle it, because the id of the run it launched is removed with its scratch directory"
    return 0
  fi
  if [ "$(rcol "$row" 4)" != true ]; then
    run_bounded "$CANCEL_TIMEOUT" teardown-cancel "$SANDBOX" "$FUGARO_BIN" cancel "$RUN_ID" --now --json
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
      RESTORE_VERDICT=FAIL
      RESTORE_NOTE="the judge run ${RUN_ID} had not ended and 'fugaro cancel ${RUN_ID} --now' exited ${BOUNDED_STATUS} (its output is in $SCRATCH/teardown-cancel.out), so it may still be going. Stop it with 'fugaro cancel ${RUN_ID} --now' at your terminal; a judge run leaves no branch and no pull request, so there is nothing else to settle"
      return 0
    fi
    status=$(python3 -c 'import json,sys; print(((json.load(open(sys.argv[1])) or {}).get("status") or ""))' "$SCRATCH/teardown-cancel.out" 2>/dev/null || true)
    cancel_status=" (cancel answered '${status:-no status}')"
    local waited=0
    while [ "$waited" -lt "$CANCEL_TIMEOUT" ]; do
      run_bounded "$CLI_TIMEOUT" teardown "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$SANDBOX" --json --limit "$LS_LIMIT"
      row=""
      if [ "$BOUNDED_STATUS" = 0 ]; then
        read_json rows "$SCRATCH/teardown.out" "$SCRATCH/teardown.txt" || true
        row=$(rows_row "$SCRATCH/teardown.txt" "$RUN_ID")
      fi
      if [ -n "$row" ] && [ "$(rcol "$row" 4)" = true ]; then break; fi
      sleep "$POLL_SECONDS"
      waited=$((waited + POLL_SECONDS))
    done
    if [ -z "$row" ] || [ "$(rcol "$row" 4)" != true ]; then
      RESTORE_VERDICT=FAIL
      RESTORE_NOTE="the judge run ${RUN_ID} was cancelled${cancel_status} and the runs list still reads it as going after ${CANCEL_TIMEOUT}s. Stop it again with 'fugaro cancel ${RUN_ID} --now' at your terminal: a judge run still going writes its verdicts after this check is over, and a teardown that reported PASS over that would be reading a run that had not finished"
      return 0
    fi
  fi
  RESTORE_NOTE="the one run this check launched (${RUN_ID}) has ended${cancel_status}, and it left no branch to delete and no pull request to close — a judge run publishes nothing. Its manifest, its verdicts and its stored inputs stay under runs/${REF}/ in the bucket, which is where the report reads them (A5.4) and where the rows above were read; the scratch directory held the commands' output"
}

cleanup() {
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
  # INT, TERM and HUP are NOT reset here: cleanup is about to read facts and
  # remove a directory, and a second Ctrl-C during it would kill this shell half
  # done.
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
  say "CHECK 211 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [offline] fugaro version and fugaro run --help — the release ${RELEASE} gate: the build's own usage line must name the ab-judge recipe"
  say "  [network, read] fugaro doctor --json in ${SANDBOX} — the project this checkout belongs to"
  say "  [network, read] fugaro recipes ls --json in ${SANDBOX} — the catalog's ab-judge recipe, launchable, one ab_judge step"
  say "  [network, read] fugaro config layer --json in ${SANDBOX} — the published layer's ab: block, which must name judge_model"
  say "  [network, BILLABLE] fugaro run --repo ${SANDBOX} --recipe ab-judge --json — ONE judge run, two model calls per pair on ab.judge_model over up to ${JUDGE_MANIFEST_MAX} pairs"
  say "  [read] fugaro runs ls --repo ${SANDBOX} --json — the judge run's own row: that it ended, that it carries no pull request, and that its branch is the branch lock's key 'fugaro/<the run id>' alone and not a branch of its own"
  say "  [read] gcloud storage cat gs://fugaro-runs-<the project>/runs/<the slug>/<the run>/ab/manifest.json and ab/verdicts.json — the launcher's manifest and the run's verdicts, under the run's own prefix"
  say "  [read] gcloud storage cat of each stored judge input the verdicts name (ab/inputs/<hex>.json) — the object the judge was handed, scanned for U1"
  say "  [WRITE, the teardown] a judge run that has not ended is stopped with fugaro cancel --now and read back as ended. Nothing is deleted: the run's objects are what the report reads"
  for key in release sandbox recipe layer launch run-end publishes-nothing prefix pairs verdicts input-blind; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact judge-label-guess INFO "planned: U2 is a rate the real run records and never grades; --dry-run recorded none"
  fact judge-pass-agreement INFO "planned: the two passes' agreement is a rate the real run records; --dry-run recorded none"
  fact dry-run PASS "the plan was printed and nothing was run: no doctor, no recipe list, no layer read, no launch, no bucket read and no teardown"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version release xtrace agent-session belong bound sandbox
  recipe layer launch run-end publishes-nothing prefix pairs verdicts input-blind
  judge-label-guess judge-pass-agreement restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 211 RESULT\n' >&3
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
  printf 'CHECK 211 RESULT END (exit %s)\n' "$status" >&3
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
MIN_PAIRS_CLI=""
YES_CLI=""
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of a repository of the sandbox project, which is what the judge run is launched from"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --min-pairs)
    [ $# -ge 2 ] || die "--min-pairs needs a count: how many known pairs the judge's manifest must hold for the reading to be the plan's (the default is ${MIN_PAIRS_DEFAULT:-5})"
    MIN_PAIRS_CLI=$2
    shift 2
    ;;
  --yes)
    YES_CLI=1
    shift
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

# A value given as a flag wins over the same value in the environment: the
# environment is how a script or a case sets a knob, and the flag is what the
# owner typed, so a run that quietly used the other one would be spending the
# sandbox's money at a bound its owner did not choose.
SANDBOX=${SANDBOX_CLI:-${CHECK211_SANDBOX:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 211 needs --sandbox: the checkout of a repository of the sandbox project. It is what the judge run is launched from and what its configuration is read through. Usage: bash scripts/live-checks/check211.sh --sandbox <a sandbox repository checkout> --yes"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
MIN_PAIRS_DEFAULT=5
MIN_PAIRS=${MIN_PAIRS_CLI:-${CHECK211_MIN_PAIRS:-$MIN_PAIRS_DEFAULT}}
case "$MIN_PAIRS" in
"" | *[!0-9]*) die "--min-pairs needs a whole number, not '${MIN_PAIRS}'" ;;
esac
[ "$MIN_PAIRS" -ge 1 ] || die "--min-pairs needs at least 1"
RELEASE=${CHECK211_RELEASE:-0.24.0}
# The launcher's own cap on the pairs one judge run takes (ee/ab's
# judgeManifestMax), said in the bound row and in the plan: it is the most
# model calls one judge run can make, and the figure the owner's spend is.
JUDGE_MANIFEST_MAX=${CHECK211_JUDGE_MANIFEST_MAX:-25}
YES=${CHECK211_YES:-$YES_CLI}
DRY_RUN=${CHECK211_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK211_KEEP_SCRATCH:-}
REGION=${CHECK211_REGION:-us-east5}
RUN_TIMEOUT=${CHECK211_RUN_TIMEOUT_SECONDS:-1800}
POLL_SECONDS=${CHECK211_POLL_SECONDS:-15}
CLI_TIMEOUT=${CHECK211_CLI_TIMEOUT_SECONDS:-180}
LAUNCH_TIMEOUT=${CHECK211_LAUNCH_TIMEOUT_SECONDS:-600}
DOCTOR_TIMEOUT=${CHECK211_DOCTOR_TIMEOUT_SECONDS:-300}
# The bound for both halves of the teardown's cancel: the `fugaro cancel --now`
# this check runs on a judge run that has not ended, and the wait for that run's
# row to read as ended afterwards.
CANCEL_TIMEOUT=${CHECK211_CANCEL_TIMEOUT_SECONDS:-300}
# The bound the teardown reads the runs list with: its own launch is the newest
# row, and the window leaves room for the rows the sandbox added beside it.
LS_LIMIT=50
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
  [ -n "$FUGARO_BIN" ] || die "Check 211 needs 'fugaro' on PATH: the check drives the build you have installed, and it is not there"
  scratch_up
  trap on_error ERR
  write_reader
  guard_xtrace
  guard_agent_session
  guard_belong
  step_release
  step_bound
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  if [ -z "$YES" ]; then
    die "Check 211 launches ONE judge run and spends what its model calls cost — up to $((JUDGE_MANIFEST_MAX * 2)) calls on ab.judge_model — so it asks for --yes. Nothing has been launched. Type: bash scripts/live-checks/check211.sh --sandbox ${SANDBOX} --yes (or read the plan first with --dry-run)"
  fi
  step_sandbox
  step_recipe
  step_layer
  step_launch
  step_end
  step_prefix
  step_pairs
  step_verdicts
  step_inputs
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
