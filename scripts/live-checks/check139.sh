#!/usr/bin/env bash
# Check 139 — what an agent ID distinguishes in a gateway run's log.
#
# Task T3.5 of the task-governance plan writes it and the owner runs it, and it
# answers the reconciliation's U4 — that `x-claude-code-agent-id` tells every
# sub-agent from the main session — over a run that ALREADY EXISTS. It launches
# nothing, spends nothing and creates nothing in the cloud: one listing, one
# `fugaro diagnose` read and one `gcloud logging read` over one run's own
# entries, every one of them a read.
#
# It GATES task T7.2: the plan makes that task's fan-out stop wait for this
# check's answer, so T7.2 does not merge until this check has run and its answer
# is on the record.
#
# What it reads, and why it is the log and not the record:
#
#   * the gateway's `model call` entries of ONE run's execution, in Cloud
#     Logging, with the filter the CLI's own log reader builds
#     (internal/backend/gcp/logs.go's executionFilter): the region, the job and
#     the execution name, and jsonPayload.message="model call". `fugaro logs`
#     does NOT carry the fields this check is about — it prints the time, the
#     severity, the stage and the message (internal/cli/logs.go's logLine) — so
#     the entries are read where they are. When the installation isolates its
#     logs the read is scoped to the project config's `log_view`
#     (internal/localcfg), which gcloud takes only as --resource-names: --view
#     is refused without --bucket and --location, and that name is the whole
#     resource rather than a view's leaf;
#   * the `agent_id` on each of them (internal/gateway/log.go): the main
#     session's calls carry an EMPTY one, each sub-agent a DISTINCT non-empty
#     one, and the background model's calls none. The gateway's stage report
#     counts those distinct ids (internal/gateway/ledger.go's
#     `r.Agents = len(st.agents)`), and T7.2's fan-out stop spends that count —
#     which is a sub-agent count only if those three readings hold;
#   * the record's own stages[].agents BESIDE it, named as a different figure
#     and never compared with it: that field is the relay's count of sub-agent
#     TOOL CALLS, and schemas/result.schema.json says in as many words that the
#     gateway's count of distinct agent IDs is a different figure and is never
#     recorded.
#
# With no --run the check FINDS the run itself: the newest settled run of the
# sandbox project whose budget was on (`model_basis: api-list` — vertex or
# api-key; an oauth run reads `subscription` and makes no gateway call at all)
# and whose entries carry at least one non-empty agent_id. A run that qualifies
# is read; a listing with none prints FAIL `no run to read` and names the one
# thing that makes one — a short gateway run that spawns a sub-agent. This check
# never launches anything.
#
# Usage:      bash scripts/live-checks/check139.sh
#             bash scripts/live-checks/check139.sh --run <run-id>
#             bash scripts/live-checks/check139.sh --run <run-id> --bg-model <name>
#             bash scripts/live-checks/check139.sh --dry-run   # print the plan
#             bash scripts/live-checks/check139.sh --help
# Needs:      bash, mktemp, python3 (the reader for the listing, the diagnosis
#             and the entries), gcloud (authenticated as an account that may
#             read Cloud Logging in the sandbox project), fugaro, and
#             awk/sed/grep/cut/tr/head.
# Where:      the SANDBOX installation ONLY: Fugaro project `fugaro` on GCP
#             project `fugaro-dev`, both hard-coded in this file (never
#             `gcloud config get-value project`). The account, the project, the
#             run and the log view the entries are read from are refused by name
#             when any of them names belong, EdgeWeb or EdgeServer — Check 130's
#             rule, applied to every name this check has, and judged before
#             anything is read. No other installation is accepted.
# Creates:    nothing: no job, no run, no build, no object, no binding, no
#             account and no policy.
# Costs:      nothing but wall clock. No model call, no run, no dollar.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check139_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, and nothing else — the check has nothing to
#             delete, and its restore row says so rather than leaving the reader
#             to assume it.
# Output:     progress on stderr, ONE block on stdout headed CHECK 139 RESULT,
#             then CHECK 139 RESULT END (exit N). Exit 0: U4 was answered and
#             holds. 1: a measured row did not hold — the count is not a
#             sub-agent count, which is a finding for T7.2 and not something
#             this check fixes — or there was no run to read. 2: the check could
#             not measure (a tool is missing, a read did not answer, or a row
#             has no reading). A row the run never reached reads
#             `FAIL — not reached`, which is a row that was NOT measured.
#
# A NOTE ON WHAT IS NEVER PRINTED: no agent ID and no session ID reaches this
# script's stdout, its stderr, the block or any file it writes. They are Claude
# Code's own opaque claims — nothing in this tree verifies what a real Claude
# Code puts in them — and the counts are what U4's answer needs. The one
# sentence the block ends with is what the owner pastes back. The one document
# that carries an id is the raw `gcloud logging read` output, and it is deleted
# as soon as the counts have been read out of it (read_calls): the counts are
# what is kept, so a scratch directory kept with CHECK139_KEEP_SCRATCH=1 holds
# no id either.
#
# Knobs (all CHECK139_*): RUN (the run to read, as --run), BG_MODEL (the run's
# EFFECTIVE background model — the repository's `agent.models.background`, else
# the coder's own model when a provider serves the coder
# (config.EffectiveBackground, design/v1.md §6.1); left empty its count is not
# taken and the `models:` line is where its name is read, and a name that is
# also a stage's own model is refused rather than counted), DRY_RUN,
# KEEP_SCRATCH, SCAN (how many rows the listing is asked for, 50 by default),
# SINCE (the listing's window, 30d),
# MAX_CANDIDATES (how many rows are read while looking for one with a
# sub-agent, 6), LIMIT (entries per log read, 5000; a read that comes back at the
# limit is refused rather than counted, because gcloud answers with the newest
# entries first and the rest of the run would be missing), FRESHNESS (30d),
# LOG_VIEW (the view's full resource name, read from the project config by
# default), DOCTOR_TIMEOUT_SECONDS, CLI_TIMEOUT_SECONDS, LOG_TIMEOUT_SECONDS.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: the commands below read
# a run's own log, and the shell it is started from may be a login shell whose
# trace ends up in a log.
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
# The reason an abort carries into the block: a run that ends without one leaves
# a block of `not reached` rows and no word on why, and the block is what gets
# pasted back.
ABORT_REASON=""
die() {
  say "$*"
  ABORT_REASON=$*
  exit 2
}

stop() {
  fact aborted FAIL "$1"
  ABORT_REASON=$1
  owner "stopped: $1"
  exit 1
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# unmeasured: a row the check could NOT measure, for a reason that is not a
# finding about the run. The row still reads FAIL in the block — it is not a
# pass and must not be read as one — and the check ends as one that could not
# measure (exit 2).
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended this check at line $line of the script: the rows that read 'not reached' were NOT measured" ;;
  esac
  say "an internal error ended this check at line $line"
  exit 2
}

need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 139 needs bash"
  local t
  for t in mktemp python3 gcloud fugaro cut sed grep awk tr head date; do
    command -v "$t" >/dev/null 2>&1 || die "Check 139 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk139-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
READS=0
STOP=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk139.XXXXXXXX") || die "Check 139 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  write_reader
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
    say "CHECK139_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# restore: this check makes nothing, and the row says so rather than missing. It
# is in the block because every check's block has it, and because "nothing was
# created" is a claim a reader is owed.
restore() {
  RESTORE_VERDICT=PASS
  RESTORE_NOTE="nothing was created and nothing was written: no job, no run, no build, no object, no binding, no account and no policy. Its whole footprint is ${READS} read(s) — the doctor, the listing, the run's own record and its log entries, whichever of them this run needed — and the scratch directory"
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
  # A run that ended on its own error carries the reason into the block: the rows
  # it never reached read `not reached` there, and without this line nothing in
  # the block would say why.
  if [ -n "$ABORT_REASON" ] && ! printf '%s\n' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|${ABORT_REASON}"
  fi
  # Both rows are added before the verdict is taken, because the verdict is
  # taken from the facts: a directory this check could not remove is a FAIL, and
  # a block that printed `scratch-removed: FAIL` beside `END (exit 0)` would read
  # as a pass.
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

# ------------------------------------------------------------------- the tools
# run_bounded: one command, bounded, with its output in the scratch directory.
# The child execs, so the pid the bound kills IS the command: a hung cloud call
# would otherwise outlive the check that gave up on it, which is the one thing
# the bound exists to prevent. The ERR trap is dropped in it too: this check runs
# commands whose failing exit status is one of its own readings (a run that
# failed, a listing with no run in it), and a trap inherited here would turn each
# of them into "an internal error" and a status of 2 before the step could
# report it.
run_bounded() { # run_bounded SECONDS KEY CMD...
  local secs=$1 key=$2
  shift 2
  BOUNDED_TIMED_OUT=no
  # `>` and not `>>`: the search below reads several runs' entries under the
  # same key, and a file that accumulated one document per candidate would stop
  # being a document a reader could parse from the second candidate on.
  ( trap - ERR; exec "$@" ) >"$SCRATCH/$key.out" 2>"$SCRATCH/$key.err" &
  # The poll is a tenth of a second and the bound is counted in tenths: a read
  # that answers at once — every one of them, when the installation is healthy —
  # would otherwise cost this check a whole second each.
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
  READS=$((READS + 1))
}

last_line() {
  printf '%s\n' "$1" | grep -v '^[[:space:]]*$' | tail -n 1 | head -c 200 || true
}

# ------------------------------------------------------------------ the reader
reader() { python3 "$SCRATCH/reader.py" "$@"; }

# field_of NAME TEXT: the value of a NAME= line the reader printed.
field_of() { # field_of NAME TEXT
  printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1 || true
}

write_reader() {
  cat >"$SCRATCH/reader.py" <<'PY'
#!/usr/bin/env python3
"""Check 139's reader: three readings, and no agent ID in any of them.

runs   argv: `fugaro runs ls --json`'s document. Prints the window's counts and
       one `cand=` line per run this check could read — settled, its budget on
       (`cost.model_basis` api-list: vertex or api-key), and carrying an
       execution — newest first. A settled run whose basis is `subscription` is
       an oauth run and is counted rather than offered: an oauth run makes no
       gateway call at all, so it has no entry to read.
diag   argv: `fugaro diagnose --json`'s document. Prints the row's run id, its
       execution, its status and outcome, and one `stage=` line per stage with
       the record's own `agents` count beside its name and model. That count is
       the relay's count of sub-agent tool calls: a DIFFERENT figure from the
       log's distinct agent ids, printed to be recorded beside it and never to
       be compared with it.
calls  argv: the log entries' JSON, and the run's effective background model
       when one was named. Prints the counts every row of the block is read
       from, the per-stage main-session counts, the models the entries name, and
       one `off=` line per offending entry — its stage and model, never an id.

Nothing here prints an agent ID or a session ID: they are Claude Code's own
opaque claims, and the counts are what U4's answer needs.
"""

import json
import sys


def load(path):
    try:
        with open(path) as fh:
            return json.load(fh)
    except (OSError, ValueError):
        return None


def as_dict(value):
    return value if isinstance(value, dict) else {}


def runs(path):
    doc = load(path)
    if not isinstance(doc, dict):
        print("reader: the listing is not a document", file=sys.stderr)
        return 2
    rows = doc.get("runs")
    if not isinstance(rows, list):
        rows = []
    settled, cands, subscription, noexec = 0, [], 0, 0
    for row in rows:
        if not isinstance(row, dict) or row.get("settled") is not True:
            continue
        settled += 1
        basis = str(as_dict(row.get("cost")).get("model_basis") or "")
        execution = str(row.get("execution") or "")
        if basis == "subscription":
            subscription += 1
            continue
        if basis != "api-list":
            continue
        if not execution:
            noexec += 1
            continue
        cands.append(row)
    cands.sort(key=lambda r: str(r.get("created") or ""), reverse=True)
    print("rows=%d" % len(rows))
    print("settled=%d" % settled)
    print("gateway=%d" % len(cands))
    print("subscription=%d" % subscription)
    print("noexec=%d" % noexec)
    for row in cands:
        print("cand=%s|%s|%s|%s|%s|%s" % (
            row.get("run_id") or "", row.get("repo") or "", row.get("execution") or "",
            row.get("status") or "", row.get("outcome") or "", row.get("created") or ""))
    return 0


def diag(path):
    doc = load(path)
    if not isinstance(doc, dict):
        print("reader: the diagnosis is not a document", file=sys.stderr)
        return 2
    row = as_dict(doc.get("row"))
    print("run_id=" + str(row.get("run_id") or ""))
    print("execution=" + str(row.get("execution") or ""))
    print("status=" + str(row.get("status") or ""))
    print("outcome=" + str(row.get("outcome") or ""))
    print("repo=" + str(row.get("repo") or ""))
    print("created=" + str(row.get("created") or ""))
    stages = doc.get("stages")
    if not isinstance(stages, list):
        stages = []
    for stage in stages:
        if not isinstance(stage, dict):
            continue
        agents = stage.get("agents")
        print("stage=%s|%s|%s" % (stage.get("name") or "(unnamed)",
                                  "-" if agents is None else agents,
                                  stage.get("model") or "-"))
    return 0


def calls(path, bg_model):
    entries = load(path)
    if not isinstance(entries, list):
        print("reader: the entries are not a list", file=sys.stderr)
        return 2
    empty = 0
    ids = {}          # agent id -> the session ids its entries name
    id_stage = {}     # agent id -> the stage it was first seen in
    id_model = {}     # agent id -> the model it was first seen under
    main = {}         # stage -> the session ids its empty-agent entries name
    main_count = {}   # stage -> how many empty-agent entries it has
    models = {}
    bg = bg_with_id = 0
    refused_entries = 0
    refused_ids, admitted_ids = set(), set()
    offenders = []

    for entry in entries:
        if not isinstance(entry, dict):
            continue
        payload = as_dict(entry.get("jsonPayload"))
        stage = str(payload.get("stage") or "(none)")
        model = str(payload.get("model") or "(none)")
        agent = str(payload.get("agent_id") or "")
        session = str(payload.get("session_id") or "")
        models[model] = models.get(model, 0) + 1
        # A call refused before it reserved settles nothing: the gateway logs it
        # with settled `zero` and a status that is not 200 (a halt's 403, an
        # upstream error, or a call nothing was sent for).
        refused = payload.get("settled") == "zero" and payload.get("status") != 200
        if refused:
            refused_entries += 1
        if bg_model and model == bg_model:
            bg += 1
            if agent:
                bg_with_id += 1
                offenders.append(("bg_with_id", stage, model))
        if not agent:
            empty += 1
            main.setdefault(stage, set()).add(session)
            main_count[stage] = main_count.get(stage, 0) + 1
            continue
        ids.setdefault(agent, set()).add(session)
        id_stage.setdefault(agent, stage)
        id_model.setdefault(agent, model)
        (refused_ids if refused else admitted_ids).add(agent)

    multi_session = shared_main = 0
    main_all = set()
    for sessions in main.values():
        main_all |= sessions
    for agent, sessions in ids.items():
        stage = id_stage[agent]
        model = id_model[agent]
        if len(sessions) > 1:
            multi_session += 1
            offenders.append(("multi_session", stage, model))
        # Run-wide, not per stage: a sub-agent's session being an empty-agent
        # entry's session is what says the empty agent_id is not the main
        # session, and the stage a sub-agent runs under is a NAME the runner
        # reuses across sessions (every review round and retry), so comparing
        # within one stage would miss the same session under two names.
        if sessions & main_all:
            shared_main += 1
            offenders.append(("shared_main", stage, model))
    refused_only = refused_ids - admitted_ids
    for agent in sorted(refused_only):
        offenders.append(("refused_only", id_stage.get(agent, "(none)"), id_model.get(agent, "(none)")))
    multi_stages = sorted(s for s, sessions in main.items() if len(sessions) > 1)
    for stage in multi_stages:
        offenders.append(("main_multi_session", stage, "(%d sessions)" % len(main[stage])))

    print("entries=%d" % len(entries))
    print("empty=%d" % empty)
    print("distinct=%d" % len(ids))
    print("multi_session=%d" % multi_session)
    print("shared_main=%d" % shared_main)
    print("main_multi=%d" % len(multi_stages))
    print("refused_only=%d" % len(refused_only))
    print("refused_entries=%d" % refused_entries)
    print("bg=%d" % bg)
    print("bg_with_id=%d" % bg_with_id)
    print("bg_named=%s" % ("yes" if bg_model else "no"))
    print("has_subagent=%s" % ("yes" if ids else "no"))
    named = ", ".join("%s x%d" % (m, n) for m, n in sorted(models.items(), key=lambda kv: (-kv[1], kv[0])))
    print("models=" + named)
    for stage in sorted(main_count):
        sessions = main[stage]
        blank = "yes" if sessions == {""} else "no"
        print("stage_main=%s|%d|%d|%s" % (stage, main_count[stage], len(sessions), blank))
    for kind, stage, model in offenders[:8]:
        print("off=%s|%s|%s" % (kind, stage, model))
    if len(offenders) > 8:
        print("off_more=%d" % (len(offenders) - 8))
    return 0


def main(argv):
    if len(argv) < 3:
        print("reader: need a reading and a file", file=sys.stderr)
        return 2
    if argv[1] == "runs":
        return runs(argv[2])
    if argv[1] == "diag":
        return diag(argv[2])
    if argv[1] == "calls":
        return calls(argv[2], argv[3] if len(argv) > 3 else "")
    print("reader: unknown reading %s" % argv[1], file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
PY
}

# ------------------------------------------------------------------ the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check139.sh — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy a run's own log to the terminal"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy a run's own log"
}

# guard_agent_session: the check is the owner's — it reads a real installation's
# log with the owner's own credentials — and a coding agent's session is not the
# session this check is written for. Refusing here rather than letting a later
# command refuse is what makes the block say why.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 139 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

# is_owners_name NAME...: `belong`, EdgeWeb and EdgeServer are the owner's own
# installations, and Check 130's rule — a substring test on the lowercased name,
# so a name that merely contains one is refused too — is that a name of theirs is
# refused rather than read.
is_owners_name() {
  case "$(printf '%s' "$*" | tr '[:upper:]' '[:lower:]')" in
  *belong* | *edgeweb* | *edgeserver*) return 0 ;;
  esac
  return 1
}

# guard_belong: Check 130's rule, applied to EVERY name this check has — the
# account it runs as, the GCP project and the Fugaro project (hard-coded below),
# the run it was asked to read, and the log view its entries are read from, which
# CHECK139_LOG_VIEW or the project config can name. All of them are judged here,
# before guard_project reads anything of the installation, so a name of the
# owner's own is the reason the run ended rather than a stop another guard's
# message could hide behind. The log view is resolved before this guard for
# exactly that reason (resolve_log_view): a view is a name this check reads with,
# and it is refused like the other four.
guard_belong() {
  if is_owners_name "${ACCOUNT:-}" "$P" "$INSTALLATION" "${RUN:-}" "${LOG_VIEW:-}"; then
    stop "the account, project, run or log view this check would read names belong, EdgeWeb or EdgeServer: Check 139 is never run there — it reads the sandbox installation only (Fugaro project ${INSTALLATION} on GCP project ${P}) and names no other. A view named by CHECK139_LOG_VIEW or by the project config is refused here too, before a single read"
  fi
  fact belong PASS "nothing this check names is belong, EdgeWeb or EdgeServer: the account, the Fugaro project and the GCP project (both hard-coded in this file), the run, and the log view the entries are read from — every name this check has, each judged here before anything is read"
}

guard_project() {
  # The Fugaro project and the GCP project are hard-coded, never read from
  # `gcloud config`. This check reads a real installation's log: run anywhere
  # else and it reads another installation's, which is why this stops rather
  # than warns.
  local doctor_out v
  run_bounded "$DOCTOR_TIMEOUT" doctor fugaro doctor --project "$INSTALLATION" --all
  # doctor exits 1 whenever any of its checks is not ok, warnings included, and
  # still prints its document; only a timeout or another status is no answer
  # (#2719). --all is what makes it print the project line at all.
  if [ "$BOUNDED_TIMED_OUT" = yes ] || { [ "$BOUNDED_STATUS" != 0 ] && [ "$BOUNDED_STATUS" != 1 ]; }; then
    stop "fugaro doctor --project ${INSTALLATION} --all did not answer (exit ${BOUNDED_STATUS}): it is how this check knows which installation it is reading, and it reads a real installation's log. Run it here yourself and read what it says"
  fi
  doctor_out=$(cat "$SCRATCH/doctor.out" 2>/dev/null || true)
  if ! printf '%s\n' "$doctor_out" | grep '^project: .*(GCP '"$P"',' >/dev/null; then
    stop "this is not the ${P} installation: fugaro doctor --project ${INSTALLATION} --all printed no 'project: ... (GCP ${P},' line. Run it here yourself and read its output before going on — this check reads the log of ${P} and of no other project, and never belong"
  fi
  v=$(fugaro version 2>/dev/null | head -n 1 || true)
  fact "fugaro-version" PASS "${v:-fugaro version printed nothing} (this check reads a run's own log entries and its record, not a behaviour of this build: any build that can read the runs bucket will do)"
  fact "project-region" PASS "${INSTALLATION} on GCP project ${P} (sandbox only; never belong)"
  read_log_view
}

# resolve_log_view: the view a run's entries live in, when the installation
# isolates its logs. It is the project config's `log_view` line
# (internal/localcfg), and the project config is read from the file rather than
# through a command because no command prints that line. Left empty, gcloud
# reads the project's default, which is where an installation without isolation
# writes. Resolved BEFORE guard_belong — it is a local file read and nothing
# else — so the view is judged by the same rule as the account, the project and
# the run: a view of the owner's own installations is refused rather than read,
# and it is refused before the guard's own row is recorded.
resolve_log_view() {
  LOG_VIEW_SRC=""
  LOG_VIEW_CFG=""
  if [ -n "$LOG_VIEW" ]; then
    LOG_VIEW_SRC=flag
    return 0
  fi
  local file cfg=""
  for file in "${FUGARO_CONFIG:-}" "${XDG_CONFIG_HOME:-}/fugaro/projects/${INSTALLATION}.yaml" "${HOME:-}/.config/fugaro/projects/${INSTALLATION}.yaml"; do
    [ -n "$file" ] || continue
    if [ -f "$file" ]; then cfg=$file; break; fi
  done
  if [ -z "$cfg" ]; then
    LOG_VIEW_SRC=no-config
    return 0
  fi
  LOG_VIEW_SRC=config
  LOG_VIEW_CFG=$cfg
  LOG_VIEW=$(sed -n 's/^log_view:[[:space:]]*//p' "$cfg" | head -n 1 || true)
  LOG_VIEW=${LOG_VIEW%\"}
  LOG_VIEW=${LOG_VIEW#\"}
}

# read_log_view: the row, recorded once the guards have run.
read_log_view() {
  case "$LOG_VIEW_SRC" in
  flag)
    fact log-view PASS "CHECK139_LOG_VIEW names the view's full resource name (${LOG_VIEW}), so the read below is scoped to it"
    ;;
  no-config)
    fact log-view INFO "no project config for ${INSTALLATION} was found (looked at \$FUGARO_CONFIG, \$XDG_CONFIG_HOME/fugaro/projects/${INSTALLATION}.yaml and \$HOME/.config/fugaro/projects/${INSTALLATION}.yaml), so no log_view was read and the read below is scoped to the project itself. Set CHECK139_LOG_VIEW to the view's full resource name (projects/<project>/locations/<location>/buckets/<bucket>/views/<view>) if this installation isolates its logs"
    ;;
  *)
    if [ -z "$LOG_VIEW" ]; then
      fact log-view INFO "${LOG_VIEW_CFG} carries no log_view line, so this installation does not isolate its logs and the read below is scoped to the project itself"
    else
      fact log-view PASS "the project config ${LOG_VIEW_CFG} names the view ${LOG_VIEW}: the read below is scoped to it with --resource-names, which is the only way gcloud takes a full resource name (--view is refused without --bucket and --location)"
    fi
    ;;
  esac
}

# -------------------------------------------------------------------- the run
# read_calls EXEC: the run's own `model call` entries into $SCRATCH/calls.out
# and the counts into $SCRATCH/calls.txt. 0 when the entries were read, 1 when
# EXEC is not an execution name, 2 when the read did not answer, 3 when what it
# answered is not the document this check reads.
#
# The raw read is the ONE document that carries an agent_id and a session_id, so
# every path out of this function deletes it once the counts have been read out
# of it: what a scratch directory holds — kept or not — is the counts, never an
# id. The messages that point the owner at what a failed read printed say so
# rather than pointing at a file that is gone.
read_calls() { # read_calls EXEC
  local exec_=$1 region job name filter rc=0
  region=$(printf '%s' "$exec_" | cut -d/ -f4)
  job=$(printf '%s' "$exec_" | cut -d/ -f6)
  name=$(printf '%s' "$exec_" | cut -d/ -f8)
  if [ -z "$region" ] || [ -z "$job" ] || [ -z "$name" ]; then
    return 1
  fi
  # The filter the CLI's own reader builds (internal/backend/gcp/logs.go's
  # executionFilter), plus the one attribute that makes an entry a gateway call.
  filter="resource.type=\"cloud_run_job\" AND resource.labels.location=\"${region}\" AND resource.labels.job_name=\"${job}\" AND labels.\"run.googleapis.com/execution_name\"=\"${name}\" AND jsonPayload.message=\"model call\""
  if [ -n "$LOG_VIEW" ]; then
    run_bounded "$LOG_TIMEOUT" calls gcloud --quiet logging read --resource-names="$LOG_VIEW" "$filter" --limit="$LIMIT" --freshness="$FRESHNESS" --format=json
  else
    run_bounded "$LOG_TIMEOUT" calls gcloud --quiet logging read --project="$P" "$filter" --limit="$LIMIT" --freshness="$FRESHNESS" --format=json
  fi
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    # A read that did not answer can still have written part of its document:
    # the raw output goes with the answer it did not give.
    rm -f "$SCRATCH/calls.out"
    return 2
  fi
  reader calls "$SCRATCH/calls.out" "$BG_MODEL" >"$SCRATCH/calls.txt" 2>"$SCRATCH/reader.err" || rc=$?
  rm -f "$SCRATCH/calls.out"
  if [ "$rc" != 0 ]; then return 3; fi
  return 0
}

# step_run: which run is read. A run named with --run is read as it is — the
# owner picked it, and the stops below say what is wrong with it if it is not a
# gateway run with a sub-agent. With no --run the check finds one itself, and
# the reading a listing cannot take is the one that decides: a row's cost
# carries the run's model basis but not whether a sub-agent ever called, so each
# candidate's entries are read until one carries a non-empty agent_id.
step_run() {
  if [ -n "$RUN" ]; then
    RUN_FROM="named with --run"
    return 0
  fi
  say "finding a run: fugaro runs ls --all --project ${INSTALLATION} --json --since=${SINCE} --limit=${SCAN}"
  run_bounded "$CLI_TIMEOUT" ls fugaro runs ls --all --project "$INSTALLATION" --json --since="$SINCE" --limit="$SCAN"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    die "fugaro runs ls --json did not answer (exit ${BOUNDED_STATUS}): the listing is how this check finds a run to read, and it needs an account that may read the runs bucket of ${P}. Its output is in $SCRATCH/ls.out and $SCRATCH/ls.err; run 'fugaro runs ls --all --project ${INSTALLATION} --json --since=${SINCE} --limit=${SCAN}' here yourself and read what it says"
  fi
  local out
  if ! out=$(reader runs "$SCRATCH/ls.out" 2>"$SCRATCH/reader.err"); then
    die "fugaro runs ls --json printed a document this check could not read ($(last_line "$(cat "$SCRATCH/reader.err" 2>/dev/null || true)")): what it printed is in $SCRATCH/ls.out"
  fi
  printf '%s\n' "$out" >"$SCRATCH/cands.txt"
  local rows settled gateway subscription noexec
  rows=$(field_of rows "$out")
  settled=$(field_of settled "$out")
  gateway=$(field_of gateway "$out")
  subscription=$(field_of subscription "$out")
  noexec=$(field_of noexec "$out")
  if [ "${gateway:-0}" -eq 0 ]; then
    fact run FAIL "no run to read: the listing of the last ${SINCE} holds ${rows:-0} row(s), ${settled:-0} of them settled, and ${gateway:-0} of those is a settled gateway run with an execution this check could read (${subscription:-0} settled run(s) are oauth — model_basis subscription — and an oauth run makes no gateway call at all, so it has no entry to read; ${noexec:-0} carries no execution). To get one: launch a short gateway run that spawns a sub-agent — in a sandbox repository's checkout, with the budget on (agent.auth api-key or vertex, never oauth), 'fugaro run --repo <owner/name> \"<a task that delegates one search to a sub-agent>\"' — wait for it to settle, then run this check again with no arguments. This check launches nothing itself and spends nothing, and this row is not a finding about U4: it is this check having no run to read"
    STOP=yes
    return 0
  fi
  local i=0 line rid rexec rc skipped=0
  while IFS= read -r line; do
    case "$line" in cand=*) ;; *) continue ;; esac
    i=$((i + 1))
    [ "$i" -le "$MAX_CANDIDATES" ] || break
    rid=$(printf '%s' "${line#cand=}" | cut -d'|' -f1)
    rexec=$(printf '%s' "${line#cand=}" | cut -d'|' -f3)
    say "  reading ${rid}'s entries (candidate ${i} of ${gateway}, newest first)"
    rc=0
    read_calls "$rexec" || rc=$?
    if [ "$rc" != 0 ]; then
      skipped=$((skipped + 1))
      say "  ${rid}: its entries could not be read (exit ${rc}), so this candidate says nothing about a sub-agent"
      continue
    fi
    if [ "$(field_of has_subagent "$(cat "$SCRATCH/calls.txt" 2>/dev/null || true)")" = yes ]; then
      RUN=$rid
      RUN_FROM="found in the listing (settled, its budget on, and its entries carry a sub-agent's own id)"
      CALLS_EXEC=$rexec
      CALLS_TEXT=$(cat "$SCRATCH/calls.txt")
      return 0
    fi
  done <<<"$(printf '%s\n' "$out" | grep '^cand=' || true)"
  local read_n=$i
  if [ "$read_n" -gt "$MAX_CANDIDATES" ]; then read_n=$MAX_CANDIDATES; fi
  local read_phrase="${read_n} of those runs"
  if [ "$read_n" -gt 1 ]; then read_phrase="${read_n} of those runs (up to CHECK139_MAX_CANDIDATES=${MAX_CANDIDATES})"; fi
  local skip_phrase=""
  if [ "$skipped" -gt 0 ]; then skip_phrase=", and ${skipped} of those reads did not answer at all, which is not a finding either"; fi
  fact run FAIL "no run to read: the listing of the last ${SINCE} holds ${rows:-0} row(s), ${settled:-0} settled, and ${gateway:-0} of those is a settled gateway run with an execution (${subscription:-0} settled run(s) are oauth — model_basis subscription — and an oauth run makes no gateway call at all, so it has no entry to read; ${noexec:-0} carries no execution). This check read the entries of ${read_phrase} and not one carries a non-empty agent_id${skip_phrase}, so none of them is a run whose sub-agents the gateway saw. To get one: launch a short gateway run that spawns a sub-agent — in a sandbox repository's checkout, with the budget on (agent.auth api-key or vertex, never oauth), 'fugaro run --repo <owner/name> \"<a task that delegates one search to a sub-agent>\"' — wait for it to settle, then run this check again with no arguments. This check launches nothing itself and spends nothing, and this row is not a finding about U4: it is this check having no run to read"
  STOP=yes
  return 0
}

# step_execution: the run's own record, for the execution the filter is built
# from and for the record's own stage `agents` — recorded beside the log's
# distinct count and never compared with it.
step_execution() {
  say "reading the run's own record: fugaro diagnose ${RUN} --project ${INSTALLATION} --json"
  run_bounded "$CLI_TIMEOUT" diag fugaro diagnose "$RUN" --project "$INSTALLATION" --json
  # diagnose exits 1 when the execution is one it will not follow (--region);
  # it still prints its document, so 1 is an answer and only a timeout or
  # another status is not (#2719).
  if [ "$BOUNDED_TIMED_OUT" = yes ] || { [ "$BOUNDED_STATUS" != 0 ] && [ "$BOUNDED_STATUS" != 1 ]; }; then
    die "fugaro diagnose ${RUN} --project ${INSTALLATION} --json did not answer (exit ${BOUNDED_STATUS}): the run's own record names the execution this check reads the entries of. Its output is in $SCRATCH/diag.out and $SCRATCH/diag.err; run it here yourself and read what it says"
  fi
  local out
  if ! out=$(reader diag "$SCRATCH/diag.out" 2>"$SCRATCH/reader.err"); then
    die "fugaro diagnose ${RUN} printed a document this check could not read ($(last_line "$(cat "$SCRATCH/reader.err" 2>/dev/null || true)")): what it printed is in $SCRATCH/diag.out"
  fi
  DIAG=$(printf '%s\n' "$out")
  if [ -z "$(field_of run_id "$DIAG")" ]; then
    die "fugaro diagnose ${RUN} --project ${INSTALLATION} named no run: ${RUN} is not a run of this installation, or the read did not reach its record. Its output is in $SCRATCH/diag.out; list the runs with 'fugaro runs ls --all --project ${INSTALLATION} --json' and name one of them with --run"
  fi
  REPO=$(field_of repo "$DIAG")
  RUN_STATUS=$(field_of status "$DIAG")
  RUN_OUTCOME=$(field_of outcome "$DIAG")
  RUN_CREATED=$(field_of created "$DIAG")
  EXEC=$(field_of execution "$DIAG")
  local row_exec=$CALLS_EXEC
  if [ -z "$EXEC" ]; then
    unmeasured execution "${RUN} names no execution in its own record, so there are no entries to read: a run from before the launcher recorded one, or one of another backend. The row says which run it is: 'fugaro diagnose ${RUN} --project ${INSTALLATION}'"
    STOP=yes
    return 0
  fi
  if [ -n "$row_exec" ] && [ "$row_exec" != "$EXEC" ]; then
    fact execution FAIL "the listing's row for ${RUN} names the execution ${row_exec} and the run's own record names ${EXEC}: the entries read below are the record's, and the listing's row is a different execution — read both before this check's answer is recorded"
    STOP=yes
    return 0
  fi
  fact execution PASS "${EXEC} — projects/<project>/locations/<region>/jobs/<job>/executions/<name>, off the run's own record, which is what the filter below is built from (internal/backend/gcp/logs.go's executionFilter)"
  fact run PASS "${RUN}${REPO:+ (${REPO})}, ${RUN_STATUS:-status unread}${RUN_OUTCOME:+, outcome ${RUN_OUTCOME}}, started ${RUN_CREATED:-unknown} — ${RUN_FROM}"
}

# step_calls: the entries, and the counts every row below is read from. Two
# stops, and both are the vacuous reading this check must never record: no entry
# at all answers nothing, and a run whose sub-agents the gateway never saw is
# not one U4 can be answered from. Neither is a failing answer — the check is
# run again on a run whose entries are there.
step_calls() {
  if [ "$CALLS_EXEC" != "$EXEC" ]; then
    say "reading the run's own entries: gcloud logging read over ${EXEC}"
    local rc=0
    read_calls "$EXEC" || rc=$?
    case "$rc" in
    0) ;;
    1)
      unmeasured entries "the run's record names ${EXEC}, which is not projects/<project>/locations/<region>/jobs/<job>/executions/<name>, so this check cannot build the filter the CLI's own reader builds"
      STOP=yes
      return 0
      ;;
    2)
      # Both a bound that killed the read and a gcloud that exited non-zero
      # arrive here as rc=2, and run_bounded sets BOUNDED_TIMED_OUT to yes or
      # no, never to nothing: the suffix is added on the value, or a permission
      # failure (exit 1) would read as a read that timed out.
      local timeout_phrase=""
      if [ "$BOUNDED_TIMED_OUT" = yes ]; then timeout_phrase=", timed out after ${LOG_TIMEOUT}s"; fi
      unmeasured entries "the run's log entries could not be read (gcloud exited ${BOUNDED_STATUS}${timeout_phrase}): reading them needs roles/logging.viewer on ${P}. Its output is in $SCRATCH/calls.err; read the entries yourself with 'gcloud logging read' and the filter this check prints in --dry-run"
      STOP=yes
      return 0
      ;;
    *)
      unmeasured entries "the log entries are not the document this check reads ($(last_line "$(cat "$SCRATCH/reader.err" 2>/dev/null || true)")): what gcloud printed is not kept, because the raw entries carry the agent ids and session ids this check never writes down — read the entries yourself with 'gcloud logging read' and the filter this check prints in --dry-run"
      STOP=yes
      return 0
      ;;
    esac
    CALLS_TEXT=$(cat "$SCRATCH/calls.txt")
  fi
  CALLS=$CALLS_TEXT
  ENTRIES=$(field_of entries "$CALLS")
  EMPTY=$(field_of empty "$CALLS")
  DISTINCT=$(field_of distinct "$CALLS")
  MODELS=$(field_of models "$CALLS")
  if [ "${ENTRIES:-0}" -eq 0 ]; then
    unmeasured entries "no model call entry of ${RUN} is in the window: the run did not go through the gateway (an oauth run makes none, and the budget must be on — api-key or vertex), or its entries have aged out (--freshness=${FRESHNESS}, or the view's own retention), or the view this check read is not the one this installation writes to (log-view row above). Pick a gateway run whose entries are still readable, or name the view with CHECK139_LOG_VIEW"
    STOP=yes
    return 0
  fi
  # The read is bounded by --limit, and gcloud answers with the NEWEST entries
  # first: a reading that came back at the limit may be missing older ones, and
  # every count below is a count of what came back. A partial reading can read a
  # FAIL the whole run does not have (an id whose other session is in the part
  # that was cut) as easily as it can read a PASS, so it is not taken at all.
  if [ "${ENTRIES:-0}" -ge "$LIMIT" ]; then
    unmeasured entries "${ENTRIES} model call entries were read for ${EXEC}, which is the whole of --limit=${LIMIT} (CHECK139_LIMIT), so the reading may be missing older entries: gcloud answers with the newest first and stops at the limit. Every row below is a count over what came back — the empty-agent_id count, the distinct agent ids, the ids seen only on refused entries — and a run whose calls reach the limit can read a finding it does not have (an id whose second session is in the part that was cut) as easily as it can read a clean one. So no row below is taken. Raise CHECK139_LIMIT above the run's own call count and run the check again, or read a shorter run: the entries of one run are what this check is about, and part of them is not the run"
    STOP=yes
    return 0
  fi
  fact entries PASS "${ENTRIES} model call entries for ${EXEC} in ${P}, read with the filter the CLI's own reader builds (--limit=${LIMIT} --freshness=${FRESHNESS}${LOG_VIEW:+ --resource-names=${LOG_VIEW}}): ${EMPTY} of them carry an empty agent_id and ${DISTINCT} distinct non-empty agent ids are in them. The models the entries name: ${MODELS:-none}"
  if [ "${DISTINCT:-0}" -eq 0 ]; then
    unmeasured "subagents-distinct" "no entry of ${RUN} carries a non-empty agent_id, so the run spawned no sub-agent the gateway saw and there is nothing here to answer U4 from: pick a run with a sub-agent. (A run whose every call is the main session's is not one U4 can be answered from, and the ${EMPTY} entries with an empty agent_id above are the whole of what it logged)"
    STOP=yes
    return 0
  fi
}

# offenders KIND: the offending entries of one kind, as `stage/model` pairs,
# bounded. Never an id: what is printed is where the entry was, not what it
# claimed.
offenders() { # offenders KIND
  printf '%s\n' "$CALLS" | sed -n "s/^off=$1|//p" | awk -F'|' '{ printf "%s%s/%s", (n++ ? ", " : ""), $1, $2 }'
}

offenders_more() {
  local more
  more=$(field_of off_more "$CALLS")
  if [ -n "$more" ]; then printf ' (and %s more)' "$more"; fi
}

# step_verdict: U4, one row per part of it, then the sentence to paste back.
step_verdict() {
  local multi shared refused_only bg bg_with_id bg_named refused_entries v_sub v_bg v_ref

  multi=$(field_of multi_session "$CALLS")
  shared=$(field_of shared_main "$CALLS")
  refused_only=$(field_of refused_only "$CALLS")
  refused_entries=$(field_of refused_entries "$CALLS")
  bg=$(field_of bg "$CALLS")
  bg_with_id=$(field_of bg_with_id "$CALLS")
  bg_named=$(field_of bg_named "$CALLS")

  # 1. The main session carries an empty agent_id, and no session an entry with
  # an empty agent_id names is also a sub-agent's. A stage NAME carrying more
  # than one session is not a finding: the runner reuses stage names across
  # sessions by design — every senior review round and every retry is
  # r.stage("review") with a new session (internal/runner/runner.go), a fix with
  # no session to resume starts one, and an account-limited attempt gets a new
  # one — so a run with two review rounds reads two sessions under one name.
  local stages main_multi
  stages=$(printf '%s\n' "$CALLS" | sed -n 's/^stage_main=//p' | awk -F'|' '{ printf "%s%s %s entr%s/%s session%s", (n++ ? ", " : ""), $1, $2, ($2 == 1 ? "y" : "ies"), $3, ($3 == 1 ? "" : "s") }')
  main_multi=$(field_of main_multi "$CALLS")
  if [ "${main_multi:-0}" -gt 0 ]; then
    fact "main-empty" INFO "not a finding: a stage name carried more than one session — $(offenders main_multi_session)$(offenders_more) — and the runner reuses stage names across sessions by design (every senior review round and every retry is the same stage name with a new session, and a fix with no session to resume starts one), so two review rounds read two sessions under one name. What this row asserts is that no session id is shared between an entry with an empty agent_id and a sub-agent's entry anywhere in the run, and the subagents-distinct row's shared count is where that is judged: ${shared} here. The per-stage counts are ${stages:-none}"
  else
    fact "main-empty" PASS "every entry with an empty agent_id names a session no sub-agent's entry names: ${stages:-no stage logged one} (entries with an empty agent_id, and the distinct session ids they name). No stage's main session carries an id, which is what grouping on an empty agent_id means"
  fi

  # 2. Each sub-agent carries a distinct one, its entries name one session, and
  # that session is no empty-agent_id entry's, anywhere in the run.
  local distinct
  distinct=$(field_of distinct "$CALLS")
  if [ "${multi:-0}" -eq 0 ] && [ "${shared:-0}" -eq 0 ]; then
    v_sub=PASS
    fact "subagents-distinct" PASS "${distinct} distinct non-empty agent ids are in the entries, no id's entries span more than one session (0), and no sub-agent's session is one an entry with an empty agent_id also names (0): an id is one sub-agent and not a session's rename, and a sub-agent is not the main session relabelled"
  else
    v_sub=FAIL
    fact "subagents-distinct" FAIL "${distinct} distinct non-empty agent ids are in the entries, and ${multi} of them span more than one session while ${shared} name a session an entry with an empty agent_id also names (anywhere in the run): an id that outlives its session is a rename rather than a sub-agent, and a sub-agent whose session is an empty-agent_id entry's is the main session relabelled. The offending entries, by stage/model: $(offenders multi_session)$(offenders shared_main)$(offenders_more)"
  fi

  # 3. The background model's calls carry none. The model is the run's EFFECTIVE
  # background — config.EffectiveBackground: the file's agent.models.background,
  # else the model the provider serves the coder with (design/v1.md §6.1), which
  # the run fills before its pins are judged — and it is read off the models:
  # line when it is not named. So the name can ALSO be a stage's own model, and
  # then every call of that stage matches it: the record's stage models are what
  # says so, and the row is not taken when one of them is the name, because
  # counting the stage's calls as background calls would FAIL it on the very
  # sub-agents it counts.
  local models bg_stage=""
  models=$(field_of models "$CALLS")
  if [ -n "$BG_MODEL" ]; then
    bg_stage=$(printf '%s\n' "$DIAG" | sed -n 's/^stage=//p' | awk -F'|' -v m="$BG_MODEL" '$3 == m { printf "%s%s", (n++ ? ", " : ""), $1 }')
  fi
  if [ "$bg_named" = no ]; then
    v_bg=INFO
    fact "background-none" INFO "not taken: no background model was named. It is the run's EFFECTIVE background — the repository's agent.models.background, else the coder's own model when a provider serves the coder (config.EffectiveBackground, design/v1.md §6.1) — and that is the name to give with --bg-model (or CHECK139_BG_MODEL). The models the entries name are ${models:-none}, and that line is where the name is read: with it named, this row counts that model's entries and how many of them carry an agent_id, and a background call that carried one would be counted as a sub-agent and would make the count wrong"
  elif [ -n "$bg_stage" ]; then
    v_bg=UNMEASURED
    unmeasured "background-none" "the model named (${BG_MODEL}) is also a stage's own model — ${bg_stage} ran on it — so the name cannot tell a background call from that stage's calls: every call of the stage matches, its sub-agents' included, and counting them as background calls would make this row FAIL on the sub-agents it counts. The background model is the run's EFFECTIVE one (config.EffectiveBackground: the file's agent.models.background, else the coder's own model when a provider serves the coder, design/v1.md §6.1), so a repository that names none has its coder's model for a background and this is the row that meets it. This row is not taken: give the repository a background of its own — agent.models.background, a model no stage runs — and read a gateway run of it, where every entry that model names is a background call and nothing else. The models the entries name are ${models:-none}"
  elif [ "${bg:-0}" -eq 0 ]; then
    unmeasured "background-none" "no entry of ${RUN} names the background model ${BG_MODEL}, so this row was not taken: either the run never called it (a short run may not), or ${BG_MODEL} is not this run's background model. The models the entries name are ${models:-none}. Name the right one with --bg-model and run the check again"
    v_bg=UNMEASURED
  elif [ "${bg_with_id:-0}" -gt 0 ]; then
    v_bg=FAIL
    fact "background-none" FAIL "the background model ${BG_MODEL} made ${bg} call(s) and ${bg_with_id} of them carry an agent_id — $(offenders bg_with_id)$(offenders_more): a background call counted as a sub-agent would make the distinct-agent count wrong, which is U4 answered no. The gateway counts every call that carries an id (internal/gateway/ledger.go), so the fix is a decision about the fan-out stop and not something this check does"
  else
    v_bg=PASS
    fact "background-none" PASS "the background model ${BG_MODEL} made ${bg} call(s) and not one of them carries an agent_id: a background call is no sub-agent and the gateway's count does not include it. The models the entries name are ${models:-none}"
  fi

  # 4a. A call refused before it reserved still logs its agent_id, and the count
  # is of the sub-agents whose calls the stage admitted.
  if [ "${refused_only:-0}" -eq 0 ]; then
    v_ref=PASS
    fact "refused-ids" PASS "no agent id appears on refused entries alone (0), over ${refused_entries:-0} refused entr$([ "${refused_entries:-0}" = 1 ] && echo y || echo ies) — an entry whose settled is zero and whose status is not 200 (a halt, a pin violation, an upstream error): every sub-agent the gateway saw had at least one call admitted"
  else
    v_ref=FAIL
    fact "refused-ids" FAIL "${refused_only} agent id(s) appear on refused entries only, over ${refused_entries:-0} refused entr$([ "${refused_entries:-0}" = 1 ] && echo y || echo ies) — $(offenders refused_only)$(offenders_more): a sub-agent whose every call was refused is counted by the gateway and settled nothing, so the distinct-agent count is not only of the sub-agents whose calls the stage admitted"
  fi

  # 4b. The record's own stage agents, recorded BESIDE the log's distinct count.
  local rec rec_n=0
  rec=$(printf '%s\n' "$DIAG" | sed -n 's/^stage=//p' | awk -F'|' '$2 != "-" { printf "%s%s=%s", (n++ ? ", " : ""), $1, $2 }')
  rec_n=$(printf '%s\n' "$DIAG" | sed -n 's/^stage=//p' | awk -F'|' '$2 != "-"' | grep -c . || true)
  if [ "${rec_n:-0}" -eq 0 ]; then
    fact "record-agents" INFO "the run's record names no stage agents (stages[].agents is absent on every stage): that field is the relay's count of the sub-agent TOOL CALLS a stage's stream carried, a DIFFERENT figure from the log's ${distinct} distinct agent ids, and it is absent rather than zero when nobody counted. The two are named beside each other and never compared"
  else
    fact "record-agents" INFO "the run's record names ${rec} — the relay's count of the sub-agent TOOL CALLS each stage's stream carried, a DIFFERENT figure from the log's ${distinct} distinct agent ids, which is never recorded (schemas/result.schema.json says so in as many words). The two are named beside each other and never compared, and no row here reads one against the other"
  fi

  # The verdict, and the one sentence the owner pastes back.
  local verdict why=""
  if [ "$v_sub" = FAIL ]; then why="${why}${why:+; }a sub-agent's id spans more than one session, or its session is one an entry with an empty agent_id also names"; fi
  if [ "$v_bg" = FAIL ]; then why="${why}${why:+; }the background model's calls carry an agent_id"; fi
  if [ "$v_ref" = FAIL ]; then why="${why}${why:+; }an agent id appears on refused entries alone"; fi
  if [ -n "$why" ]; then
    verdict=FAIL
    fact u4 FAIL "a distinct-agent count is NOT a sub-agent count for ${RUN}: ${why}. This is a finding for T7.2 and not something this check fixes: the plan makes T7.2 wait for this answer, so what the fan-out stop counts — or whether it counts at all — is the owner's decision before T7.2 merges"
  elif [ "$v_bg" = UNMEASURED ]; then
    verdict=UNMEASURED
    unmeasured u4 "U4's first two parts hold for ${RUN} — no entry with an empty agent_id names a session a sub-agent's entry names, and the ${distinct} distinct non-empty ids are sub-agents — but the background model's row could not be taken, so U4 is not answered whole. The background-none row above says why, and what makes it measurable: a background of the repository's own (agent.models.background, a model no stage runs) is the one that separates a background call from a stage's"
  else
    verdict=PASS
    fact u4 PASS "a distinct-agent count IS a sub-agent count for ${RUN}: the main session's entries carry an empty agent_id and name no session a sub-agent's entry names, the ${distinct} distinct non-empty ids are one sub-agent each (no id spans two sessions and no sub-agent shares a session with an entry that carries an empty agent_id), $(if [ "$v_bg" = PASS ]; then echo "the background model ${BG_MODEL}'s ${bg} call(s) carry none"; else echo "the background model was not named so its row was not taken (the models: line above is where its name is read)"; fi), and no agent id appears on refused entries alone. T7.2's fan-out stop rests on the reading it was written against"
  fi

  local bg_sentence rec_sentence u4_sentence
  case "$v_bg" in
  INFO) bg_sentence="the background model not named, so its count was not taken (the models: line is where its name is read)" ;;
  UNMEASURED) bg_sentence="the background model named (${BG_MODEL}) not counted, so its row was not taken (the background-none row above says why)" ;;
  *) bg_sentence="${bg} background-model entr$([ "${bg:-0}" = 1 ] && echo y || echo ies), ${bg_with_id} carrying an id" ;;
  esac
  if [ "${rec_n:-0}" -eq 0 ]; then rec_sentence="the record's own stage agents read none"; else rec_sentence="the record's own stage agents read ${rec}"; fi
  case "$verdict" in
  PASS) u4_sentence="a distinct-agent count IS a sub-agent count" ;;
  FAIL) u4_sentence="a distinct-agent count is NOT a sub-agent count" ;;
  *) u4_sentence="not answered whole — a row above was not taken" ;;
  esac
  fact "paste-back" PASS "Check 139 — ${RUN}${REPO:+ (${REPO})}, $(date -u +%Y-%m-%d): ${ENTRIES} model call entries, ${EMPTY} with an empty agent_id, ${distinct} distinct agent ids (${multi} spanning more than one session, ${shared} sharing a session with an entry that carries an empty agent_id), ${bg_sentence}, ${refused_only} seen only on refused entries; ${rec_sentence}; U4: ${verdict} — ${u4_sentence}. Read with ${FUGARO_VERSION:-a fugaro build} on $(date -u +%Y-%m-%d), no agent ID and no session ID printed"
}

# -------------------------------------------------------------------- the plan
plan() {
  local key
  say "CHECK 139 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [network, read] the guards: fugaro doctor --project ${INSTALLATION} --all (it reads the installation's config, policy and billing, so this is the dry run's one network read) and fugaro version"
  if [ -n "$RUN" ]; then
    say "  [network, read] fugaro diagnose ${RUN} --project ${INSTALLATION} --json — the run's own record: its execution, its outcome and its stages' agents"
  else
    say "  [network, read] fugaro runs ls --all --project ${INSTALLATION} --json --since=${SINCE} --limit=${SCAN} — the newest settled runs, to find one whose budget was on"
    say "  [network, read] up to ${MAX_CANDIDATES} of their executions' entries, one 'gcloud logging read' each, until one carries a non-empty agent_id"
  fi
  say "  [network, read] gcloud logging read 'resource.type=\"cloud_run_job\" AND resource.labels.location=\"<region>\" AND resource.labels.job_name=\"<job>\" AND labels.\"run.googleapis.com/execution_name\"=\"<name>\" AND jsonPayload.message=\"model call\"' --limit=${LIMIT} --freshness=${FRESHNESS} --format=json $([ -n "$LOG_VIEW" ] && echo "--resource-names=${LOG_VIEW}" || echo "--project=${P}") — the entries every row is read from (the filter is the one internal/backend/gcp/logs.go's executionFilter builds)"
  say "  [nothing created] no job, no run, no build, no object, no binding, no account and no policy; no model call and no dollar; the scratch directory is removed at the end"
  for key in fugaro-version project-region log-view xtrace agent-session belong run execution \
    entries main-empty subagents-distinct background-none refused-ids record-agents u4 paste-back; do
    fact "$key" INFO "planned: a real run measures this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no listing was read, no run was diagnosed and no log entry was read"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version project-region log-view xtrace agent-session belong dry-run
  run execution entries main-empty subagents-distinct background-none refused-ids
  record-agents u4 paste-back restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 139 RESULT\n' >&3
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
    if printf '%s\n' "$printed" | grep -qx "$key"; then continue; fi
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 139 RESULT END (exit %s)\n' "$status" >&3
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
# Hard-coded, never `gcloud config get-value project`: a check that reads the
# project from the environment can be pointed at another installation by a stray
# config, and this one reads a real installation's log.
INSTALLATION=fugaro
P=fugaro-dev
DRY_RUN_CLI=""
RUN_CLI=""
BG_CLI=""
while [ $# -gt 0 ]; do
  case "$1" in
  --dry-run) DRY_RUN_CLI=1; shift ;;
  --run)
    [ $# -ge 2 ] || { say "--run needs a run id"; exit 2; }
    RUN_CLI=$2
    shift 2
    ;;
  --run=*)
    RUN_CLI=${1#--run=}
    shift
    ;;
  --bg-model)
    [ $# -ge 2 ] || { say "--bg-model needs a model name"; exit 2; }
    BG_CLI=$2
    shift 2
    ;;
  --bg-model=*)
    BG_CLI=${1#--bg-model=}
    shift
    ;;
  -h | --help)
    sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
    exit 0
    ;;
  *)
    say "unknown argument: $1"
    exit 2
    ;;
  esac
done

RUN=${CHECK139_RUN:-$RUN_CLI}
BG_MODEL=${CHECK139_BG_MODEL:-$BG_CLI}
SCAN=${CHECK139_SCAN:-50}
SINCE=${CHECK139_SINCE:-30d}
MAX_CANDIDATES=${CHECK139_MAX_CANDIDATES:-6}
LIMIT=${CHECK139_LIMIT:-5000}
FRESHNESS=${CHECK139_FRESHNESS:-30d}
LOG_VIEW=${CHECK139_LOG_VIEW:-}
LOG_VIEW_SRC=""
LOG_VIEW_CFG=""
DOCTOR_TIMEOUT=${CHECK139_DOCTOR_TIMEOUT_SECONDS:-60}
CLI_TIMEOUT=${CHECK139_CLI_TIMEOUT_SECONDS:-120}
LOG_TIMEOUT=${CHECK139_LOG_TIMEOUT_SECONDS:-120}
DRY_RUN=${CHECK139_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK139_KEEP_SCRATCH:-}
RUN_FROM=""
CALLS_EXEC=""
CALLS_TEXT=""
CALLS=""
DIAG=""
REPO=""
RUN_STATUS=""
RUN_OUTCOME=""
RUN_CREATED=""
EXEC=""
ENTRIES=""
EMPTY=""
DISTINCT=""
MODELS=""
FUGARO_VERSION=""
# The account is read for one thing only: the belong refusal. It is not used to
# run anything — every read below runs as the account gcloud is already using —
# so a value that does not look like one is dropped rather than guessed at.
ACCOUNT=$(gcloud config get-value account 2>/dev/null | head -n 1 || true)
case "$ACCOUNT" in
"" | *@*) ;;
*) ACCOUNT="" ;;
esac

main() {
  need_tools
  [ -n "$RUN" ] || [ "$MAX_CANDIDATES" -gt 0 ] || die "CHECK139_MAX_CANDIDATES is ${MAX_CANDIDATES}: with no --run the check has to read at least one candidate's entries to find a run, so it has to be a positive number"
  scratch_up
  trap on_error ERR
  guard_xtrace
  guard_agent_session
  resolve_log_view
  guard_belong
  guard_project
  FUGARO_VERSION=$(fugaro version 2>/dev/null | head -n 1 || true)
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  fact dry-run INFO "not a dry run: the rows below were measured on a real run, and nothing was written"
  step_run
  if [ "$STOP" = yes ]; then return 0; fi
  step_execution
  if [ "$STOP" = yes ]; then return 0; fi
  step_calls
  if [ "$STOP" = yes ]; then return 0; fi
  step_verdict
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
