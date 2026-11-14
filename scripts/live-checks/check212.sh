#!/usr/bin/env bash
# Check 212 — a provider challenger, judged, and below the minimum.
#
# Plan task A6.2 writes this check; what it settles is docs/model-ab-testing/
# implementation-plan.md's "A6.2 Check 212" and the reconciliation table's U4 —
# "That a shadow run on a provider route has the same latency profile as the
# champion when both run at once (shared provider rate limits): Check 212
# records both wall times". It is the one live check that reads a pair end to
# end: the launch, both arms, both costs against the per-run cap, the judge run
# A5.2 ships, the verdict object A5.4 scores from, and the report line you read.
#
# What it measures, on the sandbox installation with the project layer's `ab:`
# block naming challengers that are provider models routed for the sandbox
# repository and a `judge_model`:
#
#   * `fugaro models ls --json` (offline) resolves every model the layer names
#     as a challenger: each must be served by a provider route — not the direct
#     Anthropic one — whose allow-list names the sandbox repository or is `*`.
#     That is what "a provider model routed for the sandbox repository" is, and
#     it is read before anything billable starts, because a challenger on a
#     route the repository may not use is a pair that can never start;
#   * small sandbox tasks launched until `fugaro runs ls` shows a pair. Nothing
#     forces a sample: a sample is the sampler's own hash of the run ID, so a
#     flag that steered it would measure a different program. `--max-launches`
#     widens the bound and never forces the roll;
#   * the pair, read from `fugaro runs ls --batch <the launch's own label>` as
#     exactly two ended arms, and each arm's record out of the runs bucket as
#     you read it;
#   * BOTH WALL TIMES (U4): each record's own `started_at` and `finished_at`,
#     turned into seconds here, the challenger's beside the champion's. U4 asks
#     the check to RECORD the two, not to compare them to a threshold the plan
#     never sets, so the row is a recording and the two figures are in it;
#   * BOTH COSTS against the per-run cap (D567): each record's own `cost_usd`
#     at or below the run's own `policy.effective.per_run_usd`, and the two
#     records' caps equal to each other — the challenger runs on the champion's
#     cap unchanged, and a halt is a loss rather than a second budget;
#   * the JUDGE RUN A5.2 ships: `fugaro run --recipe ab-judge` launched by hand,
#     as the release notes say it is (no run starts one by itself), waited to
#     its end, and read for what it left — no branch and no pull request;
#   * the VERDICT OBJECT the judge writes under its own prefix: the pair's own
#     entry, the judge model the layer names, a winner in champion, challenger
#     or tie, and the two swapped passes a disagreement between which is a tie.
#     A pair the judge SKIPPED is not a verdict and reads as a FAIL here;
#   * `fugaro ab --json`, the command you read pairs with: a row for the routed
#     challenger carrying the pair, and the row's own recommendation saying
#     `below-minimum` — D563's gate, because one pair is far under the twenty
#     decisive pairs a conclusion needs. An installation that already carries
#     twenty or more decisive pairs cannot show that, and the row says so
#     instead of passing.
#
# It creates what the plan's sentence says it creates: up to thirty sandbox runs
# (each billable, and each champion among them pushes its branch and opens its
# pull request, as an ordinary sandbox run does), one judge run over the pair
# that was sampled (billable, and it publishes nothing), and nothing else. Its
# teardown is part of the check rather than a list of commands for you: a launch
# that has not ended is cancelled, every pull request this check's launches
# opened is closed and every branch they pushed is deleted, each read back.
#
# Usage:      bash scripts/live-checks/check212.sh --sandbox <a sandbox repository checkout> --yes
#             bash scripts/live-checks/check212.sh --sandbox <path> --yes --max-launches 12
#             bash scripts/live-checks/check212.sh --sandbox <path> --yes --task "<the task>"
#             bash scripts/live-checks/check212.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check212.sh --help
# Needs:      bash, mktemp, date, python3 (the `--json` documents and the
#             published layer's `ab:` block are read with it), git (the remote
#             branches and the repository's own identity), gcloud
#             (authenticated as YOU, and able to read the runs bucket), fugaro
#             (the installed release: the whole point is the build you have) and,
#             for the teardown only, gh — the sandbox repository is a GitHub
#             one, and closing its pull requests is the one step no other tool
#             here makes. A machine without gh still deletes the branches and
#             says, in the Restore row, which pull requests are left for you.
# Where:      your own machine and the **sandbox project** and the **sandbox
#             repository** only. A project or region naming belong is refused by
#             name, and so is a coding agent's session: every command this check
#             runs is one you could type yourself.
# Creates:    up to thirty sandbox runs, the pull requests their champions open
#             and one judge run — every branch deleted and every pull request
#             closed again by the Restore. On disk: one scratch directory,
#             removed at the end, holding the commands' output.
# Costs:      up to THIRTY sandbox runs and what their tasks cost, plus ONE
#             judge run's own model calls — the two passes it makes per pair
#             through the gateway — on the sandbox's own budget. About ten
#             launches is the expectation at a rate of 0.10, and thirty is the
#             bound; the launches stop at the first pair. `--yes` is required.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check212_test.sh) drives it against fake tools only.
# Cleanup:    the branches this check's launches pushed and the pull requests
#             their champions opened, closed and deleted by the Restore, which
#             reads each one back; a launch or a judge run that has not ended,
#             stopped with `fugaro cancel --now` and read back as ended, so that
#             no run of this check's own outlives it; and the scratch directory,
#             removed from the exit trap even on a failed assertion or a Ctrl-C.
# Output:     progress on stderr, ONE block on stdout headed CHECK 212 RESULT,
#             then CHECK 212 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold — the layer names no routed provider
#             challenger, no pair appeared in the bound, an arm is not what the
#             release promises, the judge skipped the pair, or the teardown
#             could not close what it opened. 2: the check could not measure — a
#             tool is missing, the checkout is not one, a `--json` document
#             could not be read, an arm did not end, or the installation
#             already carries twenty decisive pairs so `below-minimum` cannot be
#             read. A key the run never reached reads `FAIL — not reached`.
#
# A NOTE ON WHAT IS NEVER PRINTED: no credential value reaches this script's
# stdout, its stderr, the block or the scratch directory. The rows carry run
# IDs, branch names, pull request numbers, model IDs, costs and statuses.
#
# Knobs (all CHECK212_*): DRY_RUN, KEEP_SCRATCH, SANDBOX (the checkout, when you
# would rather set it in the environment), YES, TASK (what each launch is
# launched with), MAX_LAUNCHES (30), RELEASE (0.24.0: the release the pair
# launch and the judge ship in), SINCE (1d: the window `fugaro ab` is read
# over), REGION (us-east5), RUN_TIMEOUT_SECONDS (3600), POLL_SECONDS (15),
# CLI_TIMEOUT_SECONDS (180), CANCEL_TIMEOUT_SECONDS (300),
# LAUNCH_TIMEOUT_SECONDS (600), DOCTOR_TIMEOUT_SECONDS (300),
# JUDGE_TIMEOUT_SECONDS (3600). --sandbox, --task and --max-launches are the
# same three values as flags, and a value you type as a flag beats the same
# value in the environment: the environment is how a script sets a knob, and a
# run that quietly used the other one would spend the sandbox's money at a bound
# you did not choose.
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
  command -v bash >/dev/null 2>&1 || die "Check 212 needs bash"
  local t
  for t in mktemp date python3 gcloud fugaro git; do
    command -v "$t" >/dev/null 2>&1 || die "Check 212 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk212-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
PROJECT=""
REPO_NAME=""
RUN_ID=""
CHALLENGER_ID=""
PAIR_ID=""
BATCH=""
CHAMPION_REF=""
CHALLENGER_REF=""
JUDGE_ID=""
JUDGE_REF=""
LAUNCHED=0
# LAYER_OK, ROUTE_OK and JUDGE_RECIPE_OK are set by the three branches that
# establish the state the launches below are a reading of: a published layer that
# samples, a layer whose challengers are provider models routed for this
# repository, and a build whose catalog carries the judge recipe the verdict rows
# are read through. Every step that launches a run reads them and nothing else:
# launches made after a failed premise would spend the sandbox's money on a
# reading nobody could grade, and the FAIL row above is already the finding.
LAYER_OK=no
ROUTE_OK=no
JUDGE_RECIPE_OK=no
# ARMS_OK is the same for the pair's two rows: the records below are read at the
# refs those rows carry, so without them there is no key to read.
ARMS_OK=no
CHALLENGER_MODEL=""
JUDGE_MODEL=""
ROUTED_CHALLENGERS=""
REASONS_FILE=""
LAUNCHES_FILE=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk212.XXXXXXXX") || die "Check 212 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  REASONS_FILE="$SCRATCH/reasons"
  : >"$REASONS_FILE"
  LAUNCHES_FILE="$SCRATCH/launched"
  : >"$LAUNCHES_FILE"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  # ${HOME:-}: this guard keeps the removal from ever reaching your home, and
  # reading it unguarded would abort cleanup under `set -u` on a machine where
  # HOME is unset, leaving the scratch directory behind.
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
    say "CHECK212_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The `--json` documents this check reads, in one program: a live check on your
# machine has python3 and no jq, and a row that read a document with grep would
# report a run ID the release did not write the day the document is re-wrapped.
# Every reader prints `key=value` lines or `\t`-separated rows, so a shell can
# hold them without a second parser.
#
# The one document that is not JSON is the published project layer, which
# `fugaro config layer --json` hands over as YAML TEXT and no command resolves
# to a value: nothing in the release prints an effective `ab.rate`
# (`policy.Layer.ABRate` is filled by the launcher and read by the sampler), so
# the check reads the layer's own `ab:` block here, as you read it in the file
# you published. It is a text read and the row says so.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import datetime
import json
import re
import sys


def load(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def text(path):
    with open(path, "r", encoding="utf-8") as fh:
        return fh.read()


def yes(value):
    return "true" if value else "false"


def flat(value):
    return " ".join(str(value or "").split())


def listed(value):
    if isinstance(value, list):
        return ",".join(str(v) for v in value)
    return flat(value)


def number(value, default="0"):
    """A figure as the row prints it: an absent or non-numeric member is the
    default rather than a Python repr, because the rows are read with awk."""
    if isinstance(value, bool) or value is None:
        return default
    if isinstance(value, (int, float)):
        return repr(value)
    return flat(value) or default


# Go writes the run record's timestamps with time.Time's own RFC3339Nano, which
# carries up to nine fractional digits; datetime.fromisoformat in the Python
# this script needs (3.7 on some machines) takes at most six, so the fraction is
# padded or cut to six before it is parsed.
STAMP = re.compile(
    r"^(\d{4}-\d{2}-\d{2})[Tt](\d{2}:\d{2}:\d{2})(?:\.(\d+))?([Zz]|[+-]\d{2}:?\d{2})$")


def parse_stamp(raw):
    m = STAMP.match((raw or "").strip())
    if not m:
        return None
    frac = (m.group(3) or "")[:6].ljust(6, "0")
    zone = "+00:00" if m.group(4) in ("Z", "z") else m.group(4)
    if len(zone) == 5:
        zone = zone[:3] + ":" + zone[3:]
    try:
        return datetime.datetime.fromisoformat(
            "%sT%s.%s%s" % (m.group(1), m.group(2), frac, zone))
    except ValueError:
        return None


def wall_seconds(start, finish):
    a = parse_stamp(start)
    b = parse_stamp(finish)
    if a is None or b is None:
        return ""
    return "%.3f" % ((b - a).total_seconds())


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
        val = val.strip()
        if val.startswith("[") and val.endswith("]"):
            out[key.strip()] = [unquote(v) for v in split_flow(val[1:-1])]
        else:
            out[key.strip()] = unquote(val)
    return out


def block_mapping(lines):
    """A YAML mapping from its own lines, stripped of their indentation: the
    block is already known to be the one under `ab:`, so the nesting left to
    read is a scalar, a flow collection, or a sequence of plain items."""
    out = {}
    i = 0
    while i < len(lines):
        ln = lines[i]
        if ln.startswith("-"):
            i += 1
            continue
        key, sep, val = ln.partition(":")
        if not sep:
            i += 1
            continue
        key = key.strip()
        val = strip_comment(val)
        if val.startswith("[") and val.endswith("]"):
            out[key] = [unquote(v) for v in split_flow(val[1:-1])]
            i += 1
            continue
        if val:
            out[key] = unquote(val)
            i += 1
            continue
        items = []
        j = i + 1
        while j < len(lines) and lines[j].startswith("-"):
            items.append(unquote(strip_comment(lines[j][1:])))
            j += 1
        out[key] = items if j > i + 1 else ""
        i = j
    return out


def ab_block(doc):
    """The top-level `ab:` block of a layer or a fugaro.yaml, as
    (present, mapping). The key's own indentation is what ends the block: a
    mapping nested under another top-level key is not this one."""
    lines = doc.split("\n")
    i = 0
    while i < len(lines):
        line = lines[i]
        if re.match(r"^ab\s*:", line):
            rest = strip_comment(line.split(":", 1)[1])
            if rest:
                if rest.startswith("{") and rest.endswith("}"):
                    return True, flow_mapping(rest[1:-1])
                return True, {}
            block = []
            i += 1
            while i < len(lines):
                ln = lines[i]
                if not ln.strip() or ln.lstrip().startswith("#"):
                    i += 1
                    continue
                if not ln[:1].isspace():
                    break
                block.append(ln.strip())
                i += 1
            return True, block_mapping(block)
        i += 1
    return False, {}


def top_keys(doc):
    """The layer's own top-level keys: what a project that publishes no `ab:`
    block DOES carry, which is what tells its owner where the block goes."""
    keys = []
    for line in doc.split("\n"):
        m = re.match(r"^([A-Za-z_][A-Za-z0-9_.-]*)\s*:", line)
        if m and m.group(1) not in keys:
            keys.append(m.group(1))
    return keys


def ab_challengers(mapping):
    value = mapping.get("challengers")
    if isinstance(value, list):
        return [flat(v) for v in value if flat(v)]
    single = flat(value)
    return [single] if single else []


def ab_lines(mapping):
    out = []
    out.append("rate=%s" % flat(mapping.get("rate")))
    out.append("challengers=%s" % listed(mapping.get("challengers")))
    out.append("challenger_count=%d" % len(ab_challengers(mapping)))
    out.append("max_pairs_per_day=%s" % flat(mapping.get("max_pairs_per_day")))
    out.append("judge_model=%s" % flat(mapping.get("judge_model")))
    out.append("keys=%s" % ",".join(sorted(mapping.keys())))
    return out


def route_for(doc, model, repo):
    """What the project's own model table says about one model the layer named
    as a challenger: the route that serves it, whether that route is a provider
    one rather than the direct Anthropic route, and whether the repository's
    code may be sent down it. `allowed_to` is the serving route's own
    allow_data_to with ["*"] for a route open to every repository, and [] when
    nothing serves the model or the project's allowed_models leaves it out, so
    the membership test below is the whole of D556's routability question."""
    routes = {flat(r.get("name")): r for r in (doc.get("routes") or [])}
    models = {flat(m.get("id")): m for m in (doc.get("models") or [])}
    row = models.get(model)
    if row is None:
        return None
    route = flat(row.get("route"))
    route_row = routes.get(route) or {}
    allowed = [flat(a) for a in (row.get("allowed_to") or [])]
    hit = any(a == "*" or a.lower() == repo.lower() for a in allowed)
    return {
        "model": model,
        "served": bool(route),
        "route": route,
        "kind": flat(route_row.get("kind")),
        "host": flat(route_row.get("host")),
        "provider": bool(route) and route != "direct",
        "allowed": hit,
        "allowed_to": ",".join(allowed),
    }


what = sys.argv[1]

if what == "doctor":
    doc = load(sys.argv[2])
    project = doc.get("project") or {}
    print("gcp_project=%s" % flat(project.get("gcp_project")))
    print("name=%s" % flat(project.get("name")))
elif what == "layer":
    doc = load(sys.argv[2])
    print("where=%s" % flat(doc.get("where")))
    print("generation=%s" % (doc.get("generation") or 0))
    print("sha256=%s" % flat(doc.get("sha256")))
    present, mapping = ab_block(doc.get("yaml") or "")
    print("has_ab=%s" % yes(present))
    print("top_keys=%s" % ",".join(top_keys(doc.get("yaml") or "")))
    for line in ab_lines(mapping):
        print(line)
elif what == "abfile":
    present, mapping = ab_block(text(sys.argv[2]))
    print("has_ab=%s" % yes(present))
    for line in ab_lines(mapping):
        print(line)
elif what == "recipes":
    doc = load(sys.argv[2])
    rows = doc.get("recipes") or []
    print("count=%d" % len(rows))
    for row in rows:
        print("recipe\t%s\t%s\t%s" % (
            flat(row.get("name")),
            flat(row.get("source")),
            yes(row.get("launchable")),
        ))
elif what == "models":
    doc = load(sys.argv[2])
    # The model the layer named is looked up through route_for, which needs the
    # whole document; this query prints the candidates it decides between.
    repo = sys.argv[3] if len(sys.argv) > 3 else ""
    wanted = sys.argv[4] if len(sys.argv) > 4 else ""
    for model in [flat(m) for m in wanted.split(",")]:
        if not model:
            continue
        info = route_for(doc, model, repo)
        if info is None:
            print("cand\t%s\tno\t\t\t\tno\tno\t" % model)
            continue
        print("cand\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s" % (
            info["model"],
            yes(info["served"]),
            info["route"],
            info["kind"],
            info["host"],
            yes(info["provider"]),
            yes(info["allowed"]),
            info["allowed_to"],
        ))
elif what == "launch":
    doc = load(sys.argv[2])
    pair = doc.get("pair") or {}
    print("run_id=%s" % flat(doc.get("run_id")))
    print("run_ref=%s" % flat(doc.get("run")))
    print("repo=%s" % flat(doc.get("repo")))
    print("status=%s" % flat(doc.get("status")))
    print("pair_id=%s" % flat(pair.get("id")))
    print("pair_batch=%s" % flat(pair.get("batch")))
    print("pair_challenger=%s" % flat(pair.get("challenger")))
    print("pair_reason=%s" % flat(pair.get("reason")))
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
elif what == "record":
    doc = load(sys.argv[2])
    ab = doc.get("ab") or {}
    policy = (doc.get("policy") or {}).get("effective") or {}
    start = flat(doc.get("started_at"))
    finish = flat(doc.get("finished_at"))
    print("run_id=%s" % flat(doc.get("run_id")))
    print("repo=%s" % flat(doc.get("repo")))
    print("status=%s" % flat(doc.get("status")))
    print("stage=%s" % flat(doc.get("stage")))
    print("outcome=%s" % flat(doc.get("outcome")))
    print("reason=%s" % flat(doc.get("reason")))
    print("branch=%s" % flat(doc.get("branch")))
    # runstore.Record.PR is an object, not a number (result.schema.json's `pr`:
    # {number,url}), so the field is read for what it holds: a reader that took
    # it for a number would paste a Python repr into the row.
    print("pr=%s" % ((doc.get("pr") or {}).get("number") or 0))
    print("patch_object=%s" % flat(doc.get("patch_object")))
    print("patch_truncated=%s" % yes(doc.get("patch_truncated")))
    print("started_at=%s" % start)
    print("finished_at=%s" % finish)
    print("wall_seconds=%s" % wall_seconds(start, finish))
    print("cost_usd=%s" % number(doc.get("cost_usd")))
    print("per_run_usd=%s" % number(policy.get("per_run_usd")))
    print("ab_pair_id=%s" % flat(ab.get("pair_id")))
    print("ab_arm=%s" % flat(ab.get("arm")))
    print("ab_publish=%s" % flat(ab.get("publish")))
    print("ab_challenger=%s" % flat(ab.get("challenger")))
    print("ab_challenger_source=%s" % flat(ab.get("challenger_source")))
    print("ab_slot=%s" % flat(ab.get("slot")))
elif what == "verdicts":
    doc = load(sys.argv[2])
    pairs = doc.get("pairs") or []
    print("version=%s" % (doc.get("version") or 0))
    print("judge_model=%s" % flat(doc.get("judge_model")))
    print("count=%d" % len(pairs))
    for pair in pairs:
        passes = pair.get("passes") or []
        print("vpair\t%s\t%s\t%s\t%d\t%s" % (
            pair.get("pair_id") or "",
            pair.get("winner") or "",
            pair.get("skipped") or "",
            len(passes),
            ",".join(
                "%s=%s" % (yes(p.get("swap")), p.get("label") or "-")
                for p in passes),
        ))
elif what == "ab":
    doc = load(sys.argv[2])
    rows = doc.get("rows") or []
    print("count=%d" % len(rows))
    for row in rows:
        rec = row.get("recommendation") or {}
        print("ab_row\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s" % (
            row.get("mode") or "",
            row.get("tier") or "",
            row.get("champion") or "",
            row.get("challenger") or "",
            row.get("pairs") or 0,
            row.get("wins") or 0,
            row.get("losses") or 0,
            rec.get("decision") or "",
            rec.get("rule") or "",
            rec.get("decisive") or 0,
            row.get("unjudged") or 0,
        ))
    for w in doc.get("warnings") or []:
        print("warning\t%s" % flat(w))
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

# cand_row FILE MODEL — the `fugaro models ls` line for one candidate, and
# whether that model is served by a provider route this repository may use.
cand_row() { # cand_row FILE MODEL
  awk -F'\t' -v m="$2" '$1 == "cand" && $2 == m { print; exit }' "$1" 2>/dev/null
}

# routed_here FILE — every candidate the project may in fact send this
# repository's code to on a provider route, as a comma-separated list.
routed_here() { # routed_here FILE
  awk -F'\t' '$1 == "cand" && $7 == "true" && $8 == "true" { printf "%s%s", (n++ ? "," : ""), $2 }' "$1" 2>/dev/null
}

# in_list LIST ITEM — 0 when ITEM is one of LIST's comma-separated members.
in_list() { # in_list LIST ITEM
  [ -n "$1" ] && [ -n "$2" ] || return 1
  case ",$1," in *",$2,"*) return 0 ;; esac
  return 1
}

# money_le A B — 0 when A is at or below B, both read as decimal dollars. awk
# rather than a shell test because the figures are floats, and a string compare
# would call 9.5 greater than 10.
money_le() { # money_le A B
  awk -v a="$1" -v b="$2" 'BEGIN { exit (a + 0 <= b + 0) ? 0 : 1 }'
}

# repo_slug_from_url URL: the owner/name a remote URL names, which is the form
# every allow_data_to entry is written in. Both URL shapes a checkout carries
# are read: https://host/owner/name(.git) and git@host:owner/name(.git).
repo_slug_from_url() { # repo_slug_from_url URL
  printf '%s\n' "$1" |
    sed -E 's#^[A-Za-z][A-Za-z0-9+.-]*://[^/]+/##; s#^[^/@]*@[^/:]+:##; s#\.git$##; s#/+$##'
}

# The two facts run_bounded reports are declared here, before any step: a step
# that names them in a message after a read that never started (a ref that is
# not this check's, an argument that did not match) would otherwise abort under
# `set -u` instead of reporting what it found.
BOUNDED_STATUS=0
BOUNDED_TIMED_OUT=no

# run_bounded SECONDS KEY DIR CMD...: run one command from the directory it has
# to be run in and under a bound. Every command here names the project through
# the checkout it is run in, and the record and patch reads are the exception.
#
# The child execs, so the pid the bound kills IS the command: a hung cloud call
# would otherwise outlive the check that gave up on it, which is the one thing
# the bound exists to prevent. The ERR trap is dropped in it too: this check runs
# commands whose failing exit status is one of its own readings (a record that
# is not there, a branch that is not on the remote), and a trap inherited here
# would turn each of them into "an internal error" and a status of 2 before the
# step could report it.
run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  BOUNDED_TIMED_OUT=no
  # Truncated, not appended: the launch loop and the arms poll call the same key
  # again and again, and a `--json` read takes the call's own document — two
  # documents in one file is a parse error, not a second reading.
  ( trap - ERR; cd "$dir" || exit 1; exec "$@" ) >"$SCRATCH/$key.out" 2>"$SCRATCH/$key.err" &
  # The poll is a tenth of a second and the bound is counted in tenths: a read
  # that answers at once — every one of them, when the installation is healthy —
  # would otherwise cost this check a whole second each, and you watch the check
  # rather than the clock.
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
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check212.sh --sandbox <a sandbox repository checkout> --yes — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy the run's own output to the terminal"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the run's output"
}

# guard_agent_session: the check is yours, and it drives commands whose writes a
# coding agent's session is refused. Refusing here rather than letting each
# command refuse one at a time is what makes the block say why: a session that
# ran this far would report the refusals as findings about the release.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 212 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
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
# alone: a build that names no version is decided by the feature probes below and
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

# step_release: the gate every check of a released feature carries. The pair
# launch and the judge both ship in 0.24.0 (docs/releases/v0.24.0.md), and a
# build before it samples nothing at all. Both probes are offline and read the
# installed binary alone, no checkout: `fugaro version`, and the usage line
# `fugaro ab` prints — the A/B results command is the Subscription's, so a core
# build answers with D522's stub instead of the command, and a stub names no
# flag. The recipe catalog is read by step_judge_recipe instead, because that
# read takes a checkout with it.
step_release() {
  local v feature=no
  v=$("$FUGARO_BIN" version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing}"
  if "$FUGARO_BIN" ab --help 2>/dev/null | grep -q -- '--since'; then feature=yes; fi
  if [ "$feature" = no ]; then
    fact release FAIL "this fugaro carries no A/B: 'fugaro ab --help' does not name the command's own --since flag, so this build is either older than release ${RELEASE} — the release the pair launch and the judge ship in — or a core build, whose 'ab' is the D522 stub rather than the command. Check 212 needs release ${RELEASE} (docs/releases/v0.24.0.md) or later, built with the features it ships. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again on the build it leaves you"
    stop "the installed fugaro carries no A/B: this check needs release ${RELEASE}"
  fi
  local rc=0
  version_at_least "$v" "$RELEASE" || rc=$?
  case "$rc" in
  0)
    fact release PASS "fugaro ${v} is release ${RELEASE} or later and its 'ab' command answers: this build carries the sampler, the pair launch, the judge and the report the rows below read"
    ;;
  1)
    fact release FAIL "fugaro ${v} predates release ${RELEASE}, the release the pair launch and the judge ship in: a launch of it samples nothing, and the launches below would measure a build nobody shipped this for. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again"
    stop "the installed fugaro is ${v}, older than ${RELEASE}"
    ;;
  *)
    fact release PASS "fugaro reports '${v}', which is not a release number (a dev build or a commit): the feature probe decides, and 'fugaro ab --help' names the command, so this build carries the work"
    ;;
  esac
}

# step_bound: the spend this check is about to commit, said before it commits
# it. `--yes` is your answer to it; a dry run prints the same sentence and asks
# for nothing.
step_bound() {
  fact bound PASS "up to ${MAX_LAUNCHES} sandbox runs and what their tasks cost — each champion's implement stage and its review rounds, plus each challenger's own spend — on the sandbox's own budget, plus every branch and pull request those champions leave (the Restore closes and deletes them again), plus ONE judge run over the pair that was sampled, whose two model calls per pair go through the run's own gateway and are billed to the project as any call is. The launches stop at the FIRST pair, so the whole bound is only spent when the layer samples nothing. Nothing here forces a sample: a sample is the sampler's own hash of the run ID, and --max-launches widens the bound rather than steering the roll. --yes is required to launch"
}

# ---------------------------------------------------------------- the steps
# step_sandbox: the premise. The project comes from the checkout itself
# (`fugaro doctor --json`), so the check reads the project you actually work in
# rather than one it guessed, and every launch and record below is that
# project's. The repository's own owner/name comes from the checkout's remote,
# which is the form the project layer's `allow_data_to` entries are written in
# and the form `fugaro ab --repo` takes.
step_sandbox() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox project)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the runs this check launches are launched from one"
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
    unmeasured sandbox "fugaro doctor --json answered without a GCP project: this check cannot say which project the sandbox checkout belongs to, and the rows below are about the project and the runs bucket. What it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  case "$PROJECT" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${PROJECT}: this check is never run there" ;;
  esac
  run_bounded "$CLI_TIMEOUT" remote-url "$SANDBOX" git remote get-url origin
  if [ "$BOUNDED_TIMED_OUT" = no ] && [ "$BOUNDED_STATUS" = 0 ]; then
    REPO_NAME=$(repo_slug_from_url "$(head -n 1 "$SCRATCH/remote-url.out")")
  fi
  if [ -z "$REPO_NAME" ]; then
    unmeasured sandbox "${SANDBOX} is a Fugaro repository's checkout and it belongs to ${PROJECT}, but 'git remote get-url origin' there named no repository (exit ${BOUNDED_STATUS}, output in $SCRATCH/remote-url.out and $SCRATCH/remote-url.err). The owner/name is what the layer's allow_data_to entries are matched against and what 'fugaro ab --repo' takes, so without it the route row below cannot be read"
    return 0
  fi
  fact sandbox PASS "${SANDBOX} is a Fugaro repository's checkout, it belongs to ${PROJECT}, and its origin is ${REPO_NAME}: the check launches from this checkout, matches the layer's allow-lists against ${REPO_NAME}, and reads the pair's records in this project's bucket"
}

# step_judge_recipe: the catalog the judge run's recipe has to be in. `fugaro
# recipes ls --json` reads the checkout it is run in and re-reads that
# checkout's project layer from the bucket, so it is read in the sandbox
# checkout and after step_sandbox rather than offline: run anywhere else it
# would answer for another project's catalog, and it is the sandbox project the
# judge run below is launched in. A catalog without `ab-judge`, or with one
# that is not launchable, is the FAIL row here — and no pair is launched for it
# (step_pair's guard), because a pair whose verdict no build can write is money
# spent on half a reading.
step_judge_recipe() {
  if [ -z "$PROJECT" ]; then
    unmeasured judge-recipe "there is no project to read the catalog for: the row above says why"
    return 0
  fi
  say "reading the recipe catalog: fugaro recipes ls --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" recipes "$SANDBOX" "$FUGARO_BIN" recipes ls --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ] || ! read_json recipes "$SCRATCH/recipes.out" "$SCRATCH/recipes.txt"; then
    unmeasured judge-recipe "'fugaro recipes ls --json' did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): the judge run below is 'fugaro run --recipe ab-judge', and without ${PROJECT}'s catalog this check cannot say whether the build you have carries that recipe. Its output is in $SCRATCH/recipes.out and $SCRATCH/recipes.err"
    return 0
  fi
  local row
  row=$(awk -F'\t' '$1 == "recipe" && $2 == "ab-judge" { print; exit }' "$SCRATCH/recipes.txt")
  if [ -z "$row" ]; then
    fact judge-recipe FAIL "the catalog carries no recipe named ab-judge (it carries $(awk -F'\t' '$1 == "recipe" { printf "%s%s", (n++ ? ", " : ""), $2 }' "$SCRATCH/recipes.txt")): the judge run below is launched with 'fugaro run --recipe ab-judge' (docs/releases/v0.24.0.md), and a build whose catalog does not carry it is one no pair of it can be judged on. Run 'fugaro update' at your terminal and run this check again on the build it leaves you"
    return 0
  fi
  if [ "$(printf '%s' "$row" | awk -F'\t' '{ print $4 }')" != true ]; then
    fact judge-recipe FAIL "the catalog's ab-judge recipe is not launchable (source $(printf '%s' "$row" | awk -F'\t' '{ print $3 }')): 'fugaro run --recipe ab-judge' is refused for it, and the verdict row below has nothing to read. Run 'fugaro update' at your terminal and run this check again"
    return 0
  fi
  JUDGE_RECIPE_OK=yes
  fact judge-recipe PASS "the catalog carries ab-judge (source $(printf '%s' "$row" | awk -F'\t' '{ print $3 }')) and it is launchable, so the judge run below is one this build can start"
}

# step_layer: the state every launch below is a reading of. The published
# project layer is the only place ab.rate, the challenger list and judge_model
# live (D550, D556): nothing in the release prints an effective ab.rate, so the
# check reads the layer's own `ab:` block out of the text `fugaro config layer
# --json` hands over, and says in the row that it is a text read.
step_layer() {
  if [ -z "$PROJECT" ]; then
    unmeasured layer "there is no project to read the layer for: the row above says why"
    return 0
  fi
  if ! read_json abfile "$SANDBOX/fugaro.yaml" "$SCRATCH/repoab.txt"; then
    unmeasured layer "the sandbox checkout's fugaro.yaml could not be read: it is where a repository's own ab.rate: 0 opt-out lives (D556), and without it this check cannot say whether the repository has opted out. The file is at $SANDBOX/fugaro.yaml"
    return 0
  fi
  say "reading the published project layer: fugaro config layer --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" layer "$SANDBOX" "$FUGARO_BIN" config layer --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured layer "fugaro config layer --json did not answer (exit ${BOUNDED_STATUS}): the published project layer is where ab.rate, the challenger list and judge_model live, and without it there is nothing to launch against. Its output is in $SCRATCH/layer.out and $SCRATCH/layer.err — a project with no published layer answers here too, and then this check's premise is one 'fugaro config publish' away"
    return 0
  fi
  if ! read_json layer "$SCRATCH/layer.out" "$SCRATCH/layer.txt"; then
    unmeasured layer "fugaro config layer --json printed a document this check could not read: what it printed is in $SCRATCH/layer.out"
    return 0
  fi
  local where rate challengers count keys
  where=$(field "$SCRATCH/layer.txt" where)
  rate=$(field "$SCRATCH/layer.txt" rate)
  challengers=$(field "$SCRATCH/layer.txt" challengers)
  count=$(field "$SCRATCH/layer.txt" challenger_count)
  keys=$(field "$SCRATCH/layer.txt" top_keys)
  JUDGE_MODEL=$(field "$SCRATCH/layer.txt" judge_model)
  if [ "$(field "$SCRATCH/layer.txt" has_ab)" != true ]; then
    fact layer FAIL "the published project layer carries no top-level ab: block (its where is ${where:-unknown}); its top-level keys are ${keys:-none}, and this check read the whole block out of the layer's own text. A project with no ab: block samples nothing at all (D550, D557), so no launch below could ever be the pair this check measures. Add the block to the project's layer file — a rate above zero, the provider challengers you allow and a judge_model, which is the shape docs/releases/v0.24.0.md's For operators paragraph gives — and publish it with 'fugaro config publish'"
    return 0
  fi
  if ! awk -v r="${rate:-}" 'BEGIN { exit (r + 0 > 0) ? 0 : 1 }'; then
    fact layer FAIL "the published project layer sets ab.rate: ${rate:-unset} (where ${where:-unknown}); A/B is off, so no launch below is ever sampled and the loop would spend its whole bound without a pair — which is not a reading about the shadow but about a switch nobody turned on. Set ab.rate above zero in the project's layer file (0.10 is the figure the plan works at, and the sampler's ceiling, D557) and publish it with 'fugaro config publish'"
    return 0
  fi
  if [ "${count:-0}" -lt 1 ]; then
    fact layer FAIL "the published project layer sets ab.rate: ${rate} and names no challenger (ab.challengers is empty); a launch that samples then skips with the closed reason 'no-challenger' (D556), so no pair is ever started. Name the provider model you allow as a challenger in the layer's challengers list and publish the layer again"
    return 0
  fi
  if [ -z "$JUDGE_MODEL" ]; then
    fact layer FAIL "the published project layer names no ab.judge_model (where ${where:-unknown}): the judge run is the 'ab-judge' recipe, which calls ab.judge_model, and a project that publishes none has nothing to judge with — 'fugaro run --recipe ab-judge' is refused rather than judging on a model you did not name (D553, A5.2). Name the model in the layer's ab: block and publish the layer again"
    return 0
  fi
  if [ "$(field "$SCRATCH/repoab.txt" has_ab)" = true ]; then
    local repo_rate
    repo_rate=$(field "$SCRATCH/repoab.txt" rate)
    if awk -v r="${repo_rate:-}" 'BEGIN { exit (r + 0 > 0) ? 0 : 1 }'; then
      :
    else
      fact layer FAIL "the sandbox repository's own fugaro.yaml carries an ab: block with rate ${repo_rate:-unset}, which is the opt-out (D556, D557): the repository lowers the project's rate to zero, so no launch of it is ever sampled whatever the layer says. Remove the ab: block from $SANDBOX/fugaro.yaml (or raise its rate, which a repository may only do down to zero) and run this check again"
      return 0
    fi
  fi
  LAYER_OK=yes
  fact layer PASS "the published project layer (${where:-unknown}, generation $(field "$SCRATCH/layer.txt" generation), sha256 $(field "$SCRATCH/layer.txt" sha256)) sets ab.rate: ${rate}, names ${count} challenger(s) — ${challengers:-none} — and names the judge model ${JUDGE_MODEL}. This is a text read of the layer's own ab: block — no command in this release prints an effective ab.rate — and the sandbox repository's fugaro.yaml carries no opt-out"
}

# step_route: the plan's own words for this check are "a pair whose challenger
# is a provider model routed for the sandbox repository", and both halves of
# that are read here, offline, before anything billable starts: the model the
# layer names has to be served by a route that is not the direct Anthropic one,
# and that route's own allow-list has to admit this repository. A challenger the
# repository may not use is one no pair can be started on — the call is refused
# at the gateway — so a launch loop run against it would spend the sandbox's
# money to prove a thing the model table already says.
step_route() {
  if [ "$LAYER_OK" != yes ]; then
    unmeasured route "there is no challenger list to resolve: the layer row above says why"
    return 0
  fi
  if [ -z "$REPO_NAME" ]; then
    unmeasured route "this check does not know the sandbox repository's owner/name (the sandbox row above says why), and it is what every allow_data_to entry is matched against"
    return 0
  fi
  local challengers
  challengers=$(field "$SCRATCH/layer.txt" challengers)
  say "resolving the layer's challengers against this project's routes: fugaro models ls --json (offline)"
  run_bounded "$CLI_TIMEOUT" models "$SANDBOX" "$FUGARO_BIN" models ls --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured route "fugaro models ls --json did not answer (exit ${BOUNDED_STATUS}): the model table is where the route that serves each challenger and the repositories that route admits are read, and it reaches no bucket and no host. Its output is in $SCRATCH/models.out and $SCRATCH/models.err"
    return 0
  fi
  if ! python3 "$SCRATCH/read.py" models "$SCRATCH/models.out" "$REPO_NAME" "$challengers" >"$SCRATCH/models.txt" 2>"$SCRATCH/models.txt.err"; then
    unmeasured route "fugaro models ls --json printed a document this check could not read: what it printed is in $SCRATCH/models.out"
    return 0
  fi
  local model row bad="" why served provider allowed route kind host allow
  local IFS=,
  for model in $challengers; do
    row=$(cand_row "$SCRATCH/models.txt" "$model")
    served=$(rcol "$row" 3)
    route=$(rcol "$row" 4)
    kind=$(rcol "$row" 5)
    host=$(rcol "$row" 6)
    provider=$(rcol "$row" 7)
    allowed=$(rcol "$row" 8)
    allow=$(rcol "$row" 9)
    if [ -z "$row" ]; then
      bad="${bad}${bad:+; }${model} is not in this project's model table at all — no route claims it and no model_prices or budget.allowed_models entry names it — so a run cannot name it"
      continue
    fi
    if [ "$served" != true ] || [ -z "$route" ]; then
      bad="${bad}${bad:+; }${model} is served by no route"
      continue
    fi
    if [ "$provider" != true ]; then
      bad="${bad}${bad:+; }${model} is served by the direct route (${route}, ${kind} ${host}), which is the Anthropic API itself rather than a provider route: a challenger on it is a second run on the same route as the champion, and the shared-rate-limit question U4 asks cannot be read from a pair of those"
      continue
    fi
    if [ "$allowed" != true ]; then
      bad="${bad}${bad:+; }${model} is served by the provider route ${route} (${kind} ${host}) whose allow-list is '${allow:-empty}' and does not admit ${REPO_NAME}: the project would refuse to send this repository's code down that route, so a pair on it can never make its calls. Add ${REPO_NAME} (or *) to that provider's allow_data_to in the project config"
      continue
    fi
    printf 'routed\t%s\t%s\t%s\t%s\n' "$model" "$route" "$host" "$allow" >>"$SCRATCH/routed.txt"
  done
  unset IFS
  if [ -n "$bad" ]; then
    fact route FAIL "the published layer's challengers are not provider models routed for ${REPO_NAME}, so the pairs below would not be the pair this check exists for: ${bad}. Change ab.challengers to a model whose route is a provider route admitting ${REPO_NAME}, publish the layer again, and run this check again"
    return 0
  fi
  ROUTED_CHALLENGERS=$(routed_here "$SCRATCH/models.txt")
  if [ -z "$ROUTED_CHALLENGERS" ]; then
    fact route FAIL "this check resolved no routed challenger at all (the layer names ${challengers:-none}): the readings below are about a pair on a provider route, and there is none to read. The model table it read is in $SCRATCH/models.out"
    return 0
  fi
  ROUTE_OK=yes
  fact route PASS "every challenger the layer names is a provider model routed for ${REPO_NAME}: $(awk -F'\t' '$1 == "routed" { printf "%s%s on %s (%s, allow_data_to %s)", (n++ ? ", " : ""), $2, $3, $4, $5 }' "$SCRATCH/routed.txt"). Read offline from fugaro models ls --json, before anything billable started"
}

# step_pair: the launches. Small sandbox tasks launched until `fugaro runs ls`
# shows a pair, about ten expected at 0.10, the owner stopping at thirty; and
# there is deliberately no flag that forces a sample, so this step can only spend
# launches and read what they answered. The launch's own `--json` names the pair,
# and the batch label it carries is what the row below reads `fugaro runs ls`
# with.
step_pair() {
  if [ -z "$PROJECT" ]; then
    unmeasured pair "there is no project to launch a run in: the row above says why"
    return 0
  fi
  # The launches happen only once the rows above have established the state they
  # are a reading of — a published layer that samples with routed provider
  # challengers named. Launches made after a failed premise would spend the
  # sandbox's money on a reading nobody could grade, and the FAIL row above is
  # already the finding.
  if [ "$LAYER_OK" != yes ] || [ "$ROUTE_OK" != yes ] || [ "$JUDGE_RECIPE_OK" != yes ]; then
    unmeasured pair "nothing was launched: the layer, route or judge-recipe row above did not establish the state these launches are a reading of, and up to ${MAX_LAUNCHES} runs launched without it would spend the sandbox's money on a reading nobody could grade. Settle that row and run this check again"
    return 0
  fi
  local n pair_id pair_batch pair_challenger pair_reason run_id repo status
  for n in $(seq 1 "$MAX_LAUNCHES"); do
    say "launching ${n} of at most ${MAX_LAUNCHES} from ${SANDBOX} (a real sandbox run, with what its task costs)"
    run_bounded "$LAUNCH_TIMEOUT" launch "$SANDBOX" "$FUGARO_BIN" run --repo "$SANDBOX" --json "$TASK"
    if [ "$BOUNDED_TIMED_OUT" = yes ]; then
      unmeasured pair "launch ${n} did not finish inside ${LAUNCH_TIMEOUT}s and was ended: the launches before it are real sandbox runs, and this check's teardown cancels the ones that have not ended with 'fugaro cancel <run> --now' when it ends. What it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
      return 0
    fi
    if [ "$BOUNDED_STATUS" != 0 ]; then
      fact pair FAIL "fugaro run exited ${BOUNDED_STATUS} on launch ${n} of at most ${MAX_LAUNCHES} from ${SANDBOX}: the check needs a launch to sample a pair, and a launch that refuses says the sandbox's configuration is not one a run starts on. What it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
      return 0
    fi
    if ! read_json launch "$SCRATCH/launch.out" "$SCRATCH/launch.txt"; then
      fact pair FAIL "fugaro run --json printed a document this check could not read on launch ${n}: the launch is the step that samples, and its own --json view is how the pair is named. What it printed is in $SCRATCH/launch.out"
      return 0
    fi
    run_id=$(field "$SCRATCH/launch.txt" run_id)
    repo=$(field "$SCRATCH/launch.txt" repo)
    status=$(field "$SCRATCH/launch.txt" status)
    if [ -z "$run_id" ]; then
      fact pair FAIL "the launch ${n} named no run (status=${status:-unknown}): this check follows and tears down the runs it launched, and a launch that does not name its own cannot be followed. What it printed is in $SCRATCH/launch.out"
      return 0
    fi
    LAUNCHED=$n
    RUN_ID=$run_id
    [ -n "$repo" ] && REPO_NAME=$repo
    printf '%s\n' "$run_id" >>"$LAUNCHES_FILE"
    pair_id=$(field "$SCRATCH/launch.txt" pair_id)
    pair_batch=$(field "$SCRATCH/launch.txt" pair_batch)
    pair_challenger=$(field "$SCRATCH/launch.txt" pair_challenger)
    pair_reason=$(field "$SCRATCH/launch.txt" pair_reason)
    if [ -n "$pair_id" ]; then
      if [ -z "$pair_challenger" ]; then
        fact pair FAIL "launch ${n} sampled a pair (${pair_id}) and the challenger did not start: fugaro run --json names the pair and no second run in it. The champion ${run_id} is a real sandbox run: this check's teardown cancels it with 'fugaro cancel ${run_id} --now' when it ends, unless it has ended by then. Its stderr names why the arm did not start and is in $SCRATCH/launch.err"
        return 0
      fi
      PAIR_ID=$pair_id
      BATCH=$pair_batch
      CHALLENGER_ID=$pair_challenger
      printf '%s\n' "$pair_challenger" >>"$LAUNCHES_FILE"
      fact pair PASS "launch ${n} of at most ${MAX_LAUNCHES} was sampled: fugaro run --json names pair ${PAIR_ID} on batch ${BATCH} with challenger ${CHALLENGER_ID}. The champion is ${run_id} on ${REPO_NAME:-the sandbox repository}; both arms are torn down by the Restore below"
      return 0
    fi
    printf '%s\t%s\n' "${n}" "${pair_reason:--}" >>"$REASONS_FILE"
  done
  local skipped=0 silent=0 detail="" reason
  while IFS=$'\t' read -r _ reason; do
    [ -n "$reason" ] || continue
    if [ "$reason" = "-" ]; then
      silent=$((silent + 1))
    else
      skipped=$((skipped + 1))
      detail="${detail}${detail:+, }${reason}"
    fi
  done <"$REASONS_FILE"
  fact pair FAIL "${MAX_LAUNCHES} launches and none sampled a pair. ${silent} of them answered with no pair block at all — the rate's own roll, or a launch D558's eligibility excludes, and at 0.10 about a tenth of them sampling is what the plan expects — and ${skipped} were skipped with a closed reason: ${detail:-none}. Nothing here forces a sample, so the bound is the only lever: widen it with --max-launches (each launch is a real sandbox run with what its task costs) or read the reasons above, which name what the sampler refused and what to change"
}

# step_arms: the pair, through the command the plan's sentence names. `fugaro
# runs ls --batch <label>` is what a reader uses to see the two arms, and the
# label is the one the launch's own --json carried, so this row reads the pair
# back rather than restating it. Both arms have to have ENDED: a record read
# while a run is still working is whatever was there before.
step_arms() {
  if [ -z "$PAIR_ID" ]; then
    unmeasured arms "there is no pair to read back: the row above says why"
    return 0
  fi
  say "waiting for both arms of ${PAIR_ID} to end (up to ${RUN_TIMEOUT}s; fugaro runs ls --repo ${SANDBOX} --batch ${BATCH} --json)"
  local waited=0 count="" champion_row="" challenger_row="" both=no
  while [ "$waited" -lt "$RUN_TIMEOUT" ]; do
    run_bounded "$CLI_TIMEOUT" arms "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$SANDBOX" --batch "$BATCH" --json
    count=""
    if [ "$BOUNDED_STATUS" = 0 ] && read_json rows "$SCRATCH/arms.out" "$SCRATCH/arms.txt"; then
      count=$(field "$SCRATCH/arms.txt" count)
      champion_row=$(rows_row "$SCRATCH/arms.txt" "$RUN_ID")
      challenger_row=$(rows_row "$SCRATCH/arms.txt" "$CHALLENGER_ID")
      if [ "$(rcol "$champion_row" 4)" = true ] && [ "$(rcol "$challenger_row" 4)" = true ]; then
        both=yes
        break
      fi
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$both" != yes ]; then
    unmeasured arms "${PAIR_ID}'s two arms had not both ended after ${RUN_TIMEOUT}s (fugaro runs ls --batch ${BATCH} answered ${count:-no} row(s): champion ${RUN_ID} $([ -n "$champion_row" ] && echo "status=$(rcol "$champion_row" 3)" || echo 'is not in the list'), challenger ${CHALLENGER_ID} $([ -n "$challenger_row" ] && echo "status=$(rcol "$challenger_row" 3)" || echo 'is not in the list')). The two arms are real sandbox runs: this check's teardown cancels the ones that have not ended with 'fugaro cancel <run> --now' when it ends, and 'fugaro runs show ${RUN_ID}' or 'fugaro runs show ${CHALLENGER_ID}' reads what they did"
    return 0
  fi
  if [ "$count" != 2 ]; then
    fact arms FAIL "fugaro runs ls --batch ${BATCH} answered ${count} row(s) and the pair is two: the batch label is what both arms carry and what joins them (A3.0), so a label that names ${count} runs is a pairing a reader cannot take the two arms from. The rows are in $SCRATCH/arms.out"
    return 0
  fi
  if [ "$(rcol "$champion_row" 5)" != "$BATCH" ] || [ "$(rcol "$challenger_row" 5)" != "$BATCH" ]; then
    fact arms FAIL "the two rows fugaro runs ls --batch ${BATCH} answered do not both carry the label '${BATCH}': champion '$(rcol "$champion_row" 5)', challenger '$(rcol "$challenger_row" 5)'. The label is what makes the two rows a pair, and one that reads differently is a pair nobody can join. The rows are in $SCRATCH/arms.out"
    return 0
  fi
  CHAMPION_REF=$(rcol "$champion_row" 6)
  CHALLENGER_REF=$(rcol "$challenger_row" 6)
  case "$CHAMPION_REF/$CHALLENGER_REF" in
  */"$RUN_ID"/*/"$CHALLENGER_ID") ;;
  *)
    fact arms FAIL "the two rows under batch ${BATCH} are keyed ${CHAMPION_REF:-none} and ${CHALLENGER_REF:-none}, and this check's arms are ${RUN_ID} and ${CHALLENGER_ID}: a row keyed elsewhere is another run's, and the records below are read at the keys those rows carry. The rows are in $SCRATCH/arms.out"
    return 0
    ;;
  esac
  ARMS_OK=yes
  fact arms PASS "fugaro runs ls --repo ${SANDBOX} --batch ${BATCH} answers exactly the pair's two rows — champion ${RUN_ID} and challenger ${CHALLENGER_ID} — and both have ended (champion $(rcol "$champion_row" 3), challenger $(rcol "$challenger_row" 3)) after ${waited}s. The plan's own sentence is read this way: one label, two rows, a pair"
}

# record_of REF DEST: one run's record out of the runs bucket, as the owner
# reads it. The ref is the row's own `<slug>/<run id>`, which is why it is taken
# from `fugaro runs ls` rather than built here.
record_of() { # record_of REF DEST
  local ref=$1 dest=$2
  case "$ref" in
  */"$RUN_ID" | */"$CHALLENGER_ID" | */"$JUDGE_ID") ;;
  *)
    return 1
    ;;
  esac
  run_bounded "$CLI_TIMEOUT" "$dest" "$SANDBOX" gcloud storage cat "gs://fugaro-runs-${PROJECT}/runs/${ref}/result.json" --project="${PROJECT}"
  [ "$BOUNDED_TIMED_OUT" = no ] && [ "$BOUNDED_STATUS" = 0 ] || return 1
  read_json record "$SCRATCH/$dest.out" "$SCRATCH/$dest.txt"
}

# step_champion: the champion's own record. It publishes as an unsampled run
# does (A2.1) and adds its `ab` block and its patch object; the arm and the pair
# id are what join it to the shadow.
step_champion() {
  if [ "$ARMS_OK" != yes ]; then
    unmeasured champion "there is no ended pair to read the champion's record from: the row above says why"
    return 0
  fi
  if ! record_of "$CHAMPION_REF" champion; then
    unmeasured champion "gs://fugaro-runs-${PROJECT}/runs/${CHAMPION_REF}/result.json could not be read (exit ${BOUNDED_STATUS}): a run that ended has a record, and the record is where the arm, the pair, the wall times and the cost are written. What gcloud printed is in $SCRATCH/champion.err"
    return 0
  fi
  local recorded arm pair_id
  recorded=$(field "$SCRATCH/champion.txt" run_id)
  arm=$(field "$SCRATCH/champion.txt" ab_arm)
  pair_id=$(field "$SCRATCH/champion.txt" ab_pair_id)
  if [ "$recorded" != "$RUN_ID" ]; then
    fact champion FAIL "the record at runs/${CHAMPION_REF}/result.json records run_id=${recorded} and this check launched ${RUN_ID}: the record at that key is another run's, and nothing below can be read as this arm's"
    return 0
  fi
  if [ "$arm" != champion ]; then
    fact champion FAIL "the champion's record carries ab.arm '${arm:-absent}' and this arm is 'champion': the champion is the arm of a pair that publishes as it always did (A2.1), and a champion's own block saying otherwise is a pair a reader cannot join. It carries pair_id '${pair_id:-absent}'. The record is in $SCRATCH/champion.out"
    return 0
  fi
  if [ "$pair_id" != "$PAIR_ID" ]; then
    fact champion FAIL "the champion's record carries ab.pair_id '${pair_id:-absent}' and this check's pair is ${PAIR_ID}: the pair id is the one value both arms carry and the only way the two records are joined (D559). The record is in $SCRATCH/champion.out"
    return 0
  fi
  fact champion PASS "the champion's record at runs/${CHAMPION_REF}/result.json carries ab.arm champion and the pair's id ${PAIR_ID} (status=$(field "$SCRATCH/champion.txt" status), outcome=$(field "$SCRATCH/champion.txt" outcome), branch=$(field "$SCRATCH/champion.txt" branch), pr=$(field "$SCRATCH/champion.txt" pr)): it is the arm that publishes, and the rows below read what it left"
}

# step_challenger: the reading the plan names first. The challenger's record is
# where the model the OWNER named in the layer is written, so this is also where
# "the pair's challenger IS one of the routed provider models" is settled: the
# route row above proved the layer's list is made of them, and this row proves
# the pair that was sampled drew from that list.
step_challenger() {
  if [ "$ARMS_OK" != yes ]; then
    unmeasured challenger "there is no ended pair to read the challenger's record from: the row above says why"
    return 0
  fi
  if ! record_of "$CHALLENGER_REF" challenger; then
    unmeasured challenger "gs://fugaro-runs-${PROJECT}/runs/${CHALLENGER_REF}/result.json could not be read (exit ${BOUNDED_STATUS}): the challenger's own record is where ab.arm, ab.publish, its model, its wall time and its cost are written. What gcloud printed is in $SCRATCH/challenger.err"
    return 0
  fi
  local recorded arm publish source outcome branch pr
  recorded=$(field "$SCRATCH/challenger.txt" run_id)
  arm=$(field "$SCRATCH/challenger.txt" ab_arm)
  publish=$(field "$SCRATCH/challenger.txt" ab_publish)
  source=$(field "$SCRATCH/challenger.txt" ab_challenger_source)
  outcome=$(field "$SCRATCH/challenger.txt" outcome)
  branch=$(field "$SCRATCH/challenger.txt" branch)
  pr=$(field "$SCRATCH/challenger.txt" pr)
  CHALLENGER_MODEL=$(field "$SCRATCH/challenger.txt" ab_challenger)
  if [ "$recorded" != "$CHALLENGER_ID" ]; then
    fact challenger FAIL "the record at runs/${CHALLENGER_REF}/result.json records run_id=${recorded} and this check's challenger is ${CHALLENGER_ID}: the record at that key is another run's, and nothing below can be read as this arm's"
    return 0
  fi
  if [ "$arm" != challenger ]; then
    fact challenger FAIL "the challenger's record carries ab.arm '${arm:-absent}' and this arm is 'challenger': a second arm without the block is a run that publishes, which is the one thing the shadow may never be (D554, A2.1). The record is in $SCRATCH/challenger.out"
    return 0
  fi
  if [ "$publish" != none ]; then
    fact challenger FAIL "the challenger's record carries ab.publish '${publish:-absent}' and the arm's own block is 'none' (D554): the publish mode is what the runner reads to push nothing and open nothing, and a challenger whose own record does not carry it is one whose publishes are a question rather than a reading. The record is in $SCRATCH/challenger.out"
    return 0
  fi
  if [ -z "$CHALLENGER_MODEL" ]; then
    fact challenger FAIL "the challenger's record names no model in ab.challenger: the arm runs the coder role on the model the OWNER named in the project layer (D556), and a record that names none is one the pair's report cannot score against the champion. The record is in $SCRATCH/challenger.out"
    return 0
  fi
  if ! in_list "$ROUTED_CHALLENGERS" "$CHALLENGER_MODEL"; then
    fact challenger FAIL "the pair's challenger ran on ${CHALLENGER_MODEL}, which is not one of the provider models this check resolved as routed for ${REPO_NAME} (${ROUTED_CHALLENGERS:-none}): this check exists for a pair whose challenger is a provider model routed for the sandbox repository, and this pair is not one. Either the layer's challengers changed between the route row and the launch, or the pair drew a model from outside that list — which would be a finding about the sampler (D556) rather than about the shadow. The record is in $SCRATCH/challenger.out"
    return 0
  fi
  if [ "$source" != owner ]; then
    fact challenger FAIL "the challenger's record carries ab.challenger_source '${source:-absent}' and a challenger's is 'owner' (D556): the source says the model came from the project layer rather than the repository's own file, which is the boundary a repository author may not cross. The record is in $SCRATCH/challenger.out"
    return 0
  fi
  if [ "$outcome" != none ]; then
    fact challenger FAIL "the challenger's record carries outcome '${outcome:-absent}' and a challenger's is 'none' (A2.1): the outcome is what a reader takes the arm's pull request from, and an arm that records one is an arm that opened something. The record is in $SCRATCH/challenger.out"
    return 0
  fi
  if [ -n "$branch" ] || [ "${pr:-0}" != 0 ]; then
    fact challenger FAIL "the challenger's record carries branch '${branch:-none}' and pr ${pr:-0}: a challenger pushes nothing and opens nothing (D554, A2.1), so a record that names either is the shadow having published. The record is in $SCRATCH/challenger.out, and the branch and pull request it names are yours to remove"
    return 0
  fi
  fact challenger PASS "the challenger's record at runs/${CHALLENGER_REF}/result.json carries ab.arm challenger, ab.publish none, ab.challenger ${CHALLENGER_MODEL} with challenger_source owner, outcome none, and NO branch and NO pull request (status=$(field "$SCRATCH/challenger.txt" status)): the same task, at the same commit, on the provider model ${CHALLENGER_MODEL} the layer routed for ${REPO_NAME} — and it published nothing"
}

# step_walltimes: U4's own reading. The reconciliation table's row is "That a
# shadow run on a provider route has the same latency profile as the champion
# when both run at once (shared provider rate limits): Check 212 records both
# wall times" — so what this row owes is the two figures, taken from each
# record's own started_at and finished_at rather than from anything a run says
# about itself, and it fails only where there is no figure to record (a record
# with no timestamps, or one whose end precedes its start) or no spend at all.
# The ratio of the two is printed because it is what the owner reads the
# question off; the plan sets no threshold for it, so the check applies none.
step_walltimes() {
  if [ ! -s "$SCRATCH/champion.txt" ] || [ ! -s "$SCRATCH/challenger.txt" ]; then
    unmeasured wall-times "there are no two records to read the wall times from: a row above says why"
    return 0
  fi
  local cstart cfinish sstart sfinish csec ssec=""
  cstart=$(field "$SCRATCH/champion.txt" started_at)
  cfinish=$(field "$SCRATCH/champion.txt" finished_at)
  sstart=$(field "$SCRATCH/challenger.txt" started_at)
  sfinish=$(field "$SCRATCH/challenger.txt" finished_at)
  csec=$(field "$SCRATCH/champion.txt" wall_seconds)
  ssec=$(field "$SCRATCH/challenger.txt" wall_seconds)
  if [ -z "$csec" ] || [ -z "$ssec" ]; then
    fact wall-times FAIL "U4's two wall times could not both be read: the champion's record carries started_at '${cstart:-absent}' and finished_at '${cfinish:-absent}' (${csec:-no duration}), the challenger's started_at '${sstart:-absent}' and finished_at '${sfinish:-absent}' (${ssec:-no duration}). A run that ended has both stamps — the runner writes them at bootstrap and at the end — so a record missing one is a record that cannot be read for the latency U4 asks about. The records are in $SCRATCH/champion.out and $SCRATCH/challenger.out"
    return 0
  fi
  if ! awk -v a="$csec" 'BEGIN { exit (a + 0 > 0) ? 0 : 1 }' || ! awk -v b="$ssec" 'BEGIN { exit (b + 0 > 0) ? 0 : 1 }'; then
    fact wall-times FAIL "the two arms' own stamps give the champion ${csec}s and the challenger ${ssec}s, and a run takes time: a duration at or below zero is a finished_at at or before its started_at, which is a record nobody can read a latency profile from. The records are in $SCRATCH/champion.out and $SCRATCH/challenger.out"
    return 0
  fi
  local ratio
  ratio=$(awk -v a="$ssec" -v b="$csec" 'BEGIN { printf "%.2f", a / b }')
  fact wall-times PASS "U4's two wall times, each read from its own record's started_at and finished_at: champion ${RUN_ID} ${csec}s (${cstart} to ${cfinish}), challenger ${CHALLENGER_ID} on ${CHALLENGER_MODEL} ${ssec}s (${sstart} to ${sfinish}). The challenger's is ${ratio}x the champion's. This row RECORDS the two, which is what the plan asks of Check 212: it sets no threshold for the ratio, so the figure is yours to read rather than a bar this check applies"
}

# step_costs: D567's own reading. The challenger runs on the champion's per-run
# cap unchanged, so the two records' effective caps have to be the same figure,
# and each arm's own cost has to be at or below it — a cap the arms are not
# inside is one that halted a run, and D567 makes a halt a loss rather than a
# second budget. The cap is read out of each record's own `policy.effective`
# rather than from the sandbox's configuration, which is what the run actually
# ran under.
step_costs() {
  if [ ! -s "$SCRATCH/champion.txt" ] || [ ! -s "$SCRATCH/challenger.txt" ]; then
    unmeasured costs "there are no two records to read the costs from: a row above says why"
    return 0
  fi
  local ccap scap ccost scost
  ccap=$(field "$SCRATCH/champion.txt" per_run_usd)
  scap=$(field "$SCRATCH/challenger.txt" per_run_usd)
  ccost=$(field "$SCRATCH/champion.txt" cost_usd)
  scost=$(field "$SCRATCH/challenger.txt" cost_usd)
  if ! awk -v a="$ccap" 'BEGIN { exit (a + 0 > 0) ? 0 : 1 }' || ! awk -v b="$scap" 'BEGIN { exit (b + 0 > 0) ? 0 : 1 }'; then
    fact costs FAIL "the per-run cap this row is about is not on the records: the champion's policy.effective carries per_run_usd '${ccap:-absent}' and the challenger's '${scap:-absent}', and a cap of zero or none is no cap for the two costs to be within. The plan's sentence is 'both costs within the per-run cap' (D567), so the sandbox has to publish one: set budget.per_run_usd in the project layer (or the project policy it publishes) and run this check again on a pair launched under it. The records are in $SCRATCH/champion.out and $SCRATCH/challenger.out"
    return 0
  fi
  if ! awk -v a="$ccap" -v b="$scap" 'BEGIN { exit (a + 0 == b + 0) ? 0 : 1 }'; then
    fact costs FAIL "the two arms ran under different per-run caps: the champion's record carries ${ccap} and the challenger's ${scap}. D567 is that the challenger gets the champion's cap unchanged, so a pair whose two records disagree about it is one where the shadow was budgeted separately — and a figure the report reads across the two arms is then not comparing like with like. The records are in $SCRATCH/champion.out and $SCRATCH/challenger.out"
    return 0
  fi
  if ! awk -v c="$ccost" 'BEGIN { exit (c + 0 > 0) ? 0 : 1 }' || ! awk -v c="$scost" 'BEGIN { exit (c + 0 > 0) ? 0 : 1 }'; then
    fact costs FAIL "one of the pair's records carries no spend: the champion's cost_usd is ${ccost:-absent} and the challenger's ${scost:-absent}. A pair is two runs that did the work, and a record at zero is one that reached no model call — so there is no cost here to compare with the cap, whatever the cap says. 'fugaro runs show ${RUN_ID}' and 'fugaro runs show ${CHALLENGER_ID}' read what the two did, and the records are in $SCRATCH/champion.out and $SCRATCH/challenger.out"
    return 0
  fi
  if ! money_le "$ccost" "$ccap"; then
    fact costs FAIL "the champion's own cost ${ccost} is above the per-run cap ${ccap} its record carries: the cap is what the gateway enforces call by call (D567), so a run that ended above it is one whose ledger and whose policy disagree. Its record is in $SCRATCH/champion.out"
    return 0
  fi
  if ! money_le "$scost" "$scap"; then
    fact costs FAIL "the challenger's own cost ${scost} is above the per-run cap ${scap} its record carries: the shadow runs under the champion's cap unchanged (D567), so a challenger above it is one the gateway did not hold to the same figure — which for a shadow is the whole of what its budget claim rests on. Its record is in $SCRATCH/challenger.out"
    return 0
  fi
  fact costs PASS "both arms are inside the per-run cap, and it is the same cap: champion ${RUN_ID} cost ${ccost} of ${ccap}, challenger ${CHALLENGER_ID} on ${CHALLENGER_MODEL} cost ${scost} of ${scap}. Read from each record's own cost_usd and policy.effective.per_run_usd: the challenger ran on the champion's cap unchanged (D567), and neither record shows a halt at it"
}

# step_judge: the judge run A5.2 ships, launched by hand because the release
# says no run starts one by itself (docs/releases/v0.24.0.md: "The judge run is
# launched by hand with `fugaro run --recipe ab-judge`, one per repository at a
# time"). The check is the hand. It reads the run's own `--json` for its id and
# key, waits for the run to end through the runs list, and then reads its record
# for the one thing the release promises about it: it publishes nothing.
step_judge() {
  if [ "$ARMS_OK" != yes ]; then
    unmeasured judge-run "there is no ended pair for the judge to read: the arms row above says why"
    return 0
  fi
  if [ -z "$JUDGE_MODEL" ]; then
    unmeasured judge-run "the layer named no ab.judge_model (the layer row above says why), so a judge run would be refused rather than judging on a model you did not name (D553, A5.2)"
    return 0
  fi
  say "launching the judge over ${PAIR_ID}: fugaro run --repo ${SANDBOX} --recipe ab-judge --json (one judge run per repository, and it spends on the project's model credential)"
  run_bounded "$JUDGE_TIMEOUT" judge "$SANDBOX" "$FUGARO_BIN" run --repo "$SANDBOX" --recipe ab-judge --json
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured judge-run "the judge launch did not answer inside ${JUDGE_TIMEOUT}s. A judge run is one per repository and it is refused while one has not finished, so a further launch would be refused too: read the repository's runs with 'fugaro runs ls --repo ${SANDBOX} --json', stop the judge with 'fugaro cancel <run> --now' if one is still going, and run this check again. What the launch printed is in $SCRATCH/judge.out and $SCRATCH/judge.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact judge-run FAIL "'fugaro run --repo ${SANDBOX} --recipe ab-judge' exited ${BOUNDED_STATUS}: the judge run is what reads the pair's two patches and writes the verdict the row below reads, and a launch that is refused — because one judge run for this repository has not finished, or because the build's catalog carries no ab-judge recipe, or because the manifest builder found nothing to judge — is one no verdict follows from. What it printed is in $SCRATCH/judge.out and $SCRATCH/judge.err"
    return 0
  fi
  if ! read_json launch "$SCRATCH/judge.out" "$SCRATCH/judge.txt"; then
    fact judge-run FAIL "the judge launch printed a document this check could not read: without its run id the judge cannot be waited for or torn down. What it printed is in $SCRATCH/judge.out"
    return 0
  fi
  JUDGE_ID=$(field "$SCRATCH/judge.txt" run_id)
  JUDGE_REF=$(field "$SCRATCH/judge.txt" run_ref)
  if [ -z "$JUDGE_ID" ]; then
    fact judge-run FAIL "the judge launch named no run (status=$(field "$SCRATCH/judge.txt" status)): this check follows and tears down the run it launched, and a launch that does not name its own cannot be followed. What it printed is in $SCRATCH/judge.out"
    return 0
  fi
  printf '%s\n' "$JUDGE_ID" >>"$LAUNCHES_FILE"
  say "waiting for the judge run ${JUDGE_ID} to end (up to ${RUN_TIMEOUT}s)"
  local waited=0 row="" ended=no
  while [ "$waited" -lt "$RUN_TIMEOUT" ]; do
    run_bounded "$CLI_TIMEOUT" judge-wait "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$SANDBOX" --json --limit "$LS_LIMIT"
    row=""
    if [ "$BOUNDED_STATUS" = 0 ] && read_json rows "$SCRATCH/judge-wait.out" "$SCRATCH/judge-wait.txt"; then
      row=$(rows_row "$SCRATCH/judge-wait.txt" "$JUDGE_ID")
      if [ -n "$row" ] && [ "$(rcol "$row" 4)" = true ]; then
        ended=yes
        [ -n "$JUDGE_REF" ] || JUDGE_REF=$(rcol "$row" 6)
        break
      fi
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$ended" != yes ]; then
    unmeasured judge-run "the judge run ${JUDGE_ID} had not ended after ${RUN_TIMEOUT}s ($([ -n "$row" ] && echo "its row reads status=$(rcol "$row" 3)" || echo 'the runs list does not carry it')). It is a real judge run on the sandbox's budget: this check's teardown cancels it with 'fugaro cancel ${JUDGE_ID} --now' when it ends, and 'fugaro runs show ${JUDGE_ID}' reads what it did"
    return 0
  fi
  if [ -z "$JUDGE_REF" ]; then
    unmeasured judge-run "the judge run ${JUDGE_ID} ended (status $(rcol "$row" 3)) and neither its launch nor the runs list named the key its record is at, so the row below cannot read the verdict object"
    return 0
  fi
  if ! record_of "$JUDGE_REF" judge; then
    unmeasured judge-run "gs://fugaro-runs-${PROJECT}/runs/${JUDGE_REF}/result.json could not be read (exit ${BOUNDED_STATUS}): a run that ended has a record, and it is where the judge's own outcome is. What gcloud printed is in $SCRATCH/judge.err"
    return 0
  fi
  local branch pr outcome
  branch=$(field "$SCRATCH/judge.txt" branch)
  pr=$(field "$SCRATCH/judge.txt" pr)
  outcome=$(field "$SCRATCH/judge.txt" outcome)
  if [ -n "$branch" ] || [ "${pr:-0}" != 0 ]; then
    fact judge-run FAIL "the judge run ${JUDGE_ID} left branch '${branch:-none}' and pr ${pr:-0}: the judge is a mode: evaluate run with no checkout of its own, so it pushes nothing and opens nothing (A5.2, and its own test TestJudgeRunPublishesNothing). A judge run that published is one whose reading of the pair happened somewhere other than the two patch objects, which is the blindness the verdict below rests on. The branch and pull request it names are yours to remove. Its record is in $SCRATCH/judge.out"
    return 0
  fi
  fact judge-run PASS "the judge run ${JUDGE_ID} ran over ${PAIR_ID} and ended (status $(rcol "$row" 3), outcome ${outcome:-none}) publishing nothing: no branch and no pull request (D554, A5.2). It was launched by hand as the release says it is — 'fugaro run --repo ${SANDBOX} --recipe ab-judge', one judge run per repository — and it called ${JUDGE_MODEL}, the model the layer names"
}

# step_verdict: the verdict object, under the judge run's own prefix and nowhere
# else (runstore.VerdictsName). A5.4 scores pairs from it and joins it to the two
# records by pair id alone (D559), so the row reads the object for the one entry
# this pair has: the judge model the layer named, a winner in the three words the
# score speaks, and the two swapped passes. A pair the judge SKIPPED is not a
# verdict — it decided nothing, and reading it as a tie would count a pair nobody
# judged — so a skip reads as a FAIL here.
step_verdict() {
  if [ -z "$JUDGE_REF" ] || [ -z "$PAIR_ID" ]; then
    unmeasured verdict "there is no judge run and pair to read a verdict object for: a row above says why"
    return 0
  fi
  say "reading the judge's own verdict object: gs://fugaro-runs-${PROJECT}/runs/${JUDGE_REF}/ab/verdicts.json"
  run_bounded "$CLI_TIMEOUT" verdicts "$SANDBOX" gcloud storage cat "gs://fugaro-runs-${PROJECT}/runs/${JUDGE_REF}/ab/verdicts.json" --project="${PROJECT}"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured verdict "the judge run ${JUDGE_ID} left no readable verdict object at runs/${JUDGE_REF}/ab/verdicts.json (gcloud exited ${BOUNDED_STATUS}): the judge writes its verdicts under its own prefix, so a judge run that ended without one read the pair and recorded nothing. What gcloud printed is in $SCRATCH/verdicts.err, and the run's own record is in $SCRATCH/judge.out"
    return 0
  fi
  if ! read_json verdicts "$SCRATCH/verdicts.out" "$SCRATCH/verdicts.txt"; then
    unmeasured verdict "the verdict object at runs/${JUDGE_REF}/ab/verdicts.json is not a document this check could read: what gcloud printed is in $SCRATCH/verdicts.out"
    return 0
  fi
  local object_model entries pair_line
  object_model=$(field "$SCRATCH/verdicts.txt" judge_model)
  entries=$(field "$SCRATCH/verdicts.txt" count)
  if [ "$object_model" != "$JUDGE_MODEL" ]; then
    fact verdict FAIL "the verdict object carries judge_model '${object_model:-absent}' and the layer names '${JUDGE_MODEL}': the object records which model judged, D553's 'recorded on the verdict', and one whose model is not the layer's is a judgement no project asked for. The object is in $SCRATCH/verdicts.out"
    return 0
  fi
  pair_line=$(awk -F'\t' -v id="$PAIR_ID" '$1 == "vpair" && $2 == id { print; exit }' "$SCRATCH/verdicts.txt")
  if [ -z "$pair_line" ]; then
    fact verdict FAIL "the verdict object (${entries} entries, judge_model ${object_model}) carries no entry for the pair ${PAIR_ID}: the manifest builder hands the judge the repository's complete and unjudged pairs, so a pair this check saw both arms of and the judge did not read is a pair the two disagree about — 'fugaro runs show ${JUDGE_ID}' reads what the judge run did. The object is in $SCRATCH/verdicts.out"
    return 0
  fi
  local winner skipped passes
  winner=$(rcol "$pair_line" 3)
  skipped=$(rcol "$pair_line" 4)
  passes=$(rcol "$pair_line" 5)
  if [ -n "$skipped" ]; then
    fact verdict FAIL "the judge SKIPPED the pair ${PAIR_ID} with the reason '${skipped}' (${entries} entries in the object): a skipped pair decided nothing and the object says so rather than calling it a tie, so this pair carries no verdict to read — the row is about a pair that was judged. The usual reasons are a missing patch object or a pair the manifest could not assemble; 'fugaro runs show ${JUDGE_ID}' names what happened, and the object is in $SCRATCH/verdicts.out"
    return 0
  fi
  case "$winner" in
  champion | challenger | tie) ;;
  *)
    fact verdict FAIL "the judge's entry for ${PAIR_ID} carries winner '${winner:-absent}', which is none of champion, challenger or tie — the three words the score speaks (score.go). The passes were $(rcol "$pair_line" 6). The object is in $SCRATCH/verdicts.out"
    return 0
    ;;
  esac
  if [ "${passes:-0}" != 2 ]; then
    fact verdict FAIL "the judge's entry for ${PAIR_ID} carries ${passes:-0} pass(es) and the judge makes two per pair — the same two patches shown as A and B, then with the order swapped (D552, A5.2). Its winner is '${winner}': a verdict read from fewer than the two swapped passes is one whose disagreement rule never ran. The object is in $SCRATCH/verdicts.out"
    return 0
  fi
  fact verdict PASS "the judge run ${JUDGE_ID} wrote runs/${JUDGE_REF}/ab/verdicts.json naming judge_model ${object_model} and carrying the pair ${PAIR_ID}: winner '${winner}'${winner:+ }from the two swapped passes ($(rcol "$pair_line" 6)). The verdict is a verdict — the pair was judged, not skipped, by the model the layer names, in the three words the score reads (A5.2, A5.4)"
}

# step_report: the pair through the command the owner reads pairs with. The
# plan's row is that `fugaro ab` lists the pair BELOW THE MINIMUM, which is
# D563's gate: fewer than twenty decisive pairs carries no conclusion rather than
# an approximation, and one pair is far short of it. An installation that has
# already reached twenty decisive pairs in this group cannot show that, and this
# row says it could not measure the fact rather than passing it.
step_report() {
  if [ -z "$CHALLENGER_MODEL" ]; then
    unmeasured ab-report "this check does not know the challenger's model (the challenger row above says why), so it cannot look for the pair in fugaro ab's rows"
    return 0
  fi
  say "reading the pairs report: fugaro ab --repo ${REPO_NAME:-<the sandbox repository>} --since ${SINCE} --json"
  run_bounded "$CLI_TIMEOUT" ab "$SANDBOX" "$FUGARO_BIN" ab --repo "$REPO_NAME" --since "$SINCE" --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured ab-report "fugaro ab --json did not answer (exit ${BOUNDED_STATUS}): the report is how you read the pairs your installation sampled, and it could not be read here. Its output is in $SCRATCH/ab.out and $SCRATCH/ab.err"
    return 0
  fi
  if ! read_json ab "$SCRATCH/ab.out" "$SCRATCH/ab.txt"; then
    unmeasured ab-report "fugaro ab --json printed a document this check could not read: what it printed is in $SCRATCH/ab.out"
    return 0
  fi
  local line pairs="" seen="" mine="" decisive=0
  while IFS= read -r line; do
    case "$line" in ab_row*) ;; *) continue ;; esac
    seen="${seen}${seen:+, }$(printf '%s' "$line" | awk -F'\t' '{ printf "%s in mode %s (%s pair(s), %s decisive)", $5, $2, $6, $11 }')"
    if [ "$(printf '%s' "$line" | awk -F'\t' '{ print $5 }')" = "$CHALLENGER_MODEL" ] &&
      [ "$(printf '%s' "$line" | awk -F'\t' '{ print $6 }')" -ge 1 ]; then
      mine="${mine}${mine:+$'\n'}$line"
    fi
  done <"$SCRATCH/ab.txt"
  if [ -z "$mine" ]; then
    fact ab-report FAIL "fugaro ab --repo ${REPO_NAME} --since ${SINCE} --json carries no row for the challenger model ${CHALLENGER_MODEL} with a pair in it (its rows name ${seen:-nothing}): the judge wrote a verdict for pair ${PAIR_ID} and the command you read pairs with does not show it, so a pair the check saw judged is one the report cannot score. The report's own output is in $SCRATCH/ab.out"
    return 0
  fi
  # The row the check reads is the one the pair is in: with more than one row for
  # the same challenger (a different rigor mode or tier), the lowest decisive
  # count is the one that can still be below the minimum.
  pairs=$(printf '%s\n' "$mine" | sort -t$'\t' -k11,11n | head -n 1)
  decisive=$(printf '%s' "$pairs" | awk -F'\t' '{ print $11 }')
  if [ "${decisive:-0}" -ge 20 ]; then
    unmeasured ab-report "fugaro ab carries a row for ${CHALLENGER_MODEL} with ${decisive} decisive pairs, which is at or above the twenty D563's gate is drawn at: the plan's row is that the report lists THIS pair as below the minimum, and an installation that has already reached the minimum cannot show it. Judge a fresh pair on an installation whose group is still short of twenty decisive pairs — or read the row that is there, whose rule is '$(printf '%s' "$pairs" | awk -F'\t' '{ print $10 }')' and whose decision is '$(printf '%s' "$pairs" | awk -F'\t' '{ print $9 }')'. Its output is in $SCRATCH/ab.out"
    return 0
  fi
  if [ "$(printf '%s' "$pairs" | awk -F'\t' '{ print $10 }')" != below-minimum ]; then
    fact ab-report FAIL "fugaro ab's row for ${CHALLENGER_MODEL} (mode $(printf '%s' "$pairs" | awk -F'\t' '{ print $2 }'), tier $(printf '%s' "$pairs" | awk -F'\t' '{ print $3 }'), champion $(printf '%s' "$pairs" | awk -F'\t' '{ print $4 }'), $(printf '%s' "$pairs" | awk -F'\t' '{ print $6 }') pair(s), ${decisive} decisive) carries the rule '$(printf '%s' "$pairs" | awk -F'\t' '{ print $10 }')' and a conclusion under twenty decisive pairs is D563's rule 'below-minimum' — no figure is drawn from a sample that thin, which is the gate the plan's row is about. Its output is in $SCRATCH/ab.out"
    return 0
  fi
  fact ab-report PASS "fugaro ab --repo ${REPO_NAME} --since ${SINCE} --json shows the pair — mode $(printf '%s' "$pairs" | awk -F'\t' '{ print $2 }'), tier $(printf '%s' "$pairs" | awk -F'\t' '{ print $3 }'), champion $(printf '%s' "$pairs" | awk -F'\t' '{ print $4 }'), challenger $(printf '%s' "$pairs" | awk -F'\t' '{ print $5 }'), pairs $(printf '%s' "$pairs" | awk -F'\t' '{ print $6 }'), ${decisive} decisive — and its own recommendation says '$(printf '%s' "$pairs" | awk -F'\t' '{ print $9 }')' by the rule below-minimum: the pair the judge judged is a pair the report scores, and the report declines to draw a conclusion from it, which is D563 read where you read it"
}

# restore_rows: the runs list as the teardown reads it, and whether the read
# worked. It is read again after every cancel below, because a run that was
# going can push its branch and open its pull request in the seconds before the
# cancel reaches it, and a teardown that read the list once would report what
# the run had left when it looked.
restore_rows() {
  run_bounded "$CLI_TIMEOUT" teardown "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$SANDBOX" --json --limit "$LS_LIMIT"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then return 1; fi
  read_json rows "$SCRATCH/teardown.out" "$SCRATCH/teardown.txt"
}

# restore_wait_ended ID: wait, up to CANCEL_TIMEOUT, for a run this check
# cancelled to read as ended. `fugaro cancel --now` cancels the execution
# through the backend, and the run's own row is what says it stopped.
restore_wait_ended() {
  local waited=0 row
  while [ "$waited" -lt "$CANCEL_TIMEOUT" ]; do
    restore_rows || return 1
    row=$(rows_row "$SCRATCH/teardown.txt" "$1")
    if [ -n "$row" ] && [ "$(rcol "$row" 4)" = true ]; then return 0; fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  return 1
}

# restore: what this check changed, said rather than assumed. It is the teardown
# the plan asks a check of this shape to carry, and nothing this check launches
# outlives it: a launched run that has not ended is cancelled with `fugaro cancel
# <run> --now` and read back as ended before its branch and its pull request are
# dealt with, because a run still going pushes and opens AFTER a teardown that
# read only what it had left so far — a Restore that skipped it would read PASS
# over a push that had not happened yet. Then every pull request these launches
# opened is closed and every branch they pushed is deleted, each read back. A
# pull request already closed and a branch already gone are the state it wants,
# not a failure; a launched run still going after the cancel, or one the runs
# list does not carry at all, IS one — the row names it, and names the commands
# that settle it by run id, because this check's list of its own launches goes
# with its scratch directory and running the check again does not reach them.
restore() {
  RESTORE_VERDICT=PASS
  local ids="" id row branch pr close_failed="" deleted="" kept="" note=""
  local going="" absent="" cancelled="" cancel_status="" cancel_note="" why=""
  if [ ! -s "$LAUNCHES_FILE" ] || [ -z "$PROJECT" ]; then
    RESTORE_NOTE="nothing was created: ${LAUNCHED:-no} launch(es) were made and no branch or pull request of this check's own is left. The scratch directory held the commands' output"
    return 0
  fi
  ids=$(sort -u "$LAUNCHES_FILE" | tr '\n' ' ')
  if ! restore_rows; then
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="the runs this check launched are ${ids}and their branches and pull requests could not be read back (fugaro runs ls --json exited ${BOUNDED_STATUS}; its output is in $SCRATCH/teardown.out and $SCRATCH/teardown.err), so nothing was closed or deleted. Settle them by run id: read one with 'fugaro runs show <run>', stop one that is still going with 'fugaro cancel <run> --now', then close its pull request with 'gh pr close <n> --repo ${REPO_NAME:-<owner/name>}' and delete its branch with 'git push origin --delete <branch>' in ${SANDBOX}"
    return 0
  fi
  [ -n "$REPO_NAME" ] || REPO_NAME=$(field "$SCRATCH/teardown.txt" repo)
  for id in $ids; do
    row=$(rows_row "$SCRATCH/teardown.txt" "$id")
    if [ -z "$row" ]; then
      absent="${absent}${absent:+, }${id}"
      continue
    fi
    if [ "$(rcol "$row" 4)" != true ]; then
      run_bounded "$CANCEL_TIMEOUT" "teardown-cancel" "$SANDBOX" "$FUGARO_BIN" cancel "$id" --now --json
      if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
        going="${going}${going:+, }${id} ('fugaro cancel ${id} --now' exited ${BOUNDED_STATUS})"
        continue
      fi
      cancel_status=$(python3 -c 'import json,sys; print(((json.load(open(sys.argv[1])) or {}).get("status") or ""))' "$SCRATCH/teardown-cancel.out" 2>/dev/null || true)
      if ! restore_wait_ended "$id"; then
        going="${going}${going:+, }${id} (still going after 'fugaro cancel ${id} --now' answered '${cancel_status:-no status}')"
        continue
      fi
      cancelled="${cancelled}${cancelled:+, }${id}"
      row=$(rows_row "$SCRATCH/teardown.txt" "$id")
    fi
    branch=$(rcol "$row" 7)
    pr=$(rcol "$row" 8)
    if [ -n "$branch" ]; then
      run_bounded "$CLI_TIMEOUT" "teardown-ls" "$SANDBOX" git ls-remote --heads origin "refs/heads/${branch}"
      # A branch the remote no longer holds is the state this wants, not a
      # failure: the teardown may be the second one to run, and deleting a
      # branch twice is not a reading of anything.
      if [ "$BOUNDED_STATUS" = 0 ] && grep -q "refs/heads/${branch}\$" "$SCRATCH/teardown-ls.out"; then
        run_bounded "$CLI_TIMEOUT" "teardown-rm" "$SANDBOX" git push origin --delete "$branch"
        if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
          close_failed="${close_failed}${close_failed:+, }the branch ${branch} (git push --delete exited ${BOUNDED_STATUS})"
        else
          run_bounded "$CLI_TIMEOUT" "teardown-ls2" "$SANDBOX" git ls-remote --heads origin "refs/heads/${branch}"
          if [ "$BOUNDED_STATUS" = 0 ] && grep -q "refs/heads/${branch}\$" "$SCRATCH/teardown-ls2.out"; then
            close_failed="${close_failed}${close_failed:+, }the branch ${branch} (still on the remote after git push --delete)"
          else
            deleted="${deleted}${deleted:+, }${branch}"
          fi
        fi
      fi
    fi
    if [ "${pr:-0}" != 0 ]; then
      if [ -z "$GH_BIN" ]; then
        kept="${kept}${kept:+, }pull request ${pr}"
        continue
      fi
      run_bounded "$CLI_TIMEOUT" "teardown-pr" "$SANDBOX" "$GH_BIN" pr view "$pr" --repo "$REPO_NAME" --json state
      local state=""
      if [ "$BOUNDED_STATUS" = 0 ]; then
        state=$(python3 -c 'import json,sys; print(((json.load(open(sys.argv[1])) or {}).get("state") or ""))' "$SCRATCH/teardown-pr.out" 2>/dev/null || true)
      fi
      if [ "$state" = OPEN ]; then
        run_bounded "$CLI_TIMEOUT" "teardown-close" "$SANDBOX" "$GH_BIN" pr close "$pr" --repo "$REPO_NAME"
        if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
          close_failed="${close_failed}${close_failed:+, }pull request ${pr} (gh pr close exited ${BOUNDED_STATUS})"
          continue
        fi
      fi
      run_bounded "$CLI_TIMEOUT" "teardown-check" "$SANDBOX" "$GH_BIN" pr view "$pr" --repo "$REPO_NAME" --json state
      state=""
      if [ "$BOUNDED_STATUS" = 0 ]; then
        state=$(python3 -c 'import json,sys; print(((json.load(open(sys.argv[1])) or {}).get("state") or ""))' "$SCRATCH/teardown-check.out" 2>/dev/null || true)
      fi
      if [ "$state" != CLOSED ]; then
        close_failed="${close_failed}${close_failed:+, }pull request ${pr} (read back as '${state:-unreadable}')"
      fi
    fi
  done
  if [ -n "$going" ]; then
    why="these launches had not ended: 'fugaro cancel <run> --now' was run on each of them and the runs list still reads them as going — ${going}"
  fi
  if [ -n "$absent" ]; then
    why="${why}${why:+; }the runs list does not carry these launches at all: ${absent}"
  fi
  if [ -n "$why" ]; then
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="the runs this check launched are ${ids}and ${why}. A run that is still going is settled by nothing this teardown reads — it can push its branch and open its pull request after this check is over — so settle each one by run id: read it with 'fugaro runs show <run>', stop it with 'fugaro cancel <run> --now', then close its pull request with 'gh pr close <n> --repo ${REPO_NAME:-<owner/name>}' and delete its branch with 'git push origin --delete <branch>' in ${SANDBOX}. Running this check again does not settle them: the list of the runs it launched is removed with its scratch directory"
    return 0
  fi
  if [ -n "$close_failed" ]; then
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="the runs this check launched are ${ids}and ${close_failed} could NOT be closed or deleted. Close the pull request with 'gh pr close <n> --repo ${REPO_NAME}' and delete the branch with 'git push origin --delete <branch>' in ${SANDBOX}: closing a closed pull request and deleting a deleted branch are the state this wants, so both commands are safe to run again"
    return 0
  fi
  note="${deleted:+ deleted ${deleted};}"
  if [ -n "$kept" ]; then
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="the runs this check launched are ${ids}their branches were${note:-} deleted, and ${kept} could NOT be closed: 'gh' is not on PATH, and a pull request is the one thing no other tool here closes. Close each one with 'gh pr close <n> --repo ${REPO_NAME:-<owner/name>}' — they are sandbox pull requests and closing them is the whole of it"
    return 0
  fi
  if [ -n "$cancelled" ]; then
    cancel_note="the launches that had not ended (${cancelled}) were cancelled with 'fugaro cancel <run> --now' and read back as ended, and "
  fi
  RESTORE_NOTE="the runs this check launched are ${ids}${cancel_note}every branch they pushed was deleted${note:-} and every pull request their champions opened is closed, each read back from the forge. The records and the verdict object stay in the bucket, which is what the rows above were read from"
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
  say "CHECK 212 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [offline] fugaro version and fugaro ab --help — the release ${RELEASE} gate, read off the installed build and no checkout"
  say "  [network, read] fugaro doctor --json and git remote get-url origin in ${SANDBOX} — the project this checkout belongs to and its owner/name"
  say "  [network, read] fugaro recipes ls --json in ${SANDBOX} — the project's own catalog, which has to carry a launchable ab-judge recipe for the judge run below"
  say "  [network, read] fugaro config layer --json in ${SANDBOX} — the published project layer, whose ab: block must set a rate above zero, name challengers and name a judge_model (and ${SANDBOX}/fugaro.yaml must carry no opt-out)"
  say "  [offline, read] fugaro models ls --json in ${SANDBOX} — each challenger must be a provider model (not the direct route) whose allow-list admits ${REPO_NAME:-<owner/name>}"
  say "  [network, BILLABLE] fugaro run --repo ${SANDBOX} --json \"${TASK}\" — up to ${MAX_LAUNCHES} sandbox runs, stopped at the first pair. About ten are expected at 0.10; nothing forces a sample"
  say "  [read] fugaro runs ls --repo ${SANDBOX} --batch <the pair's label> --json — the two arms, both ended"
  say "  [read] gcloud storage cat gs://fugaro-runs-<the project>/runs/<the slug>/<run>/result.json for BOTH arms — the arm, the outcome, the branch, the pull request, both wall times and both costs against the per-run cap"
  say "  [network, BILLABLE] fugaro run --repo ${SANDBOX} --recipe ab-judge --json — ONE judge run over the pair, launched by hand as the release says; it calls ab.judge_model twice per pair and publishes nothing"
  say "  [read] gcloud storage cat of the judge run's own ab/verdicts.json — the pair's verdict, the judge model and the two swapped passes"
  say "  [read] fugaro ab --repo ${REPO_NAME:-<owner/name>} --since ${SINCE} --json — the pair through the command you read pairs with, listed below the minimum"
  say "  [WRITE, the teardown] a launch that has not ended is stopped with fugaro cancel --now and read back, then every branch these launches pushed is deleted and every pull request their champions opened is closed with gh, each read back"
  for key in release judge-recipe sandbox layer route pair arms champion challenger wall-times costs judge-run verdict ab-report; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no doctor, no catalog read, no layer read, no model read, no launch, no judge run, no remote read and no teardown"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version release judge-recipe xtrace agent-session belong bound
  sandbox layer route pair arms champion challenger wall-times costs judge-run
  verdict ab-report restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 212 RESULT\n' >&3
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
  printf 'CHECK 212 RESULT END (exit %s)\n' "$status" >&3
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
TASK_CLI=""
MAX_CLI=""
YES_CLI=""
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of a repository of the sandbox project, which is what the runs are launched from"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --task)
    [ $# -ge 2 ] || die "--task needs the text each launch is launched with; it has to be small, because the check launches up to ${MAX_LAUNCHES:-30} of them"
    TASK_CLI=$2
    shift 2
    ;;
  --max-launches)
    [ $# -ge 2 ] || die "--max-launches needs a count: how many launches the check may spend looking for a pair (the default is 30, the bound the plan sets)"
    MAX_CLI=$2
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
SANDBOX=${SANDBOX_CLI:-${CHECK212_SANDBOX:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 212 needs --sandbox: the checkout of a repository of the sandbox project. It is what the runs are launched from and what its configuration is read through. Usage: bash scripts/live-checks/check212.sh --sandbox <a sandbox repository checkout> --yes"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
TASK=${TASK_CLI:-${CHECK212_TASK:-"Add a line reading 'check 212' to CHECK212.md, and change nothing else."}}
MAX_LAUNCHES=${MAX_CLI:-${CHECK212_MAX_LAUNCHES:-30}}
case "$MAX_LAUNCHES" in
"" | *[!0-9]*) die "--max-launches needs a whole number, not '${MAX_LAUNCHES}'" ;;
esac
[ "$MAX_LAUNCHES" -ge 1 ] || die "--max-launches needs at least 1"
# The bound the teardown reads the runs list with: its own launches are the
# newest rows, and a pair is two rows plus the judge run, so the window is twice
# the bound plus room for the rows another run of the sandbox added beside them.
LS_LIMIT=$((MAX_LAUNCHES * 2 + 10))
RELEASE=${CHECK212_RELEASE:-0.24.0}
SINCE=${CHECK212_SINCE:-1d}
YES=${CHECK212_YES:-$YES_CLI}
DRY_RUN=${CHECK212_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK212_KEEP_SCRATCH:-}
REGION=${CHECK212_REGION:-us-east5}
RUN_TIMEOUT=${CHECK212_RUN_TIMEOUT_SECONDS:-3600}
POLL_SECONDS=${CHECK212_POLL_SECONDS:-15}
CLI_TIMEOUT=${CHECK212_CLI_TIMEOUT_SECONDS:-180}
# The bound for both halves of the teardown's cancel: the `fugaro cancel --now`
# this check runs on a launched run that has not ended, and the wait for that
# run's row to read as ended afterwards.
CANCEL_TIMEOUT=${CHECK212_CANCEL_TIMEOUT_SECONDS:-300}
LAUNCH_TIMEOUT=${CHECK212_LAUNCH_TIMEOUT_SECONDS:-600}
DOCTOR_TIMEOUT=${CHECK212_DOCTOR_TIMEOUT_SECONDS:-300}
# The judge run is a whole run of its own — it assembles the pairs' bundles and
# makes its passes — so it gets a run's own bound rather than a launch's.
JUDGE_TIMEOUT=${CHECK212_JUDGE_TIMEOUT_SECONDS:-3600}
# Resolved before anything runs, and by absolute path: this check drives a real
# `fugaro` that reaches the cloud, so it must be the one on PATH when the check
# started rather than one a later step could put there. `gh` is not required —
# the branches are deleted without it — so a missing one is left empty and the
# restore row says which pull requests are therefore yours to close.
FUGARO_BIN=$(command -v fugaro || true)
GH_BIN=$(command -v gh || true)
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

main() {
  need_tools
  [ -n "$FUGARO_BIN" ] || die "Check 212 needs 'fugaro' on PATH: the check drives the build you have installed, and it is not there"
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
    die "Check 212 launches up to ${MAX_LAUNCHES} sandbox runs and one judge run, and spends what their tasks cost, so it asks for --yes. Nothing has been launched. Type: bash scripts/live-checks/check212.sh --sandbox ${SANDBOX} --yes (or read the plan first with --dry-run)"
  fi
  step_sandbox
  step_judge_recipe
  step_layer
  step_route
  step_pair
  step_arms
  step_champion
  step_challenger
  step_walltimes
  step_costs
  step_judge
  step_verdict
  step_report
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
