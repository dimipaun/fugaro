#!/usr/bin/env bash
# Check 212 — a provider challenger, judged, end to end.
#
# Plan task A6.2 writes this check; what it settles is docs/model-ab-testing/
# implementation-plan.md's "Checks 210-212" and U4 of reconciliation.md (D550,
# D554, D556, D559, D563, D565): a run of the sandbox repository that the
# sampler PAIRED, whose challenger is a provider model the project layer allows
# and the project routes for that repository, whose two arms both ended, whose
# judge run wrote a verdict object for the pair, and which `fugaro ab` lists as
# below the minimum — with both arms' wall times recorded beside it, which is
# U4's own reading of a shadow on a provider route.
#
# What it measures:
#
#   * the release the check needs: `fugaro version` and the presence of the
#     report itself (`fugaro ab --help` names --since). The work is unreleased
#     until 0.24.0, so a build without it stops here with the release named
#     rather than measuring a pair nothing can report on;
#   * the layer, from `fugaro config layer --json`, read in the sandbox
#     checkout: an `ab:` block the sampler can act on — a rate above zero, a
#     challenger list, and the judge model. A layer with none of it samples
#     nothing, so there is nothing here to measure;
#   * the challenger, from `fugaro models ls --json`: the model the launcher
#     will take (the first entry of ab.challengers) is a provider model — not a
#     Claude ID, and not served by the direct Anthropic route — whose route
#     allow_data_to names this repository, and which is priced. It must not be
#     the repository's own coder pin;
#   * the pair: sandbox runs launched with `fugaro run --json` until one is
#     sampled. The launcher samples by a hash of the run id the CLI mints, so
#     the roll is not the check's to force: `--run-id`, `--retry` and an
#     `--attempts` above one are all ineligible, and only a fresh launch can
#     pair. Each launch that pairs nothing is an ordinary champion run;
#   * both arms terminal, their two records read from the runs bucket as you
#     would read them: the pair id both carry, the champion's arm publishing as
#     it always did, the challenger's arm shadowing with `publish: none` and
#     the provider model named on it;
#   * the two wall times (U4), and both costs against the run's own per-run cap;
#   * the judge run (`fugaro run --recipe ab-judge`) and the verdict object it
#     leaves at ab/verdicts.json: the entry for THIS pair, judged by the model
#     the layer names, with its two passes agreeing on the winner;
#   * `fugaro ab --repo <owner/name> --json`: the pair is in the figures, and
#     the row for the two models carries the pair — with fewer than the
#     minimum's decisive pairs, no conclusion and the rule below-minimum.
#
# Usage:      bash scripts/live-checks/check212.sh --sandbox <a sandbox repository checkout> --yes
#             bash scripts/live-checks/check212.sh --sandbox <path> --challenger <model id> --yes
#             bash scripts/live-checks/check212.sh --sandbox <path> --task "<the task>" --yes
#             bash scripts/live-checks/check212.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check212.sh --help
# Needs:      bash, mktemp, date, python3 (the `--json` documents are read with
#             it), gcloud (authenticated as YOU, and able to read the runs
#             bucket) and fugaro (the installed release: the whole point is the
#             build you have).
# Where:      your own machine and the **sandbox project** and the **sandbox
#             repository** only. A project or region naming belong is refused by
#             name, and so is a coding agent's session: every command this check
#             runs is one you could type yourself.
# Creates:    sandbox runs, and nothing else: one pair (its champion, and the
#             shadow beside it) plus whatever launches paired nothing, plus the
#             judge run. The champion and every unpaired launch open their own
#             pull requests, which are left for you to close. On disk: one
#             scratch directory, removed at the end, holding the commands'
#             output.
# Costs:      BILLABLE, and the reason `--yes` is required. At the layer's
#             ab.rate the expected number of launches to pair one is about
#             1/rate — ten at the ceiling of 0.10 — and every launch is a real
#             sandbox run on the project's budget. The pair adds one shadow run
#             and the judge run adds two model calls per pair it reads.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check212_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C, and the runs this check is still following,
#             cancelled from that same trap if they have not ended. The runs
#             that paired nothing are NOT cancelled: they are ordinary runs, and
#             their pull requests are yours to close. Nothing else is created,
#             so nothing else is restored.
# Output:     progress on stderr, ONE block on stdout headed CHECK 212 RESULT,
#             then CHECK 212 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold, or a guard stopped the run — among
#             them a build older than the release the work ships in. 2: the
#             check could not measure — a tool is missing, the checkout is not
#             one, a `--json` document could not be read, or a run had not ended
#             and had to be cancelled. A key the run never reached reads
#             `FAIL — not reached`.
#
# A NOTE ON WHAT IS NEVER PRINTED: no credential value reaches this script's
# stdout, its stderr, the block or the scratch directory. The rows carry model
# ids, run ids, pair ids, statuses, times and dollars, and never a task's text
# beyond the task this check itself launches with.
#
# Knobs (all CHECK212_*): DRY_RUN, KEEP_SCRATCH, YES, SANDBOX (the checkout,
# when you would rather set it in the environment), CHALLENGER (the challenger
# to grade, when the list leads with one you would rather not measure), TASK
# (what the runs are launched with), RELEASE (0.24.0: the release this work
# ships in), REGION (us-east5), MAX_LAUNCHES (30: the most sandbox runs the
# check will launch to pair one), RUN_TIMEOUT_SECONDS (3600), JUDGE_TIMEOUT_
# SECONDS (2400), POLL_SECONDS (15), CLI_TIMEOUT_SECONDS (180), LAUNCH_TIMEOUT_
# SECONDS (600), CANCEL_TIMEOUT_SECONDS (300), DOCTOR_TIMEOUT_SECONDS (300),
# AB_TIMEOUT_SECONDS (600), LATENCY_MAX_RATIO (3: the most the two wall times
# may differ by before the row calls the profile apart).
#
# No xtrace, whatever the shell it was started from had: this check drives a
# real `fugaro` at a real terminal, and a trace of that is a second copy of what
# its own output holds.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

set -Eeuo pipefail

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
  for t in mktemp date python3 git gcloud fugaro; do
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
LAUNCH_REPO=""
SLUG=""
PAIR_ID=""
PAIR_BATCH=""
CHAMPION_ID=""
CHALLENGER_ID=""
JUDGE_ID=""
CHALLENGER_MODEL=""
JUDGE_MODEL=""
RATE=""
LAYER_OK=no
MODELS_OK=no
PAIRED=no
LAUNCHES=0
# FOLLOW_RUNS is every run this check is still waiting on. A run leaves it the
# moment it ends on its own, and what is left when the check gives up is what it
# cancels — from the step that gives up AND from the exit trap, so a Ctrl-C
# cancels the same set. The launches that paired nothing never enter it: they
# are ordinary runs whose pull requests are the owner's to close.
FOLLOW_RUNS=""
CANCEL_DONE=no
CANCEL_OK=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk212.XXXXXXXX") || die "Check 212 cannot make a scratch directory"
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
    say "CHECK212_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The `--json` documents this check reads, in one program: a live check on the
# owner's machine has python3 and no jq, and a row that read a document with
# grep would report a field the release did not write the day the document is
# re-wrapped. Every reader prints `key=value` lines or `\t`-separated rows, so a
# shell can hold them without a second parser.
#
# The layer's YAML is the one document with no JSON in it, and it is parsed here
# too rather than with grep: the block the check grades is three keys deep in a
# file the owner wrote by hand, and a reader that matched a line by its text
# alone would read `rate:` out of a comment.
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import datetime
import json
import sys


def load(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def yes(value):
    return "true" if value else "false"


def flat(value):
    return " ".join(str(value or "").split())


def wall_seconds(started, finished):
    if not started or not finished:
        return None
    try:
        a = datetime.datetime.fromisoformat(str(started).replace("Z", "+00:00"))
        b = datetime.datetime.fromisoformat(str(finished).replace("Z", "+00:00"))
    except ValueError:
        return None
    return round((b - a).total_seconds(), 1)


def strip_comment(line):
    out = []
    quote = ""
    for ch in line:
        if quote:
            out.append(ch)
            if ch == quote:
                quote = ""
            continue
        if ch in ("'", '"'):
            quote = ch
            out.append(ch)
            continue
        if ch == "#":
            break
        out.append(ch)
    return "".join(out)


def scalar(text):
    text = text.strip()
    if len(text) >= 2 and text[0] == text[-1] and text[0] in ("'", '"'):
        return text[1:-1]
    return text


def block(text, key):
    """The top-level `key:` block of a layer file, as {name: value}.

    A value is the text after the colon, `[a, b]` read as a list, and a
    following `- item` sequence appended to the name before it. That is every
    shape the keys this check grades are written in; a nested block reads as an
    empty value, which is what tells a reader here it is not a scalar.
    """
    lines = text.splitlines()
    start = None
    for i, raw in enumerate(lines):
        line = strip_comment(raw).rstrip()
        if not line.strip():
            continue
        if not line[:1].isspace() and line.startswith(key + ":"):
            start = i
            break
    if start is None:
        return None
    if strip_comment(lines[start]).rstrip()[len(key) + 1:].strip() not in ("", "{}", "~", "null"):
        return {}
    out = {}
    indent = None
    current = None
    for raw in lines[start + 1:]:
        line = strip_comment(raw).rstrip()
        if not line.strip():
            continue
        stripped = line.lstrip()
        ind = len(line) - len(stripped)
        if ind == 0:
            break
        if indent is None:
            indent = ind
        if ind < indent:
            break
        if stripped.startswith("- "):
            if current:
                out.setdefault(current, []).append(scalar(stripped[2:]))
            continue
        if ind > indent:
            continue
        name, _, value = stripped.partition(":")
        name = name.strip()
        value = value.strip()
        current = name
        if value.startswith("[") and value.endswith("]"):
            out[name] = [item for item in (scalar(v) for v in value[1:-1].split(",")) if item]
        elif value == "":
            out[name] = []
        else:
            out[name] = scalar(value)
    return out


what = sys.argv[1]
doc = load(sys.argv[2])

if what == "layer":
    print("where=%s" % flat(doc.get("where")))
    print("generation=%s" % (doc.get("generation") or 0))
    print("sha256=%s" % flat(doc.get("sha256")))
    ab = block(doc.get("yaml") or "", "ab")
    print("present=%s" % yes(ab is not None))
    if ab is not None:
        print("rate=%s" % ab.get("rate", ""))
        print("max_pairs_per_day=%s" % ab.get("max_pairs_per_day", ""))
        print("judge_model=%s" % flat(ab.get("judge_model")))
        print("challengers=%s" % " ".join(ab.get("challengers") or []))
elif what == "models":
    repo = sys.argv[3] if len(sys.argv) > 3 else ""
    routes = {}
    for route in doc.get("routes") or []:
        routes[route.get("name")] = route
        print("route\t%s\t%s\t%s\t%s" % (
            route.get("name") or "",
            route.get("kind") or "",
            yes(route.get("open_to_every_repo")),
            " ".join(route.get("allow_data_to") or []),
        ))
    for model in doc.get("models") or []:
        route = routes.get(model.get("route")) or {}
        open_all = yes(route.get("open_to_every_repo"))
        allowed = route.get("allow_data_to") or []
        routed = "true" if (open_all == "true" or repo in allowed) else "false"
        print("model\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s" % (
            model.get("id") or "",
            model.get("route") or "",
            model.get("source") or "",
            model.get("input_per_m") or 0,
            model.get("output_per_m") or 0,
            " ".join(model.get("allowed_to") or []),
            open_all,
            routed,
            flat(model.get("refused")),
        ))
    for entry in doc.get("repositories") or []:
        print("repo\t%s\t%s\t%s\t%s" % (
            entry.get("name") or "",
            entry.get("state") or "",
            flat(entry.get("coder")),
            flat(entry.get("reviewer")),
        ))
elif what == "launch":
    pair = doc.get("pair") or {}
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("repo=%s" % (doc.get("repo") or ""))
    print("status=%s" % (doc.get("status") or ""))
    print("pair_id=%s" % (pair.get("id") or ""))
    print("pair_batch=%s" % (pair.get("batch") or ""))
    print("pair_challenger=%s" % (pair.get("challenger") or ""))
    print("pair_reason=%s" % flat(pair.get("reason")))
elif what == "rows":
    rows = doc.get("runs") or []
    print("count=%d" % len(rows))
    for row in rows:
        print("row\t%s\t%s\t%s\t%s\t%s\t%s\t%s" % (
            row.get("run_id") or "",
            row.get("status") or "",
            yes(row.get("terminal")),
            flat(row.get("stage")),
            row.get("run") or "",
            row.get("batch") or "",
            row.get("recipe") or "",
        ))
elif what == "record":
    cost = doc.get("cost") or {}
    ab = doc.get("ab") or {}
    policy = (doc.get("policy") or {}).get("effective") or {}
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("repo=%s" % (doc.get("repo") or ""))
    print("status=%s" % (doc.get("status") or ""))
    print("outcome=%s" % (doc.get("outcome") or ""))
    print("stage=%s" % flat(doc.get("stage")))
    print("ab_present=%s" % yes(bool(ab)))
    print("ab_pair_id=%s" % (ab.get("pair_id") or ""))
    print("ab_arm=%s" % (ab.get("arm") or ""))
    print("ab_publish=%s" % (ab.get("publish") or ""))
    print("ab_challenger=%s" % (ab.get("challenger") or ""))
    print("ab_challenger_source=%s" % (ab.get("challenger_source") or ""))
    print("pr_url=%s" % (((doc.get("pr") or {}).get("url")) or ""))
    print("started=%s" % (doc.get("started_at") or ""))
    print("finished=%s" % (doc.get("finished_at") or ""))
    wall = wall_seconds(doc.get("started_at"), doc.get("finished_at"))
    print("wall_s=%s" % ("" if wall is None else wall))
    print("cost_usd=%s" % (cost.get("model_usd") or 0))
    print("per_run_usd=%s" % (policy.get("per_run_usd") or 0))
    print("budget_mode=%s" % (policy.get("mode") or ""))
    for name, usd in sorted((cost.get("model_by") or {}).items()):
        print("model\t%s\t%s" % (name, usd))
    for stage in doc.get("stages") or []:
        print("stage\t%s\t%s\t%s" % (
            stage.get("name") or "",
            flat(stage.get("model")),
            stage.get("model_usd") or 0,
        ))
elif what == "verdicts":
    print("version=%s" % (doc.get("version") or 0))
    print("judge_model=%s" % flat(doc.get("judge_model")))
    pairs = doc.get("pairs") or []
    print("count=%d" % len(pairs))
    for pair in pairs:
        print("pair\t%s\t%s\t%s\t%s\t%d" % (
            pair.get("pair_id") or "",
            pair.get("winner") or "",
            flat(pair.get("reason")),
            flat(pair.get("skipped")),
            len(pair.get("passes") or []),
        ))
        for i, one in enumerate(pair.get("passes") or []):
            print("pass\t%s\t%d\t%s\t%s\t%s" % (
                pair.get("pair_id") or "",
                i + 1,
                yes(one.get("swap")),
                flat(one.get("label")),
                one.get("arm") or "",
            ))
elif what == "ab":
    print("project=%s" % flat(doc.get("project")))
    print("source=%s" % flat(doc.get("source")))
    print("since=%s" % flat(doc.get("since")))
    print("until=%s" % flat(doc.get("until")))
    rows = doc.get("rows") or []
    print("count=%d" % len(rows))
    for row in rows:
        rec = row.get("recommendation") or {}
        print("row\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s" % (
            row.get("mode") or "",
            row.get("tier") or "",
            row.get("champion") or "",
            row.get("challenger") or "",
            row.get("pairs") or 0,
            row.get("wins") or 0,
            row.get("losses") or 0,
            row.get("ties") or 0,
            rec.get("decisive") or 0,
            rec.get("decision") or "",
            rec.get("rule") or "",
            row.get("unjudged") or 0,
            row.get("truncated") or 0,
        ))
elif what == "cancel":
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("status=%s" % (doc.get("status") or ""))
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

read_json() { # read_json QUERY FILE DEST [EXTRA...]
  local query=$1 file=$2 dest=$3
  shift 3
  python3 "$SCRATCH/read.py" "$query" "$file" "$@" >"$dest" 2>"$dest.err"
}

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# row_field FILE RUN_ID COLUMN: one column of the row whose run_id is RUN_ID,
# out of a `fugaro runs ls --json` listing. The run's own id is the only row
# that counts: a list read by position alone could be another run's.
row_field() { # row_field FILE RUN_ID COLUMN
  awk -F'\t' -v id="$2" -v col="$3" '$1 == "row" && $2 == id { print $col; exit }' "$1" 2>/dev/null
}

# run_bounded SECONDS KEY DIR CMD...: run one command from the directory it has
# to be run in and under a bound.
#
# The child execs, so the pid the bound kills IS the command: a hung cloud call
# would otherwise outlive the check that gave up on it, which is the one thing
# the bound exists to prevent. The ERR trap is dropped in it too: this check runs
# commands whose failing exit status is one of its own readings (a cancel that
# had nothing to do, a record that is not there), and a trap inherited here
# would turn each of them into "an internal error" and a status of 2 before the
# step could report it.
run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  BOUNDED_TIMED_OUT=no
  # Truncated, not appended: a key is run again by every turn of a poll loop
  # (`rows` above all), and a reader that met two documents in one file would
  # refuse it — while the output a failing step names is always the LAST run of
  # that key, which is the one a person needs to see.
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

follow() { FOLLOW_RUNS="${FOLLOW_RUNS:+$FOLLOW_RUNS }$1"; }

# settled: this run ended on its own, so nothing is left to cancel. It is what
# makes the cancelled row's "not needed" true: a run that left the follow set
# ended by itself and only a run still in it can be cancelled.
settled() { # settled RUN_ID
  local kept="" id
  for id in $FOLLOW_RUNS; do
    [ "$id" = "$1" ] && continue
    kept="${kept:+$kept }$id"
  done
  FOLLOW_RUNS=$kept
}

cancel_run() { # cancel_run WHY
  [ -n "$FOLLOW_RUNS" ] || return 0
  [ "$CANCEL_DONE" = yes ] && return 0
  CANCEL_DONE=yes
  local id ok=yes
  say "cancelling the runs this check is still following ($1): fugaro cancel <run> --now"
  for id in $FOLLOW_RUNS; do
    run_bounded "$CANCEL_TIMEOUT" cancel "$SANDBOX" "$FUGARO_BIN" cancel "$id" --now --json
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
      ok=no
      fact cancelled FAIL "the run ${id} had not ended, so this check cancelled it with 'fugaro cancel ${id} --now' and the command did not answer (exit ${BOUNDED_STATUS}): it may still be going. Read it with 'fugaro runs show ${id}' and cancel it yourself. What the command printed is in $SCRATCH/cancel.out and $SCRATCH/cancel.err"
      continue
    fi
    fact cancelled PASS "the run ${id} did not end on its own, so this check cancelled it with 'fugaro cancel ${id} --now' (status $(field "$SCRATCH/cancel.txt" status)): nothing this check was still following outlives it"
  done
  [ "$ok" = yes ] && CANCEL_OK=yes
  return 0
}

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check212.sh --sandbox <a sandbox repository checkout> --yes — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy the runs' own output to the terminal"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the runs' output"
}

# guard_agent_session: the check is the owner's, and it drives commands whose
# writes a coding agent's session is refused. Refusing here rather than letting
# each command refuse one at a time is what makes the block say why.
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

# step_release: the gate every check of an unreleased feature carries. The work
# ships in 0.24.0, and a build that predates it has neither `fugaro ab` nor the
# verdicts object, so a pair measured here would be a reading of a build nobody
# shipped this for. Both probes are offline: `fugaro version` and the usage line
# `fugaro ab` prints.
step_release() {
  local v feature=no
  v=$("$FUGARO_BIN" version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing}"
  if "$FUGARO_BIN" ab --help 2>/dev/null | grep -q -- '--since'; then feature=yes; fi
  if [ "$feature" = no ]; then
    fact release FAIL "this fugaro has no 'ab' report: 'fugaro ab --help' does not answer with the report's own flags, so neither the figures the last row reads nor the verdicts a judge run writes are in this build. Check 212 needs release ${RELEASE} (the model-ab-testing work, Subscription Edition) or later. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again on the build it leaves you"
    stop "the installed fugaro has no 'ab' report: this check needs release ${RELEASE}"
  fi
  local rc=0
  version_at_least "$v" "$RELEASE" || rc=$?
  case "$rc" in
  0)
    fact release PASS "fugaro ${v} is release ${RELEASE} or later and 'fugaro ab --help' answers: this build carries the work the pair below is measured against"
    ;;
  1)
    fact release FAIL "fugaro ${v} predates release ${RELEASE}, which is the release the model-ab-testing work ships in: a pair measured here would be a reading of a build nobody shipped this for. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again"
    stop "the installed fugaro is ${v}, older than ${RELEASE}"
    ;;
  *)
    fact release PASS "fugaro reports '${v}', which is not a release number (a dev build or a commit): the feature probe decides, and 'fugaro ab --help' answers, so this build carries the work"
    ;;
  esac
}

# step_bound: what this check is about to start, said before it starts it. This
# is the one live check in this directory whose cost is the feature: a pair
# cannot be forced, because the launcher samples by a hash of the run id it
# mints, so the only way to a pair is to keep launching.
step_bound() {
  fact bound PASS "BILLABLE, which is why --yes is required. This check launches sandbox runs until one is paired: at the layer's ab.rate the expected number is about 1/rate (ten at the 0.10 ceiling), and it stops at ${MAX_LAUNCHES} (CHECK212_MAX_LAUNCHES). Every launch that pairs nothing is an ordinary champion run of the sandbox repository — its own pull request, its own money — and the pair adds one shadow run plus the judge run's calls. Nothing this check is still following outlives it: a run that has not ended within ${RUN_TIMEOUT}s is cancelled with 'fugaro cancel --now', from the step that gives up on it and from this script's exit trap. The launches that paired nothing are NOT cancelled — their pull requests are yours to close"
}

# ---------------------------------------------------------------- the steps
# step_sandbox: the premise. The project comes from the checkout itself
# (`fugaro doctor --json`), so the check reads the project you actually work in
# rather than one it guessed, and the pair's records are that project's.
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
  PROJECT=$(python3 -c 'import json,sys; print(((json.load(open(sys.argv[1])) or {}).get("project") or {}).get("gcp_project") or "")' "$SCRATCH/doctor.out" 2>/dev/null || true)
  if [ -z "$PROJECT" ]; then
    unmeasured sandbox "fugaro doctor --json answered without a project block: this check cannot say which project the sandbox checkout belongs to, and the rows below are about the project and the runs bucket. What it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  case "$SANDBOX$PROJECT" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${PROJECT}: this check is never run there" ;;
  esac
  # The repository as owner/name, which is what `fugaro runs ls`, `fugaro ab` and
  # the model table's data terms are all asked about: the checkout's own origin is
  # the one place it is stated before a launch has answered, and the launch's own
  # repo below replaces it with what the launcher really used.
  LAUNCH_REPO=$(git -C "$SANDBOX" remote get-url origin 2>/dev/null | sed -e 's#\.git$##' -e 's#^.*[:/]\([^/]*/[^/]*\)$#\1#' || true)
  case "$LAUNCH_REPO" in
  */*) ;;
  *)
    unmeasured sandbox "the checkout at ${SANDBOX} names no repository: its origin reads '${LAUNCH_REPO:-none}', and this check asks the runs listing, the report and the model table's own data terms about a repository as owner/name. Point the checkout at the sandbox repository's origin and run this check again"
    return 0
    ;;
  esac
  fact sandbox PASS "${SANDBOX} is a Fugaro repository's checkout of ${LAUNCH_REPO}, and it belongs to ${PROJECT}: the check launches from this checkout and reads the pair's records in this project's bucket"
}

# step_layer: the `ab:` block the sampler acts on, read through
# `fugaro config layer --json` in the sandbox checkout — the layer the runs
# below actually read, rather than the file you think you published.
step_layer() {
  if [ -z "$PROJECT" ]; then
    unmeasured layer "there is no project to read a layer for: the row above says why"
    return 0
  fi
  say "reading the project layer: fugaro config layer --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" layer "$SANDBOX" "$FUGARO_BIN" config layer --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured layer "fugaro config layer --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): this check cannot read the layer the runs below sample against, and a project that has published none samples nothing. What it printed is in $SCRATCH/layer.out and $SCRATCH/layer.err"
    return 0
  fi
  if ! read_json layer "$SCRATCH/layer.out" "$SCRATCH/layer.txt"; then
    unmeasured layer "the layer fugaro config layer --json printed is not a document this check could read: what it printed is in $SCRATCH/layer.out"
    return 0
  fi
  if [ "$(field "$SCRATCH/layer.txt" present)" != true ]; then
    fact layer FAIL "the project layer ${PROJECT} reads carries no ab: block (generation $(field "$SCRATCH/layer.txt" generation), sha256 $(field "$SCRATCH/layer.txt" sha256)): with no block the sampler is off and no run of this project is ever paired, so there is no pair here to measure. Publish the block the how-to writes — ab.rate (up to ${AB_MAX_RATE}), ab.challengers with a provider model first, and ab.judge_model — with 'fugaro config publish', then run this check again. What the layer holds is in $SCRATCH/layer.out"
    return 0
  fi
  RATE=$(field "$SCRATCH/layer.txt" rate)
  local list judge
  list=$(field "$SCRATCH/layer.txt" challengers)
  judge=$(field "$SCRATCH/layer.txt" judge_model)
  if [ -z "$RATE" ]; then
    fact layer FAIL "the layer's ab: block writes no ab.rate: a rate the block does not carry is zero, so the sampler is off and no run is ever paired. Set ab.rate (up to ${AB_MAX_RATE}) in the project layer, publish it, and run this check again. What the layer holds is in $SCRATCH/layer.out"
    return 0
  fi
  if awk -v r="$RATE" 'BEGIN { exit !(r + 0 <= 0) }'; then
    fact layer FAIL "the layer's ab.rate is ${RATE}: a rate of zero is the project's opt-out, so the sampler pairs nothing and there is no pair here to measure. Set ab.rate above zero (up to ${AB_MAX_RATE}) in the project layer, publish it, and run this check again"
    return 0
  fi
  if awk -v r="$RATE" -v max="$AB_MAX_RATE" 'BEGIN { exit !(r + 0 > max + 0) }'; then
    fact layer FAIL "the layer's ab.rate is ${RATE}, above the ${AB_MAX_RATE} ceiling a project layer may set (config.ABMaxRate): a rate the CLI would refuse to publish is not one this check can measure against. Lower ab.rate to at most ${AB_MAX_RATE}, publish the layer, and run this check again"
    return 0
  fi
  if [ -z "$list" ]; then
    fact layer FAIL "the layer's ab: block names no ab.challengers: the list is what the launcher picks a challenger from, and an empty one samples nothing. Put a provider model the project routes for this repository at the head of ab.challengers, publish the layer, and run this check again"
    return 0
  fi
  if [ -z "$judge" ]; then
    fact layer FAIL "the layer's ab: block names no ab.judge_model: a judge run with no model to call is refused at its launch, so the pair below would never be judged. Set ab.judge_model to a model that may judge this repository's pairs, publish the layer, and run this check again"
    return 0
  fi
  JUDGE_MODEL=$judge
  LAYER_OK=yes
  fact layer PASS "the layer ${PROJECT} reads samples ${RATE} of eligible runs (at most $(field "$SCRATCH/layer.txt" max_pairs_per_day) pairs a day, or the shipped default when that key is unset), allows the challengers [${list}] in priority order and judges with ${JUDGE_MODEL} (generation $(field "$SCRATCH/layer.txt" generation), sha256 $(field "$SCRATCH/layer.txt" sha256)): this is the block the runs below sample against"
}

# step_challenger: the model this check is about to require of the pair. The
# launcher takes the FIRST challenger that admits, so that is what the check
# grades unless --challenger names another — and it must be a provider model:
# not a Claude ID and not served by the direct Anthropic route, whose data
# terms are the project's own. A pair whose arms are one model measures nothing,
# and a model the route does not admit to this repository is refused at
# launch.
step_challenger() {
  if [ "$LAYER_OK" != yes ]; then
    unmeasured challenger "there is no layer to take a challenger from: the row above says why"
    return 0
  fi
  local list
  list=$(field "$SCRATCH/layer.txt" challengers)
  local wanted="$CHALLENGER_CLI"
  if [ -n "$wanted" ]; then
    case " $list " in
    *" $wanted "*) ;;
    *)
      fact challenger FAIL "--challenger names ${wanted}, and the layer's ab.challengers is [${list}]: the launcher only ever picks a model the owner's own list carries (task.AB.ChallengerSource is owner), so a pair measured for another model is not one this layer can produce. Either publish a layer whose list carries ${wanted}, or drop --challenger and let the check grade the list's head"
      return 0
      ;;
    esac
  else
    wanted=${list%% *}
  fi
  say "reading the project's models: fugaro models ls --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" models "$SANDBOX" "$FUGARO_BIN" models ls --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured challenger "fugaro models ls --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): this check cannot say whether ${wanted} is a provider model routed for this repository, and every row below is about that pair. What it printed is in $SCRATCH/models.out and $SCRATCH/models.err"
    return 0
  fi
  if ! read_json models "$SCRATCH/models.out" "$SCRATCH/models.txt" "$LAUNCH_REPO"; then
    unmeasured challenger "the model table fugaro models ls --json printed is not a document this check could read: what it printed is in $SCRATCH/models.out"
    return 0
  fi
  local row route family refused
  row=$(awk -F'\t' -v id="$wanted" '$1 == "model" && $2 == id { print; exit }' "$SCRATCH/models.txt")
  if [ -z "$row" ]; then
    fact challenger FAIL "the layer's challenger ${wanted} is not a model this project knows: 'fugaro models ls --json' carries no row for it, so a run pinned to it has no price and no route, and the launcher's admission cannot pass it. Check the id against 'fugaro models ls', fix ab.challengers, and run this check again"
    return 0
  fi
  route=$(printf '%s' "$row" | cut -f3)
  refused=$(printf '%s' "$row" | cut -f10)
  family=${wanted%%/*}
  case "$wanted" in
  */*) ;;
  *) family=anthropic ;;
  esac
  if [ "$family" = anthropic ]; then
    fact challenger FAIL "the layer's challenger ${wanted} is a Claude ID: the plan's Check 212 measures a pair whose challenger is a provider model routed for the sandbox repository, and a Claude ID goes down the direct Anthropic route — the project's own credential and data terms — which is the champion's route and not the one this check exists to read. Put a provider model at the head of ab.challengers, publish the layer, and run this check again"
    return 0
  fi
  if [ "$route" = direct ]; then
    fact challenger FAIL "the layer's challenger ${wanted} is served by the direct Anthropic route in this project (fugaro models ls --json says route=${route}), so a run pinned to it is not a provider route at all — which is the one thing this check measures. Route ${wanted} through its provider entry in the project config, publish the layer, and run this check again"
    return 0
  fi
  if [ -n "$refused" ]; then
    fact challenger FAIL "the layer's challenger ${wanted} has no price this project can use (fugaro models ls --json refuses it: ${refused}): the launcher's admission needs a priced model, and an unpriced provider route is one the gateway cannot charge or cap. Price it, publish the layer, and run this check again"
    return 0
  fi
  if [ "$(printf '%s' "$row" | cut -f9)" != true ]; then
    fact challenger FAIL "the layer's challenger ${wanted} is served by route ${route}, and that route does not admit this repository (allow_data_to does not name it and it is not open to every repository): the launcher refuses a challenger whose route may not carry this repository's code, so no pair can be sampled for it as things stand. Add this repository to the route's allow_data_to, publish the layer, and run this check again"
    return 0
  fi
  local coder
  coder=$(awk -F'\t' -v name="$LAUNCH_REPO" '$1 == "repo" && $2 == name { print $4; exit }' "$SCRATCH/models.txt")
  if [ -n "$coder" ] && [ "$coder" = "$wanted" ]; then
    fact challenger FAIL "the layer's challenger ${wanted} is also this repository's coder pin (fugaro models ls --json says so): both arms of such a pair would run the same model, and a pair of one model measures nothing. Pin another coder for the repository or name another challenger, publish the layer, and run this check again"
    return 0
  fi
  CHALLENGER_MODEL=$wanted
  MODELS_OK=yes
  # The pin note is built here rather than inline: a possessive apostrophe
  # inside a `${x:+...}` expansion makes bash look for a quote that never
  # closes.
  local pin_note=""
  [ -n "$coder" ] && pin_note=" and it is not the coder this repository is pinned to (${coder})"
  fact challenger PASS "${CHALLENGER_MODEL} is a provider model (route ${route}, kind $(awk -F'\t' -v name="$route" '$1 == "route" && $2 == name { print $3; exit }' "$SCRATCH/models.txt")), that route admits this repository, it is priced at \$$(printf '%s' "$row" | cut -f5)/M in and \$$(printf '%s' "$row" | cut -f6)/M out${pin_note}: this is the challenger the pair below must carry"
}

# step_launch: the pair. The launcher samples by a hash of the run id the CLI
# mints, so no argument can force a roll — a caller-chosen --run-id, a --retry
# and an --attempts above one are all ineligible — and the only way to a pair is
# to launch. Each launch that pairs nothing is an ordinary champion run, which
# is what the cost row above says out loud.
step_launch() {
  if [ "$LAYER_OK" != yes ] || [ "$MODELS_OK" != yes ]; then
    unmeasured pair-launched "there is no layer and challenger to sample a pair from: the rows above say why"
    return 0
  fi
  local attempts=0 id status model reason
  say "at ab.rate ${RATE} the expected number of launches to pair one is about $(awk -v r="$RATE" 'BEGIN { printf "%.0f", 1 / r }'), and this check stops at ${MAX_LAUNCHES}"
  while [ "$attempts" -lt "$MAX_LAUNCHES" ]; do
    attempts=$((attempts + 1))
    say "launching a sandbox run (attempt ${attempts} of at most ${MAX_LAUNCHES}): fugaro run --repo ${LAUNCH_REPO} --json"
    run_bounded "$LAUNCH_TIMEOUT" launch "$SANDBOX" "$FUGARO_BIN" run --repo "$SANDBOX" --json "$TASK"
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
      fact pair-launched FAIL "fugaro run did not answer for attempt ${attempts} (exit ${BOUNDED_STATUS}): a launch that never answered is one this check cannot read a pair out of. What it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
      return 0
    fi
    if ! read_json launch "$SCRATCH/launch.out" "$SCRATCH/launch.txt"; then
      fact pair-launched FAIL "the document fugaro run printed for attempt ${attempts} is not one this check could read: what it printed is in $SCRATCH/launch.out"
      return 0
    fi
    id=$(field "$SCRATCH/launch.txt" run_id)
    status=$(field "$SCRATCH/launch.txt" status)
    PAIR_ID=$(field "$SCRATCH/launch.txt" pair_id)
    PAIR_BATCH=$(field "$SCRATCH/launch.txt" pair_batch)
    model=$(field "$SCRATCH/launch.txt" pair_challenger)
    reason=$(field "$SCRATCH/launch.txt" pair_reason)
    if [ -z "$id" ]; then
      fact pair-launched FAIL "the launch of attempt ${attempts} named no run_id (status ${status:-none}): there is no run to follow and no record to read. What it printed is in $SCRATCH/launch.out"
      return 0
    fi
    LAUNCHES=$((LAUNCHES + 1))
    # The launcher's own answer for the repository it launched on, which is what
    # every later read (`runs ls --repo`, `fugaro ab --repo`) has to be asked
    # about: the checkout's origin was the check's own reading and this is the
    # one the run really went to.
    if [ -n "$(field "$SCRATCH/launch.txt" repo)" ]; then LAUNCH_REPO=$(field "$SCRATCH/launch.txt" repo); fi
    if [ -n "$reason" ]; then
      fact pair-launched FAIL "the run ${id} sampled no pair and the launcher said why: '${reason}'. A companion line on its stderr carries the same word. Nothing below can be read from a launch that paired nothing, and the sandbox run it did start is an ordinary champion run. Settle what the reason names, then run this check again"
      return 0
    fi
    if [ -n "$PAIR_ID" ]; then
      if [ -z "$model" ]; then
        fact pair-launched FAIL "the run ${id} carries the pair id ${PAIR_ID} and no challenger: the champion arm went and the shadow beside it did not, so this pair is half-launched and no verdict can exist for it. The launcher's own line on stderr says why the second arm did not go (a task that could not be built, a store that refused, a run id that had already launched). Read the run with 'fugaro runs show ${id}' and run this check again"
        return 0
      fi
      CHAMPION_ID=$id
      CHALLENGER_ID=$model
      PAIRED=yes
      follow "$CHAMPION_ID"
      follow "$CHALLENGER_ID"
      fact pair-launched PASS "after ${LAUNCHES} launch(es) at ab.rate ${RATE}, ${CHAMPION_ID} was paired with the shadow ${CHALLENGER_ID} (pair ${PAIR_ID}, batch ${PAIR_BATCH:-none}, read from fugaro run --json): the pair exists, and the rows below follow these two runs and no others"
      return 0
    fi
    say "attempt ${attempts} paired nothing (status ${status}), which is what a rate below one means: it is an ordinary champion run and its pull request is yours to close"
  done
  fact pair-launched FAIL "the sampler paired none of the ${MAX_LAUNCHES} sandbox runs this check launched (ab.rate ${RATE}; the chance of ${MAX_LAUNCHES} rolls all missing at that rate is about $(awk -v r="$RATE" -v n="$MAX_LAUNCHES" 'BEGIN { printf "%.1f%%", 100 * (1 - r) ^ n }')): every one of them is an ordinary champion run of the sandbox repository, with its own pull request for you to close, and there is no pair here to measure. Raise ab.rate (up to ${AB_MAX_RATE}) or CHECK212_MAX_LAUNCHES, and run this check again"
}

# step_terminal: both arms have to end. The champion does the whole task; the
# shadow runs the same task on the challenger model and publishes nothing, so
# the two end about together — and a pair whose arms are not both terminal has
# no records to read and nothing for a judge to take.
step_terminal() {
  if [ "$PAIRED" != yes ]; then
    unmeasured arms-terminal "there is no pair to follow: the row above says why"
    return 0
  fi
  say "waiting for both arms to end (up to ${RUN_TIMEOUT}s; fugaro runs ls --batch ${PAIR_BATCH} --json)"
  local waited=0 champ_end="" chal_end="" champ_status="" chal_status="" row=""
  while [ "$waited" -lt "$RUN_TIMEOUT" ]; do
    run_bounded "$CLI_TIMEOUT" rows "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$LAUNCH_REPO" --batch "$PAIR_BATCH" --json --limit 50
    champ_end=""
    chal_end=""
    if [ "$BOUNDED_STATUS" = 0 ] && read_json rows "$SCRATCH/rows.out" "$SCRATCH/rows.txt"; then
      champ_status=$(row_field "$SCRATCH/rows.txt" "$CHAMPION_ID" 3)
      champ_end=$(row_field "$SCRATCH/rows.txt" "$CHAMPION_ID" 4)
      chal_status=$(row_field "$SCRATCH/rows.txt" "$CHALLENGER_ID" 3)
      chal_end=$(row_field "$SCRATCH/rows.txt" "$CHALLENGER_ID" 4)
      row=$(row_field "$SCRATCH/rows.txt" "$CHAMPION_ID" 6)
      [ -n "$row" ] && SLUG=${row%%/*}
      [ "$champ_end" = true ] && [ "$chal_end" = true ] && break
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$champ_end" != true ] || [ "$chal_end" != true ]; then
    cancel_run "an arm had not ended after ${RUN_TIMEOUT}s"
    unmeasured arms-terminal "after ${RUN_TIMEOUT}s the champion ${CHAMPION_ID} was ${champ_end:-unknown}-ended (status ${champ_status:-unknown}) and the shadow ${CHALLENGER_ID} was ${chal_end:-unknown}-ended (status ${chal_status:-unknown}): a pair whose arms have not both ended has no records and no verdict, so there is nothing here to measure — and the arms were cancelled rather than left behind. Read them with 'fugaro runs show <run>' before running this check again"
    return 0
  fi
  settled "$CHAMPION_ID"
  settled "$CHALLENGER_ID"
  fact arms-terminal PASS "both arms ended in ${waited}s (champion ${CHAMPION_ID}: status ${champ_status:-unknown}; shadow ${CHALLENGER_ID}: status ${chal_status:-unknown}, read from fugaro runs ls --json --batch ${PAIR_BATCH}): the pair is complete, which is what a judge run takes and what the report counts"
}

# step_records: the two records, read from the runs bucket as you would read
# them. The ab block on each is what makes the two one pair: the pair id both
# carry, the arm each is, and the shadow's own two facts — it publishes nothing
# and it runs the challenger model the owner's layer named.
step_records() {
  if [ "$PAIRED" != yes ] || [ -z "$SLUG" ]; then
    unmeasured records "there is no pair whose records could be read: the rows above say why"
    return 0
  fi
  local arm id key
  for arm in champion challenger; do
    if [ "$arm" = champion ]; then id=$CHAMPION_ID; else id=$CHALLENGER_ID; fi
    key="runs/${SLUG}/${id}/result.json"
    say "reading the ${arm}'s record: gcloud storage cat gs://fugaro-runs-${PROJECT}/${key}"
    run_bounded "$CLI_TIMEOUT" "record-${arm}" "$SANDBOX" gcloud storage cat "gs://fugaro-runs-${PROJECT}/${key}" --project="${PROJECT}"
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
      fact records FAIL "gs://fugaro-runs-${PROJECT}/${key} could not be read (exit ${BOUNDED_STATUS}): a run that ended has a record, and the record is where its ab block, its wall time and its cost are written. What gcloud printed is in $SCRATCH/record-${arm}.err"
      return 0
    fi
    if ! read_json record "$SCRATCH/record-${arm}.out" "$SCRATCH/record-${arm}.txt"; then
      fact records FAIL "${key} is not a document this check could read: what gcloud printed is in $SCRATCH/record-${arm}.out"
      return 0
    fi
    if [ "$(field "$SCRATCH/record-${arm}.txt" run_id)" != "$id" ]; then
      fact records FAIL "${key} records run_id=$(field "$SCRATCH/record-${arm}.txt" run_id) and this check read the ${arm}'s record as ${id}: nothing below can be read as this pair's"
      return 0
    fi
  done
  local cpair apair carm aarm challenger publish
  cpair=$(field "$SCRATCH/record-champion.txt" ab_pair_id)
  apair=$(field "$SCRATCH/record-challenger.txt" ab_pair_id)
  carm=$(field "$SCRATCH/record-champion.txt" ab_arm)
  aarm=$(field "$SCRATCH/record-challenger.txt" ab_arm)
  challenger=$(field "$SCRATCH/record-challenger.txt" ab_challenger)
  publish=$(field "$SCRATCH/record-challenger.txt" ab_publish)
  if [ "$cpair" != "$PAIR_ID" ] || [ "$apair" != "$PAIR_ID" ]; then
    fact records FAIL "the two records carry the pair ids '${cpair:-none}' and '${apair:-none}' where the launch named the pair ${PAIR_ID}: a judge joins the two arms by that id and the report counts them by it, so a pair whose records disagree is one neither can read. The records are in $SCRATCH/record-champion.out and $SCRATCH/record-challenger.out"
    return 0
  fi
  if [ "$carm" != champion ] || [ "$aarm" != challenger ]; then
    fact records FAIL "the records carry the arms '${carm:-none}' and '${aarm:-none}' where the pair's champion (${CHAMPION_ID}) and its shadow (${CHALLENGER_ID}) must be champion and challenger: an arm read the other way round turns the shadow into the run the developer sees and inverts every figure the report reads"
    return 0
  fi
  if [ "$publish" != none ]; then
    fact records FAIL "the shadow's record carries publish '${publish:-none}' where a challenger publishes nothing (task.ABPublishNone is the only value the field takes): a shadow that opened a pull request is a change the developer sees twice, which is the one thing D554 forbids the second arm"
    return 0
  fi
  if [ "$challenger" != "$CHALLENGER_MODEL" ]; then
    fact records FAIL "the shadow's record names the challenger model '${challenger:-none}' where the layer's list led with ${CHALLENGER_MODEL}: the launcher takes the first challenger that admits, so the pair that ran is a reading of another model. Either put ${CHALLENGER_MODEL} first in ab.challengers, or pass --challenger ${challenger} to grade the model this pair really carried"
    return 0
  fi
  local pr
  pr=$(field "$SCRATCH/record-champion.txt" pr_url)
  fact records PASS "both records carry the pair ${PAIR_ID} — the champion ${CHAMPION_ID} as the arm the developer sees (publishing as it always did${pr:+, at $pr}) and the shadow ${CHALLENGER_ID} as the challenger, with publish none and ${challenger} named on it (challenger_source $(field "$SCRATCH/record-challenger.txt" ab_challenger_source)) — read from the bucket as you would read them"
}

# step_wall_times: U4's own reading. The two arms run the same task at the same
# time on two routes, and a shadow that took many times the champion's wall time
# is the profile difference U4 asks an owner to see — the provider's shared rate
# limits, or a route whose calls are slower, showing up as a shadow nobody could
# compare at a glance. Both figures are recorded whatever they say; the ratio is
# this check's own bound and the knob names it.
step_wall_times() {
  if [ ! -s "$SCRATCH/record-champion.txt" ] || [ ! -s "$SCRATCH/record-challenger.txt" ]; then
    unmeasured wall-times "there are no records to read the wall times from: the row above says why"
    return 0
  fi
  local champ shadow
  champ=$(field "$SCRATCH/record-champion.txt" wall_s)
  shadow=$(field "$SCRATCH/record-challenger.txt" wall_s)
  if [ -z "$champ" ] || [ -z "$shadow" ]; then
    fact wall-times FAIL "one of the two records carries no wall time (champion '${champ:-none}', shadow '${shadow:-none}' from started_at and finished_at): both arms ended, so both records must carry the two timestamps U4 is measured from. The records are in $SCRATCH/record-champion.out and $SCRATCH/record-challenger.out"
    return 0
  fi
  WALL_CHAMPION=$champ
  WALL_CHALLENGER=$shadow
  fact wall-times PASS "the champion ${CHAMPION_ID} ran ${champ}s ($(field "$SCRATCH/record-champion.txt" started) to $(field "$SCRATCH/record-champion.txt" finished)) and the shadow ${CHALLENGER_ID} ran ${shadow}s ($(field "$SCRATCH/record-challenger.txt" started) to $(field "$SCRATCH/record-challenger.txt" finished)): both wall times are recorded, which is U4's own reading of a shadow on a provider route"
}

# step_latency: the two wall times against each other. U4's premise is that a
# shadow on a provider route shares the provider's rate limits, so its profile
# can differ; the profile row is what says by how much, in the one figure an
# owner can look at.
step_latency() {
  if [ -z "${WALL_CHAMPION:-}" ] || [ -z "${WALL_CHALLENGER:-}" ]; then
    unmeasured latency-profile "there are no two wall times to compare: the row above says why"
    return 0
  fi
  awk -v a="$WALL_CHAMPION" -v b="$WALL_CHALLENGER" -v max="$LATENCY_MAX_RATIO" 'BEGIN {
    exit !(a + 0 > 0 && b + 0 > 0)
  }' || {
    fact latency-profile FAIL "one of the two wall times is zero (champion ${WALL_CHAMPION}s, shadow ${WALL_CHALLENGER}s), and a ratio of two times needs both: a run recorded with no duration is one whose timestamps cannot be read as a profile"
    return 0
  }
  local ratio
  ratio=$(awk -v a="$WALL_CHAMPION" -v b="$WALL_CHALLENGER" 'BEGIN { printf "%.2f", (a > b ? a / b : b / a) }')
  if awk -v r="$ratio" -v max="$LATENCY_MAX_RATIO" 'BEGIN { exit !(r + 0 > max + 0) }'; then
    fact latency-profile FAIL "the two arms' wall times differ by a factor of ${ratio} (champion ${WALL_CHAMPION}s, shadow ${WALL_CHALLENGER}s), above the ${LATENCY_MAX_RATIO}x this check allows (CHECK212_LATENCY_MAX_RATIO): U4 reads a shadow on a provider route as one that shares the provider's rate limits, so a shadow this much slower than the champion beside it is a pair whose figures an owner cannot compare at a glance. Read the two runs' own timelines before raising the knob"
    return 0
  fi
  fact latency-profile PASS "the two arms' wall times differ by a factor of ${ratio} (champion ${WALL_CHAMPION}s, shadow ${WALL_CHALLENGER}s), inside the ${LATENCY_MAX_RATIO}x this check allows: the shadow on the provider route ran the same task in the same order of time as the champion, which is the profile U4 records the two figures for"
}

# step_costs: both arms against the cap their own record carries. The plan
# reads the pair's dollars against the run's per-run cap, and the two arms run
# one task under one policy: a pair whose arms recorded two different caps is
# one the report's cost ratio cannot be read across.
step_costs() {
  if [ ! -s "$SCRATCH/record-champion.txt" ] || [ ! -s "$SCRATCH/record-challenger.txt" ]; then
    unmeasured costs "there are no records to read the costs from: the row above says why"
    return 0
  fi
  local cap arm usd armcap
  cap=$(field "$SCRATCH/record-champion.txt" per_run_usd)
  for arm in champion challenger; do
    armcap=$(field "$SCRATCH/record-${arm}.txt" per_run_usd)
    if [ "$armcap" != "$cap" ]; then
      fact costs FAIL "the two arms recorded different per-run caps (champion \$${cap:-none}, ${arm} \$${armcap:-none}, from each record's policy.effective.per_run_usd): both arms of a pair run one task under one policy, so a pair whose records disagree about the cap is one this check cannot measure either cost against"
      return 0
    fi
  done
  if [ -z "$cap" ] || awk -v c="$cap" 'BEGIN { exit !(c + 0 <= 0) }'; then
    fact costs FAIL "the pair's records carry no per-run cap (policy.effective.per_run_usd is ${cap:-0}): 'within the per-run cap' is the criterion this row reads, and a run with no cap has none to be within — nor does the provider route the challenger went down have one, which is the config's own rule for a provider model (budget.mode: enforce with budget.per_run_usd). Set the cap in the project layer, publish it, and run this check again"
    return 0
  fi
  for arm in champion challenger; do
    usd=$(field "$SCRATCH/record-${arm}.txt" cost_usd)
    if awk -v u="${usd:-0}" -v c="$cap" 'BEGIN { exit !(u + 0 > c + 0) }'; then
      fact costs FAIL "the ${arm}'s model spend is \$${usd} and the run's own per-run cap is \$${cap} (policy.effective.per_run_usd): a run that spent past its cap is one the budget did not hold, and every figure the report reads from these two records is read across a cap that did not bind. The records are in $SCRATCH/record-champion.out and $SCRATCH/record-challenger.out"
      return 0
    fi
  done
  fact costs PASS "both arms are inside the \$${cap} per-run cap their own records carry (champion \$$(field "$SCRATCH/record-champion.txt" cost_usd) on $(field "$SCRATCH/record-champion.txt" budget_mode) mode, shadow \$$(field "$SCRATCH/record-challenger.txt" cost_usd)): the pair cost what the policy allowed, which is the reading the plan names beside the two wall times"
}

# step_challenger_ran: the shadow has to have really run the provider model. The
# record names it in its ab block whether or not a call was made (the arm is
# what the task was built for), so the reading that says a provider's calls were
# served is the cost split: a model named in cost.model_by, with dollars on it.
step_challenger_ran() {
  if [ ! -s "$SCRATCH/record-challenger.txt" ]; then
    unmeasured challenger-ran "there is no shadow record to read the challenger's calls from: the row above says why"
    return 0
  fi
  local usd split
  usd=$(awk -F'\t' -v id="$CHALLENGER_MODEL" '$1 == "model" && $2 == id { print $3; exit }' "$SCRATCH/record-challenger.txt")
  split=$(awk -F'\t' '$1 == "model" { printf "%s=\$%s ", $2, $3 }' "$SCRATCH/record-challenger.txt" 2>/dev/null || true)
  if [ -z "$usd" ]; then
    fact challenger-ran FAIL "the shadow's record carries no model spend for ${CHALLENGER_MODEL} (cost.model_by holds ${split:-nothing}), though it names that model as its challenger: the arm was built for the provider model and no call on it was served, so the pair the judge reads is not a reading of that model. The record is in $SCRATCH/record-challenger.out"
    return 0
  fi
  if awk -v u="$usd" 'BEGIN { exit !(u + 0 <= 0) }'; then
    fact challenger-ran FAIL "the shadow's record carries \$${usd} for ${CHALLENGER_MODEL} in cost.model_by (the whole split: ${split:-nothing}): a model row at zero dollars is an arm that made no call on the challenger, so nothing here measures the provider route. The record is in $SCRATCH/record-challenger.out"
    return 0
  fi
  fact challenger-ran PASS "the shadow's cost split names ${CHALLENGER_MODEL} at \$${usd} (cost.model_by: ${split}), and its ab block names it as the challenger from the owner's own list (source $(field "$SCRATCH/record-challenger.txt" ab_challenger_source)): the provider model really served the arm, which is what makes this pair a reading of a provider route"
}

# step_judge_run: the judge, launched the way the how-to says — one judge run
# per repository at a time, so a launch while one has not finished is refused,
# and the refusal's own sentence is reported rather than swallowed.
step_judge_run() {
  if [ "$PAIRED" != yes ]; then
    unmeasured judge-run "there is no pair for a judge run to judge: the row above says why"
    return 0
  fi
  say "launching the judge run: fugaro run --repo ${LAUNCH_REPO} --recipe ab-judge --json"
  run_bounded "$LAUNCH_TIMEOUT" judge "$SANDBOX" "$FUGARO_BIN" run --repo "$SANDBOX" --recipe ab-judge --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured judge-run "the judge run was not launched (exit ${BOUNDED_STATUS}): a repository has one judge run at a time, and the command's own sentence — in $SCRATCH/judge.err — says which. A judge run still going, or one whose launch the coordinator holds, is a state to settle rather than a finding about the release. Run this check again once nothing is judging this repository"
    return 0
  fi
  if ! read_json launch "$SCRATCH/judge.out" "$SCRATCH/judge.txt"; then
    unmeasured judge-run "the document the judge launch printed is not one this check could read: what it printed is in $SCRATCH/judge.out"
    return 0
  fi
  JUDGE_ID=$(field "$SCRATCH/judge.txt" run_id)
  if [ -z "$JUDGE_ID" ]; then
    unmeasured judge-run "the judge launch named no run_id (status $(field "$SCRATCH/judge.txt" status)): there is no run to follow and no verdicts object to read. What it printed is in $SCRATCH/judge.out"
    return 0
  fi
  follow "$JUDGE_ID"
  say "waiting for the judge run ${JUDGE_ID} to end (up to ${JUDGE_TIMEOUT}s)"
  local waited=0 ended="" status="" recipe=""
  while [ "$waited" -lt "$JUDGE_TIMEOUT" ]; do
    run_bounded "$CLI_TIMEOUT" rows "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$LAUNCH_REPO" --json --limit 50
    ended=""
    if [ "$BOUNDED_STATUS" = 0 ] && read_json rows "$SCRATCH/rows.out" "$SCRATCH/rows.txt"; then
      status=$(row_field "$SCRATCH/rows.txt" "$JUDGE_ID" 3)
      ended=$(row_field "$SCRATCH/rows.txt" "$JUDGE_ID" 4)
      recipe=$(row_field "$SCRATCH/rows.txt" "$JUDGE_ID" 8)
      [ "$ended" = true ] && break
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$ended" != true ]; then
    cancel_run "the judge run had not ended after ${JUDGE_TIMEOUT}s"
    unmeasured judge-run "the judge run ${JUDGE_ID} had not ended after ${JUDGE_TIMEOUT}s (status ${status:-unknown}), so the verdicts object below is not there to read — and it was cancelled rather than left behind. Read it with 'fugaro runs show ${JUDGE_ID}' before running this check again"
    return 0
  fi
  settled "$JUDGE_ID"
  fact judge-run PASS "the judge run ${JUDGE_ID} ended in ${waited}s (status ${status:-unknown}, recipe ${recipe:-ab-judge}) and it is this repository's own run of 'fugaro run --recipe ab-judge': the pair below is judged by the recipe A5.2 ships, launched the way the how-to says"
}

# step_verdict: the verdict object, at the key A5.2 writes it under. The entry
# for THIS pair is the reading: a judge that judged another pair and skipped
# this one leaves the pair unjudged, which the report counts and this check may
# not read as a verdict.
step_verdict() {
  if [ -z "$JUDGE_ID" ] || [ -z "$SLUG" ]; then
    unmeasured verdict "there is no judge run whose verdicts object could be read: the rows above say why"
    return 0
  fi
  local key="runs/${SLUG}/${JUDGE_ID}/ab/verdicts.json"
  say "reading the verdicts object: gcloud storage cat gs://fugaro-runs-${PROJECT}/${key}"
  run_bounded "$CLI_TIMEOUT" verdicts "$SANDBOX" gcloud storage cat "gs://fugaro-runs-${PROJECT}/${key}" --project="${PROJECT}"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact verdict FAIL "gs://fugaro-runs-${PROJECT}/${key} could not be read (exit ${BOUNDED_STATUS}): the judge run wrote no verdicts object, or not under this key, so this pair has no verdict at all. The judge run's own record — 'fugaro runs show ${JUDGE_ID}' — says how it ended. What gcloud printed is in $SCRATCH/verdicts.err"
    return 0
  fi
  if ! read_json verdicts "$SCRATCH/verdicts.out" "$SCRATCH/verdicts.txt"; then
    fact verdict FAIL "${key} is not a document this check could read: what gcloud printed is in $SCRATCH/verdicts.out"
    return 0
  fi
  if [ "$(field "$SCRATCH/verdicts.txt" version)" != 1 ]; then
    fact verdict FAIL "${key} carries version $(field "$SCRATCH/verdicts.txt" version) and this check reads version 1: a reader that meets a version it does not know refuses the object rather than reading it as one, and so does this check"
    return 0
  fi
  local judge entry skipped passes winner
  judge=$(field "$SCRATCH/verdicts.txt" judge_model)
  entry=$(awk -F'\t' -v id="$PAIR_ID" '$1 == "pair" && $2 == id { print; exit }' "$SCRATCH/verdicts.txt")
  if [ -z "$entry" ]; then
    fact verdict FAIL "${key} holds $(field "$SCRATCH/verdicts.txt" count) pair(s) and none of them is ${PAIR_ID}: the launcher takes the pairs of the repository's window and caps one run's manifest, so a pair missing here is one this judge run did not reach. Run the judge again — 'fugaro run --repo ${LAUNCH_REPO} --recipe ab-judge' — and run this check again"
    return 0
  fi
  skipped=$(printf '%s' "$entry" | cut -f5)
  winner=$(printf '%s' "$entry" | cut -f3)
  passes=$(printf '%s' "$entry" | cut -f6)
  if [ "$judge" != "$JUDGE_MODEL" ]; then
    fact verdict FAIL "${key} was written by the judge model '${judge:-none}' where the layer names ${JUDGE_MODEL} (ab.judge_model): D553's rule is that the model a pair was judged by is recorded on the verdict, and a verdict from another model is one the layer's own choice did not make"
    return 0
  fi
  if [ -n "$skipped" ]; then
    fact verdict FAIL "the pair ${PAIR_ID} was SKIPPED by the judge run: '${skipped}'. A skipped pair decided nothing — the report counts it unjudged and it enters no figure — so this pair has no verdict to read. A diff that named an arm (identified), a patch cut short (truncated) and an object too large to send (too-large) are settled by what they name; a judge call that failed (call-failed) may be judged by the next judge run. The verdicts object is in $SCRATCH/verdicts.out"
    return 0
  fi
  if [ "$passes" != 2 ]; then
    fact verdict FAIL "the pair ${PAIR_ID} carries ${passes} pass(es) where the judge makes exactly two, one from each labelling of the arms (D552): a verdict read from fewer than two calls is one this check cannot read as a decided pair. The verdicts object is in $SCRATCH/verdicts.out"
    return 0
  fi
  case "$winner" in
  champion | challenger | tie) ;;
  *)
    fact verdict FAIL "the pair ${PAIR_ID} carries the winner '${winner:-none}', which is not one of the judge's three words (champion, challenger, tie): nothing here can be read as a decided pair. The verdicts object is in $SCRATCH/verdicts.out"
    return 0
    ;;
  esac
  local pass bad=""
  while IFS=$'\t' read -r tag id index _swap label arm; do
    [ "$tag" = pass ] || continue
    [ "$id" = "$PAIR_ID" ] || continue
    pass="${arm:-(${label:-none})}"
    [ "$pass" = "$winner" ] || bad="${bad:+$bad, }pass ${index} (label ${label:-none}, arm ${arm:-none})"
  done <"$SCRATCH/verdicts.txt"
  if [ -n "$bad" ]; then
    fact verdict FAIL "the pair ${PAIR_ID} carries the winner '${winner}' and its passes disagree with it: ${bad}. The two passes label the arms the other way round on purpose (D552), so a verdict read from two answers that do not meet is a coin toss counted as a decision, not a pair the judge decided. The verdicts object is in $SCRATCH/verdicts.out"
    return 0
  fi
  fact verdict PASS "${key} carries the pair ${PAIR_ID} judged by ${judge}: the winner is '${winner}', and both passes — one from each labelling of the arms — agree on it. The object holds $(field "$SCRATCH/verdicts.txt" count) pair(s) of this repository, which is what the report reads a pair's winner out of"
}

# step_ab: `fugaro ab`, the report the plan's last criterion names. The row for
# the two models must carry the pair, and with fewer decisive pairs than the
# minimum its own rule answers below-minimum — the figures the plan is about,
# read from the same object a person reads.
step_ab() {
  if [ -z "$LAUNCH_REPO" ]; then
    unmeasured ab-listed "there is no repository to report on: the rows above say why"
    return 0
  fi
  say "reading the report: fugaro ab --repo ${LAUNCH_REPO} --since 0 --json"
  run_bounded "$AB_TIMEOUT" ab "$SANDBOX" "$FUGARO_BIN" ab --repo "$LAUNCH_REPO" --since 0 --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured ab-listed "fugaro ab --repo ${LAUNCH_REPO} --since 0 --json did not answer (exit ${BOUNDED_STATUS}): the figures the plan's last criterion reads could not be read. What it printed is in $SCRATCH/ab.out and $SCRATCH/ab.err"
    return 0
  fi
  if ! read_json ab "$SCRATCH/ab.out" "$SCRATCH/ab.txt"; then
    unmeasured ab-listed "the report fugaro ab --json printed is not a document this check could read: what it printed is in $SCRATCH/ab.out"
    return 0
  fi
  local matches
  matches=$(awk -F'\t' -v model="$CHALLENGER_MODEL" '$1 == "row" && $5 == model && $6 + 0 >= 1 { print }' "$SCRATCH/ab.txt")
  if [ -z "$matches" ]; then
    fact ab-listed FAIL "fugaro ab lists $(field "$SCRATCH/ab.txt" count) row(s) and none of them is a pair of a champion against ${CHALLENGER_MODEL} with a pair in it: the pair this check just ran is not in the figures — its records and the verdict are what the report is built from, so either the pair R2 refused (the arms' start commit, mode, tier or recipe differ), the read did not reach the runs bucket, or the window left it out. What the report printed is in $SCRATCH/ab.out"
    return 0
  fi
  local row pairs wins losses ties decisive decision rule unjudged
  row=$(printf '%s\n' "$matches" | head -n 1)
  pairs=$(printf '%s' "$row" | cut -f6)
  wins=$(printf '%s' "$row" | cut -f7)
  losses=$(printf '%s' "$row" | cut -f8)
  ties=$(printf '%s' "$row" | cut -f9)
  decisive=$(printf '%s' "$row" | cut -f10)
  decision=$(printf '%s' "$row" | cut -f11)
  rule=$(printf '%s' "$row" | cut -f12)
  unjudged=$(printf '%s' "$row" | cut -f13)
  if [ "$((wins + losses))" != "$decisive" ]; then
    fact ab-listed FAIL "the row for ${CHALLENGER_MODEL} carries wins ${wins} and losses ${losses} with decisive ${decisive}: a row whose own figures disagree is one the recommendation above it was not read through, and every number a person takes from it is then wrong"
    return 0
  fi
  if [ "$decisive" -lt "$MIN_RUNS" ]; then
    if [ "$decision" != no-conclusion ] || [ "$rule" != below-minimum ]; then
      fact ab-listed FAIL "the row for ${CHALLENGER_MODEL} carries ${decisive} decisive pair(s) — under the minimum of ${MIN_RUNS} — and answers decision '${decision:-none}' by rule '${rule:-none}' where fewer than the minimum can only answer no-conclusion by below-minimum: a conclusion drawn from ${decisive} decisive pairs is a claim the sample cannot carry. What the report printed is in $SCRATCH/ab.out"
      return 0
    fi
    fact ab-listed PASS "fugaro ab lists the pair as below the minimum: the row for ${CHALLENGER_MODEL} carries ${pairs} pair(s) — ${wins} won, ${losses} lost, ${ties} tied, ${unjudged} unjudged — with ${decisive} decisive against the minimum of ${MIN_RUNS}, and its recommendation is '${decision}' by rule '${rule}', which is what the plan's last criterion reads"
    return 0
  fi
  fact ab-listed PASS "fugaro ab lists the pair, and this repository's window already holds ${decisive} decisive pairs of ${CHALLENGER_MODEL} — at or above the minimum of ${MIN_RUNS} — so the row advises ('${decision}' by rule '${rule}') rather than saying no conclusion: ${pairs} pair(s), ${wins} won, ${losses} lost, ${ties} tied, ${unjudged} unjudged. The plan's 'below the minimum' reading is not available on an installation whose window has already reached it, and the row this check just added is in the figures either way"
}

# restore: what this check changed, said rather than assumed. The runs it is
# still following are cancelled here — from the exit trap, so a Ctrl-C or a
# failed assertion cancels them as surely as the ordinary way out.
restore() {
  cancel_run "the check is ending"
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="nothing was created but runs: ${LAUNCHES} sandbox launch(es) — $([ "$PAIRED" = yes ] && echo "the pair ${PAIR_ID} (champion ${CHAMPION_ID}, shadow ${CHALLENGER_ID})" || echo "none of which the sampler paired")${JUDGE_ID:+, and the judge run ${JUDGE_ID}}. Every launch that paired nothing is an ordinary champion run of the sandbox repository whose pull request is yours to close, and so is the pair's champion. Nothing was cancelled: the check cancelled only the runs it was still following, and $([ "$CANCEL_DONE" = yes ] && echo "the cancelled row above names them" || echo "every run it followed had ended on its own"). The scratch directory held the commands' output"
  if [ "$CANCEL_OK" = no ]; then
    RESTORE_VERDICT=FAIL
    RESTORE_NOTE="a run this check was following did not end on its own and 'fugaro cancel --now' did not answer: it may still be going. Read it with 'fugaro runs show <run>' and cancel it yourself. Nothing else was created"
  fi
}

cleanup() {
  # restore runs BEFORE the facts are captured: it is what cancels a run that
  # had not ended, and the row that says so has to be in the block.
  restore
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

finish() {
  local status=$?
  trap - EXIT
  # INT, TERM and HUP are NOT reset here: cleanup is about to cancel runs and
  # remove a directory, and a second Ctrl-C during it would kill this shell half
  # done — leaving the runs it was about to cancel.
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
  say "  [offline] fugaro version and fugaro ab --help — the release ${RELEASE} gate: the build must carry the report"
  say "  [network, read] fugaro doctor --json in ${SANDBOX} — the project this checkout belongs to"
  say "  [network, read] fugaro config layer --json in ${SANDBOX} — the ab: block: the rate, the challenger list and the judge model"
  say "  [network, read] fugaro models ls --json in ${SANDBOX} — the challenger's route, its data terms and its price"
  say "  [network, BILLABLE] fugaro run --repo ${SANDBOX} --json — sandbox runs launched until one is paired: at ab.rate the expected count is about 1/rate and it stops at ${MAX_LAUNCHES}, and every launch that pairs nothing is an ordinary champion run"
  say "  [network, BILLABLE] the pair's two arms, followed with fugaro runs ls --batch <pair> --json until both are terminal"
  say "  [read] gcloud storage cat gs://fugaro-runs-<the project>/runs/<slug>/<run>/result.json — both records: the pair id, the arms, the two wall times and the costs against the per-run cap"
  say "  [network, BILLABLE] fugaro run --repo ${SANDBOX} --recipe ab-judge --json — one judge run, followed until it ends"
  say "  [read] gcloud storage cat gs://fugaro-runs-<the project>/runs/<slug>/<judge run>/ab/verdicts.json — the verdict object and this pair's entry in it"
  say "  [network, read] fugaro ab --repo <owner/name> --since 0 --json — the figures, and this pair in them"
  say "  [network] fugaro cancel <run> --now — only for the runs still being followed when the check gives up, and again from the exit trap: nothing this check is still following outlives it. The runs that paired nothing are left alone, with their pull requests"
  for key in release sandbox layer challenger pair-launched arms-terminal records \
    wall-times latency-profile costs challenger-ran judge-run verdict ab-listed; do
    fact "$key" INFO "planned: a real pair measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no doctor, no layer read, no launch, no judge run and no cancel"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version release xtrace agent-session belong bound sandbox
  layer challenger pair-launched arms-terminal records wall-times latency-profile
  costs challenger-ran judge-run verdict ab-listed restore scratch-removed)

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
CHALLENGER_CLI=""
TASK_CLI=""
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
  --challenger)
    [ $# -ge 2 ] || die "--challenger needs a model id, one the layer's ab.challengers carries"
    CHALLENGER_CLI=$2
    shift 2
    ;;
  --task)
    [ $# -ge 2 ] || die "--task needs the text the sandbox runs are launched with"
    TASK_CLI=$2
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

SANDBOX=${CHECK212_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 212 needs --sandbox: the checkout of a repository of the sandbox project. It is what the runs are launched from and what their configuration is read through. Usage: bash scripts/live-checks/check212.sh --sandbox <a sandbox repository checkout> --yes"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
CHALLENGER_CLI=${CHECK212_CHALLENGER:-${CHALLENGER_CLI:-}}
TASK=${CHECK212_TASK:-${TASK_CLI:-"Add one line to the top of README.md naming the date, in a section of its own, and change nothing else. Do not run the test suite."}}
YES=${CHECK212_YES:-${YES_CLI:-}}
RELEASE=${CHECK212_RELEASE:-0.24.0}
DRY_RUN=${CHECK212_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK212_KEEP_SCRATCH:-}
REGION=${CHECK212_REGION:-us-east5}
MAX_LAUNCHES=${CHECK212_MAX_LAUNCHES:-30}
RUN_TIMEOUT=${CHECK212_RUN_TIMEOUT_SECONDS:-3600}
JUDGE_TIMEOUT=${CHECK212_JUDGE_TIMEOUT_SECONDS:-2400}
POLL_SECONDS=${CHECK212_POLL_SECONDS:-15}
CLI_TIMEOUT=${CHECK212_CLI_TIMEOUT_SECONDS:-180}
LAUNCH_TIMEOUT=${CHECK212_LAUNCH_TIMEOUT_SECONDS:-600}
CANCEL_TIMEOUT=${CHECK212_CANCEL_TIMEOUT_SECONDS:-300}
DOCTOR_TIMEOUT=${CHECK212_DOCTOR_TIMEOUT_SECONDS:-300}
AB_TIMEOUT=${CHECK212_AB_TIMEOUT_SECONDS:-600}
LATENCY_MAX_RATIO=${CHECK212_LATENCY_MAX_RATIO:-3}
AB_MAX_RATE=0.10
MIN_RUNS=20
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
    die "Check 212 launches sandbox runs until the sampler pairs one — each unpaired launch is an ordinary champion run on the project's budget — and then a judge run, so it is billable and it needs your word. Type: bash scripts/live-checks/check212.sh --sandbox ${SANDBOX} --yes (or read the plan first with --dry-run)"
  fi
  step_sandbox
  step_layer
  step_challenger
  step_launch
  step_terminal
  step_records
  step_wall_times
  step_latency
  step_costs
  step_challenger_ran
  step_judge_run
  step_verdict
  step_ab
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
