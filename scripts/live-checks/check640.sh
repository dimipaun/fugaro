#!/usr/bin/env bash
# Check 640 — the status and spec labels on a REAL pull request, written by the
# installation's own orchestrator daemon, as ONE script YOU run.
#
# The release behind task T7 of docs/forge-labels has the daemon ask the
# installation's credentials service to read and write the labels of the pull
# requests its runs opened: `fugaro:running` while the run is in flight, nothing
# (ready) or `fugaro:failed` once it has ended, and the `spec:<name>` label of
# the plan the work item belongs to (D2437, D2439). The SERVICE performs the
# writes: it mints its own short-lived token, scoped to
# `{pull_requests: write, metadata: read}`, and that token never leaves the
# service (T4, D2439). What only a live installation settles is that the path
# works end to end against a real GitHub repository — which is the two things
# docs/forge-labels' reconciliation leaves open:
#
#   U1  whether the dogfood installation's GitHub App really holds
#       `pull_requests: write`. Nothing in this repository can read an App's
#       permission list; a write that GitHub refuses is the reading, and it is
#       the credentials service's own audit line that carries it.
#   U3  what GitHub does with a label name that carries a colon (`spec:<name>`)
#       when the label is created by an App's installation token. The label's
#       own colour, read back from the repository, is the reading.
#
# The check is READ-ONLY and it launches nothing: with the daemon running on the
# installation and ONE APPROVED TASK already launched, it watches that run's
# pull request through `gh` (the labels and their colours) and the credentials
# service's own audit lines (`gcloud logging read`, purpose `label`), and it
# says row by row what each reading means.
#
# The FIRST premise it reads is the one the plan calls F19 and which may not
# have landed when you run this: the daemon's identity to the credentials
# service — the installation's orchestrator account holding `roles/run.invoker`
# on the service, and the service's own configuration naming that account as
# the orchestrator class it admits. Nothing mints that identity on a tree
# without it, so the daemon builds no label seams at all and no label is ever
# written (internal/cli/orchestrator_labels.go). That fact is read BEFORE
# anything else and, when it is missing, it is reported as ONE named row —
# "the daemon cannot reach the label route: F19" — and the check stops (exit 2)
# rather than failing every row below it.
#
# Usage:      bash scripts/live-checks/check640.sh [--installation <name>] \
#               [--run-id <id>] [--gcp-project <project>] [--region <region>] \
#               [--dry-run]
#             --dry-run  prints the plan and touches nothing.
#             --installation is the installation whose daemon and logs are read
#             (default `fugaro`): a doctor that names another installation, or
#             another GCP project than the hard-coded one, stops the check.
#             --run-id names the run whose pull request is read, for the case
#             where more than one work item of the daemon is in flight: by
#             default the check reads the daemon's own state and takes the ONE
#             item whose phase is `running` with a run id.
#             This script launches nothing: the ONE approved task it needs is
#             queued by scripts/live-checks/check640-queue-probe.sh, which you run
#             first (it asks you to type the project's name to approve the item).
# Needs:      bash, mktemp, python3 (the reader), gh, gcloud, fugaro. It does NOT
#             need a checkout and does NOT need git: nothing here runs git, and
#             no repository is cloned.
# Where:      the installation `fugaro` in the GCP project `fugaro-dev` and the
#             Cloud Run region `us-east5` ONLY — all three are hard-coded in
#             this file and overridable by CHECK640_INSTALLATION,
#             CHECK640_GCP_PROJECT and CHECK640_REGION. It never reads `gcloud
#             config get-value project`, and it refuses `belong` by name as the
#             installation and as the project. Repository names, label names and
#             audit lines are PRINTED, so run it on an installation whose
#             repositories you are happy to see named in a terminal.
# Costs:      NOTHING billable from this check: no run, no model call, no image
#             build, no write of any kind. Every call it makes is a read —
#             `fugaro doctor`, `fugaro orchestrator ls`, `fugaro orchestrator
#             state`, `fugaro runs ls`, `gh pr view`, `gh label list`, `gcloud
#             run services get-iam-policy`, `gcloud run services describe` and
#             `gcloud logging read`. The run it watches is the one YOU launched,
#             and its own spend is the run's.
# Runs:       by YOU. No agent runs any step of it; its own test
#             (scripts/live-checks/check640_test.sh) runs it against fakes and
#             never touches GitHub, gcloud or a Fugaro installation.
# Cleanup:    always, including on a Ctrl-C: its scratch directory is removed
#             from the exit trap, and the `restore` row says whether it really
#             went. Nothing was created, changed or deleted, so there is nothing
#             else to undo.
# Output:     progress on stderr, ONE block on stdout headed CHECK 640 RESULT,
#             to copy back to the coordinator. Its last line is the verdict, and
#             the verdict is the exit status:
#
#               CHECK 640 RESULT: PASS (exit 0)        every criterion passed;
#               CHECK 640 RESULT: FAIL (exit 1)        a criterion was measured
#                                                      and failed;
#               CHECK 640 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                      could measure everything (a
#                                                      guard stopped it, F19 is
#                                                      not in place, a tool is
#                                                      missing, a command was
#                                                      refused, an error ended it);
#               CHECK 640 RESULT: INCOMPLETE (exit 3)  it ran to the end and at
#                                                      least one criterion was
#                                                      NOT settled: a row reading
#                                                      UNVERIFIED.
#
#             ONLY an all-PASS block is a PASS: a check that measured most of its
#             criteria and left one unmeasured is INCOMPLETE, not a pass, and the
#             register's section is read against that word.
#
# NO SECRET IS READ, so none can be printed: this check reads no token, no key
# and no credential value — `gh` and `gcloud` use the credentials already on
# your machine, inside their own processes, and every answer this check reads is
# a policy, a listing, a label or a run record. What the reader prints is a
# fixed vocabulary: a name, a slug, a label, a colour, a status word, a count,
# `present`/`absent` and `yes`/`no`, each clipped to the characters that
# vocabulary allows — an answer that echoed back something else cannot reach the
# block. And no trace: `bash -x` prints every command as it runs, so
# guard_xtrace REFUSES a run whose shell arrived with tracing on.
#
# CHECK640_RUN_SECONDS, CHECK640_RUN_POLL_SECONDS, CHECK640_SETTLE_SECONDS,
# CHECK640_SETTLE_POLL_SECONDS, CHECK640_AUDIT_WAIT_SECONDS,
# CHECK640_AUDIT_POLL_SECONDS, CHECK640_AUDIT_AGE, CHECK640_AUDIT_LIMIT,
# CHECK640_CALL_SECONDS, CHECK640_RUNS_LIMIT, CHECK640_LABEL_LIMIT,
# CHECK640_ORCH_ACCOUNT, CHECK640_INSTALLATION, CHECK640_GCP_PROJECT,
# CHECK640_REGION, CHECK640_RUN_ID and CHECK640_DRY_RUN exist for this script's
# own test and for an owner's own patience; none of them can point a call at
# another host.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had.
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
ABORT_REASON=""
REACHED_END=no
die() { say "$*"; ABORT_REASON=$*; exit 2; }

stop() {
  fact aborted FAIL "$1"
  ABORT_REASON=$1
  owner "stopped: $1"
  exit 2
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|UNVERIFIED "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  case "$(grep -c '^aborted|' "$FACTS_FILE" 2>/dev/null || true)" in
  0) fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the labels (no value is printed here)" ;;
  esac
  owner "stopped: an internal error ended the run (line $line)"
  exit 2
}

# ---------------------------------------------------------------- requirements
need() {
  command -v "$1" >/dev/null 2>&1 || die "Check 640 needs '$1' on PATH and it is not there${2:+ ($2)}"
}
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 640 needs bash"
  need mktemp
  need python3 "the reader that parses the documents and the audit lines is one python3 file"
  need gh "the pull request's own labels and colours are read with gh, which is the one thing this check cannot ask Fugaro for"
  need gcloud "the credentials service's identity and its audit lines are read with gcloud"
  need fugaro
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk640-mine"
SCRATCH_REMOVED=no
PRINTED=""
CLEANUP_RUNNING=no
FACTS_CAPTURED=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk640.XXXXXXXX") || die "Check 640 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 640 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

cleanup() {
  stop_call
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
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
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

finish() {
  local status=$?
  trap - EXIT ERR
  CLEANUP_RUNNING=yes
  cleanup
  local facts=$FACTS_CAPTURED
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its last step (exit ${status}): the rows that read 'not reached' were NOT measured, which is this script ending on an error rather than an answer about the labels"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, so there is nothing to remove"
  else
    # Every file this check writes holds a document it read — a doctor's answer,
    # a listing, a policy, a label list, a run's row — and no credential: no
    # token is read on any path, so none can be in one of them. Their removal is
    # READ, not assumed.
    facts="${facts}"$'\n'"restore|$([ "$SCRATCH_REMOVED" = yes ] && echo PASS || echo FAIL)|the scratch directory $([ "$SCRATCH_REMOVED" = yes ] && echo "is gone" || echo "is STILL THERE, at $SCRATCH — remove it yourself") (its documents: the doctor's answer, the service's policy and revision, the daemon's listing and state, the run's row, the pull request's labels, the repository's label list and the service's audit lines). This check wrote nothing else anywhere: it creates, changes and deletes nothing on the installation, on GitHub or in the project"
  fi
  if [ -z "$PRINTED" ] && [ "$REACHED_END" = yes ] && [ "$status" -eq 0 ]; then
    if printf '%s\n' "$facts" | grep -q '|FAIL|'; then
      status=1
    elif [ -n "$(criteria_unmet "$facts")" ]; then
      status=3
    fi
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# --------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    fact xtrace FAIL "this shell was started with tracing on (-x, or xtrace in SHELLOPTS/BASHOPTS), which would print the commands this check runs into the log you would then paste"
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check640.sh — the check prints its own RESULT block, so a trace is not needed to report a failure"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can carry a command's environment"
}

# guard_agent_session: this check is the owner's. Its rows are about a real pull
# request on the dogfood installation and about the credentials service's own
# record of what was done to it, and the block it prints is meant to be pasted
# back by the person who ran the run it reads — the register's row says no agent
# runs any step of it. Refusing here is what makes the block say that, rather
# than an agent's run reading as the installation's answer.
#
# The loop is the five markers that say an agent, and not a person, is driving
# this shell. The sixth, CLAUDE_CODE_SSE_PORT, says only that this shell sits in
# an editor's terminal, where the person is still the one typing, so it is
# DROPPED rather than refused: refusing on it would lock you out of this check in
# your own editor.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    fact agent-session FAIL "${marker} is set, so this is a coding agent's session: Check 640 is yours to run at your own terminal, against the installation you run the daemon on, and no agent runs any step of it"
    stop "${marker} is set, so this is a coding agent's session: Check 640 is yours to run at your own terminal, against the installation you run the daemon on, and no agent runs any step of it"
  fi
  unset CLAUDE_CODE_SSE_PORT
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like), so this is your own session — the one this check is written for: the run it reads is the run you launched. The editor extension's own marker (CLAUDE_CODE_SSE_PORT) is dropped from this script's environment rather than refused, because it says this shell sits in an editor's terminal and not that an agent is driving it"
}

lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }

# guard_belong: `belong` is the installation this check must never touch — its
# labels are the owner's own work — and a script that can be pointed at it by a
# typo has no business reading anything there. Any value GIVEN to this check that
# names it is refused before the first call is made (die, so nothing is read at
# all); the values it READS are checked in the installation step, which is where
# they first exist, and that check carries the `belong` row.
guard_belong() {
  local what value
  for what in installation gcp-project; do
    case "$what" in
    installation) value=$INSTALLATION ;;
    gcp-project) value=$GCP_PROJECT ;;
    esac
    [ -n "$value" ] || continue
    case "$(lower "$value")" in
    *belong*)
      die "Check 640 refused: '$value' (the $what) names 'belong', the installation this check must never touch. Nothing was read anywhere"
      ;;
    esac
  done
}

# --------------------------------------------------------------- the calls
# last_line FILE: one bounded line of a command's own output, for a block that
# has to say why a step failed without pasting a log.
last_line() {
  local text
  text=$(grep -v '^[[:space:]]*$' "$1" 2>/dev/null | tail -n 1 | tr -d '\r' | tr '\n' ' ' | head -c 240 || true)
  [ -n "$text" ] || text="the command printed nothing"
  printf '%s' "$text"
}

# run_call SECONDS OUT LOG cmd... — one long command with a deadline of its own,
# in a process group of its own so the deadline reaches whatever the call
# started. `timeout(1)` is not used: it is not on macOS, where this check is also
# run.
CALL_PID=""
run_call() { # run_call SECONDS OUT LOG cmd...
  local secs=$1 out=$2 log=$3
  shift 3
  set -m
  "$@" </dev/null >"$out" 2>"$log" &
  CALL_PID=$!
  local waited=0
  while kill -0 "$CALL_PID" 2>/dev/null; do
    if [ "$waited" -ge "$secs" ]; then
      local pid=$CALL_PID
      stop_call
      wait "$pid" 2>/dev/null || true
      return 124
    fi
    sleep 1
    waited=$((waited + 1))
  done
  local status=0
  wait "$CALL_PID" || status=$?
  CALL_PID=""
  return "$status"
}

stop_call() {
  if [ -n "${CALL_PID:-}" ] && kill -0 "$CALL_PID" 2>/dev/null; then
    kill -TERM -- "-$CALL_PID" 2>/dev/null || kill -TERM "$CALL_PID" 2>/dev/null || true
    kill -KILL -- "-$CALL_PID" 2>/dev/null || true
  fi
  CALL_PID=""
}

# ------------------------------------------------------------- the reader
reader() { python3 "$SCRATCH/reader.py" "$@"; }

field_of() { # field_of NAME TEXT — the value of a NAME= line the reader printed
  printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1
}

write_reader() {
  cat >"$SCRATCH/reader.py" <<'READER_EOF'
#!/usr/bin/env python3
"""Check 640's reader: the one place the documents this check reads are parsed.

The shell never parses JSON and never prints a field it did not choose. This
program prints a FIXED vocabulary -- a name, a slug, a label, a colour, a status
word, a count, yes/no -- and every value is clipped to the characters that
vocabulary allows, so an answer that echoes back something else (a token in an
error body, a message a service wrote, a path someone pasted) cannot reach the
check's result block through this.
"""
import hashlib
import json
import re
import sys

# The character classes a printed value may have. A value outside its class is
# dropped WHOLE and replaced by the default: a clip could still leave a fragment
# of something that should not be here.
WORD = re.compile(r"[A-Za-z0-9._:@/+-]{1,120}")
REPO = re.compile(r"[A-Za-z0-9._-]{1,80}/[A-Za-z0-9._-]{1,80}")
LABEL = re.compile(r"[A-Za-z0-9 ._:/+-]{1,50}")
COLOUR = re.compile(r"[0-9A-Fa-f]{6}")
SLUG = re.compile(r"[A-Za-z0-9._-]{1,80}")
PHASE = re.compile(r"[a-z]{1,20}")
RUNID = re.compile(r"[A-Za-z0-9._-]{1,64}")
STATUS = re.compile(r"[a-z_]{1,32}")
SPEC = re.compile(r"[A-Za-z0-9][A-Za-z0-9-]{0,43}")
ACCOUNT = re.compile(r"[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+")
PROJECT = re.compile(r"[a-z][a-z0-9-]{4,28}[a-z0-9]")
STATE = re.compile(r"[A-Za-z]{1,16}")


def clean(rx, value, default="-"):
    if not isinstance(value, str):
        return default
    value = value.strip()
    return value if rx.fullmatch(value) else default


def field(name, value):
    print("%s=%s" % (name, value))


def die(message):
    print(message, file=sys.stderr)
    raise SystemExit(1)


def load(path):
    try:
        with open(path, "r", encoding="utf-8") as fh:
            return json.load(fh)
    except (OSError, ValueError) as err:
        die("the document %s could not be read: %s" % (path, err))


def slug_of(repo):
    """task.Slug's own value for a GitHub repository (internal/task): the
    canonical owner/name (A-Z folded to a-z, a trailing .git dropped) made
    readable (every run of characters outside a-z0-9 replaced by one dash, the
    ends trimmed, at most 46 characters), then a dash and the first sixteen hex
    characters of the SHA-256 of `github\\x00<canonical>`. The audit line names
    the repository this way and nothing else does, so the check has to compute it
    to read its own repository's lines."""
    canonical = "".join(chr(ord(ch) + 32) if "A" <= ch <= "Z" else ch for ch in repo)
    if canonical.endswith(".git"):
        canonical = canonical[:-4]
    readable = re.sub(r"[^a-z0-9]+", "-", canonical).strip("-")
    if len(readable) > 46:
        readable = readable[:46].rstrip("-")
    if not readable:
        readable = "repo"
    digest = hashlib.sha256(("github\x00" + canonical).encode()).hexdigest()
    return "%s-%s" % (readable, digest[:16])


def doctor(path):
    doc = load(path)
    project = doc.get("project") or {}
    field("project", clean(WORD, project.get("name")))
    field("gcp_project", clean(PROJECT, project.get("gcp_project")))
    field("region", clean(WORD, project.get("region")))


def ls(path, want):
    doc = load(path)
    entries = doc.get("orchestrators") or []
    found = None
    for e in entries:
        if isinstance(e, dict) and str(e.get("project", "")).lower() == want.lower():
            found = e
            break
    if found is None:
        field("entry", "no")
        field("kind", "-")
        field("stale", "-")
        field("lease", "-")
        field("orchestrators", str(len(entries)))
        return
    field("entry", "yes")
    field("kind", clean(WORD, found.get("kind")))
    field("stale", "yes" if found.get("cache_stale") else "no")
    field("lease", clean(WORD, found.get("lease_holder"), "-"))
    field("orchestrators", str(len(entries)))


def state(path):
    """The daemon's work items, read from the `items` array of the state document.

    `fugaro orchestrator state --json` carries every item there (id, phase,
    run_id, attempts, repo, spec). The `brief` beside it is a capped summary and
    is never parsed: an installation holding more items than it lists would
    otherwise look as if it held none in flight."""
    doc = load(path)
    items = doc.get("items")
    if not isinstance(items, list):
        items = []
    count = 0
    for it in items:
        if not isinstance(it, dict):
            continue
        count += 1
        run = it.get("run_id")
        spec = it.get("spec")
        print("ITEM %s|%s|%s|%s|%s" % (
            clean(WORD, it.get("id")), clean(PHASE, it.get("phase")),
            clean(REPO, it.get("repo")), clean(RUNID, run if run else "-"),
            clean(SPEC, spec if spec else "-")))
    field("items", str(count))
    field("array", "yes" if isinstance(doc.get("items"), list) else "no")


def runs(path, run_id):
    doc = load(path)
    rows = doc.get("runs") or []
    row = None
    for r in rows:
        if isinstance(r, dict) and r.get("run_id") == run_id:
            row = r
            break
    if row is None:
        field("found", "no")
        field("rows", str(len(rows)))
        return
    field("found", "yes")
    field("rows", str(len(rows)))
    field("repo", clean(REPO, row.get("repo")))
    field("status", clean(STATUS, row.get("status"), "-"))
    field("outcome", clean(STATUS, row.get("outcome"), "-"))
    field("pr_state", clean(STATUS, row.get("pr_state"), "-"))
    field("terminal", "yes" if row.get("terminal") else "no")
    pr = row.get("pr")
    field("pr", str(pr) if isinstance(pr, int) and pr > 0 else "0")
    field("pushed", "yes" if row.get("pushed") else "no")


def pr(path):
    doc = load(path)
    # github spells it in capitals (OPEN, CLOSED, MERGED) and this check compares
    # it with its own lower-case words.
    state_value = clean(STATE, doc.get("state"), "-").lower()
    field("state", state_value)
    names = []
    for entry in doc.get("labels") or []:
        name = entry.get("name") if isinstance(entry, dict) else entry
        name = clean(LABEL, name, "")
        if name:
            names.append(name)
    field("labels", ",".join(names) if names else "-")
    field("number", str(doc.get("number")) if isinstance(doc.get("number"), int) else "-")


def labels(path):
    doc = load(path)
    rows = doc if isinstance(doc, list) else (doc.get("labels") or [])
    count = 0
    for entry in rows:
        if not isinstance(entry, dict):
            continue
        name = clean(LABEL, entry.get("name"), "")
        colour = clean(COLOUR, entry.get("color"), "")
        if not name:
            continue
        count += 1
        # Folded on the way out, because GitHub's own label names are unique
        # case-insensitively and this check compares them by name.
        print("L %s|%s" % (name.lower(), colour.lower()))
    field("count", str(count))


def iam(path, account):
    """The service's own IAM policy, read for the one member the daemon's
    identity is: the installation's orchestrator account holding
    roles/run.invoker. Nothing else on the policy is printed."""
    doc = load(path)
    member = "serviceAccount:" + account
    invoker = "no"
    for b in doc.get("bindings") or []:
        if not isinstance(b, dict):
            continue
        if b.get("role") != "roles/run.invoker":
            continue
        if member in (b.get("members") or []):
            invoker = "yes"
    field("account", clean(ACCOUNT, account, "-"))
    field("invoker", invoker)


def walk_env(node, out):
    if isinstance(node, dict):
        if isinstance(node.get("name"), str) and isinstance(node.get("value"), str):
            out.append((node["name"], node["value"]))
        for value in node.values():
            walk_env(value, out)
    elif isinstance(node, list):
        for value in node:
            walk_env(value, out)


def service(path, account):
    """The service's revision, read for the ONE thing that makes the daemon's
    class real: an environment variable whose value names the account. Only the
    variable's NAME is printed — an environment value can be a credential
    reference, and this check prints no value."""
    doc = load(path)
    pairs = []
    walk_env(doc, pairs)
    names = []
    for name, value in pairs:
        if account.lower() in value.lower():
            cleaned = clean(WORD, name, "")
            if cleaned:
                names.append(cleaned)
    field("env-account", "yes" if names else "no")
    field("env-names", ",".join(sorted(names)) if names else "-")
    field("env-count", str(len(pairs)))


def audit(path, slug):
    """The credentials service's own label lines (internal/credsvc's audit
    writer: message, repository, caller, credential, purpose, status). A label
    line carries the daemon as its caller, the repository the act was for and
    the status of the act — and NEITHER a run nor a job, because the daemon
    holds no run identity.

    What is printed is a count, the repository slugs, the caller names and the
    status words, each clipped: a line's free text never is."""
    doc = load(path)
    if not isinstance(doc, list):
        die("the audit answer is not a list of entries")
    total = 0
    matched = 0
    refused = 0
    callers = set()
    statuses = {}
    repos = set()
    for entry in doc:
        payload = entry.get("jsonPayload") if isinstance(entry, dict) else None
        if not isinstance(payload, dict):
            continue
        if payload.get("purpose") != "label":
            continue
        total += 1
        repo = clean(SLUG, payload.get("repository"), "-")
        repos.add(repo)
        if repo != slug:
            continue
        matched += 1
        caller = clean(ACCOUNT, payload.get("caller"), "-")
        callers.add(caller)
        status = clean(STATUS, payload.get("status"), "-")
        statuses[status] = statuses.get(status, 0) + 1
        # Every act the service performs is written with the status `ok`
        # (internal/credsvc's StatusOK); anything else is a refusal, and the
        # count of them is what the check's own row turns on.
        if status != "ok":
            refused += 1
    field("refused", str(refused))
    field("lines", str(total))
    field("repo-lines", str(matched))
    field("callers", ",".join(sorted(callers)) if callers else "-")
    field("statuses", ",".join("%s:%d" % (k, statuses[k]) for k in sorted(statuses)) if statuses else "-")
    field("repos", ",".join(sorted(repos)) if repos else "-")


def main(argv):
    if len(argv) < 2:
        die("usage: reader.py <doctor|ls|state|runs|pr|labels|iam|service|audit|slug> ...")
    what = argv[1]
    if what == "doctor":
        doctor(argv[2])
    elif what == "ls":
        ls(argv[2], argv[3])
    elif what == "state":
        state(argv[2])
    elif what == "runs":
        runs(argv[2], argv[3])
    elif what == "pr":
        pr(argv[2])
    elif what == "labels":
        labels(argv[2])
    elif what == "iam":
        iam(argv[2], argv[3])
    elif what == "service":
        service(argv[2], argv[3])
    elif what == "audit":
        audit(argv[2], argv[3])
    elif what == "slug":
        print("slug=%s" % clean(SLUG, slug_of(argv[2])))
    else:
        die("the reader has no subcommand '%s'" % what)


main(sys.argv)
READER_EOF
}

# ------------------------------------------------------------------ the steps
# Every fact's detail says what was MEASURED and what it means, and a step that
# could not be measured says so rather than reading as a pass.

step_installation() {
  local rc=0 status=ok
  # doctor exits 1 whenever any of its checks is not ok, warnings included, and
  # still prints its document; only a timeout or another status is no answer.
  run_call "$CALL_SECONDS" "$SCRATCH/doctor.json" "$SCRATCH/doctor.err" \
    fugaro doctor --project "$INSTALLATION" --json || rc=$?
  if [ "$rc" = 0 ] || [ "$rc" = 1 ]; then status=ok; else status=failed; fi
  if [ "$status" != ok ]; then
    stop "fugaro doctor --project ${INSTALLATION} failed (exit ${rc}): $(last_line "$SCRATCH/doctor.err"). This check reads the installation's own project before it reads anything else, and it needs the installation named on this machine with usable credentials"
  fi
  local facts
  facts=$(reader doctor "$SCRATCH/doctor.json" 2>/dev/null || true)
  PROJECT_NAME=$(field_of project "$facts")
  GCP_PROJECT_SEEN=$(field_of gcp_project "$facts")
  REGION_SEEN=$(field_of region "$facts")
  # The values this check was GIVEN were refused in the argument parse, before
  # any call; the ones it has now READ are refused here, and this is the row that
  # says so.
  case "$(lower "${PROJECT_NAME}|${GCP_PROJECT_SEEN}")" in
  *belong*)
    fact belong FAIL "the installation this check READ from fugaro doctor --project ${INSTALLATION} names 'belong' (installation '${PROJECT_NAME}', GCP project '${GCP_PROJECT_SEEN}'): this check is never run against belong, whatever it was asked for. Nothing was read on it"
    stop "the installation read from the local config names belong, which this check never touches"
    ;;
  esac
  if [ "$(lower "${PROJECT_NAME:-}")" != "$(lower "$INSTALLATION")" ]; then
    stop "the local config selected by --project ${INSTALLATION} is the installation '${PROJECT_NAME:-none}': this check reads the labels of the installation you named and no other, so it stops rather than reading another installation's daemon, runs and logs"
  fi
  # The GCP project and the region are hard-coded (never `gcloud config
  # get-value project`): a check that took the project from the environment
  # could be pointed at another installation's Cloud Logging by a stray
  # variable, and the audit lines it reads are the credentials service's.
  if [ "$(lower "${GCP_PROJECT_SEEN:-}")" != "$(lower "$GCP_PROJECT")" ]; then
    stop "the installation '${PROJECT_NAME}' lives in GCP project '${GCP_PROJECT_SEEN:-none}' and this check reads the dogfood installation in '${GCP_PROJECT}' only: 'belong' and every other installation are refused by name. Set CHECK640_GCP_PROJECT (and CHECK640_INSTALLATION) if you really mean another one, or point --installation at the dogfood installation"
  fi
  fact belong PASS "nothing this check was given or has read names belong: --installation ${INSTALLATION} and --gcp-project ${GCP_PROJECT} as given, and the installation '${PROJECT_NAME}' in GCP project ${GCP_PROJECT_SEEN} as fugaro doctor --project ${INSTALLATION} reports it. belong is the owner's own installation and no script of this register reads it"
  fact installation PASS "installation '${PROJECT_NAME}' in GCP project ${GCP_PROJECT_SEEN}, region ${REGION_SEEN:-unset} (the check reads region ${REGION}), read from fugaro doctor --project ${INSTALLATION} at $(date -u +%Y-%m-%dT%H:%M:%SZ). The credentials service is ${SERVICE_NAME} in that project and region, and the daemon's account is ${ORCH_ACCOUNT}"
}

# step_identity — F19, read FIRST (the plan says so, and the register's
# prerequisite section repeats it): the daemon has no identity to the
# credentials service on a tree without that work, so the daemon builds no label
# seams at all, writes no label, and every row below this one would fail for a
# reason that is not about labels. The two halves read here are the two a check
# outside the daemon can see: the invoker grant (Terraform) and the account the
# service's own revision admits as its orchestrator class.
step_identity() {
  local rc=0
  IDENTITY_MISSING=""
  run_call "$CALL_SECONDS" "$SCRATCH/policy.json" "$SCRATCH/policy.err" \
    gcloud run services get-iam-policy "$SERVICE_NAME" --project "$GCP_PROJECT" --region "$REGION" --format=json || rc=$?
  if [ "$rc" -ne 0 ]; then
    fact identity UNVERIFIED "the credentials service ${SERVICE_NAME}'s own IAM policy could not be read (gcloud exited ${rc}: $(last_line "$SCRATCH/policy.err")), so whether the daemon can reach the label route is UNKNOWN here and nothing below was measured. This check needs an account that may read Cloud Run IAM on ${GCP_PROJECT} (roles/run.viewer or better). Run 'gcloud run services get-iam-policy ${SERVICE_NAME} --project ${GCP_PROJECT} --region ${REGION}' yourself and read the ${ORCH_ACCOUNT} membership"
    stop "the credentials service's IAM policy could not be read, so the F19 premise was not measured"
  fi
  local facts
  facts=$(reader iam "$SCRATCH/policy.json" "$ORCH_ACCOUNT" 2>/dev/null || true)
  INVOKER=$(field_of invoker "$facts")
  rc=0
  run_call "$CALL_SECONDS" "$SCRATCH/service.json" "$SCRATCH/service.err" \
    gcloud run services describe "$SERVICE_NAME" --project "$GCP_PROJECT" --region "$REGION" --format=json || rc=$?
  if [ "$rc" -ne 0 ]; then
    fact identity UNVERIFIED "the credentials service ${SERVICE_NAME} could not be described (gcloud exited ${rc}: $(last_line "$SCRATCH/service.err")), so whether its own configuration admits ${ORCH_ACCOUNT} as the orchestrator class is UNKNOWN here and nothing below was measured"
    stop "the credentials service could not be described, so the F19 premise was not measured"
  fi
  local sfacts
  sfacts=$(reader service "$SCRATCH/service.json" "$ORCH_ACCOUNT" 2>/dev/null || true)
  ENV_ACCOUNT=$(field_of env-account "$sfacts")
  ENV_NAMES=$(field_of env-names "$sfacts")
  if [ "$INVOKER" = yes ] && [ "$ENV_ACCOUNT" = yes ]; then
    fact identity PASS "the daemon's identity to the credentials service is IN PLACE: ${ORCH_ACCOUNT} is a member of a roles/run.invoker binding on ${SERVICE_NAME} (its own IAM policy), and the service's revision names that account in ${ENV_NAMES} (the orchestrator class the route's verifier admits). F19 is not this check's prerequisite to report: the route is reachable from the daemon, and the rows below are the reading of what it does with it. The check does not read which identity the daemon mints with at run time — the invoker grant and the class are the two halves visible from outside, and the audit row below is where a token that is refused would show"
    return 0
  fi
  local why=""
  [ "$INVOKER" = yes ] || why="${why}${ORCH_ACCOUNT} is NOT a member of any roles/run.invoker binding on ${SERVICE_NAME}; "
  [ "$ENV_ACCOUNT" = yes ] || why="${why}the service's own revision names ${ORCH_ACCOUNT} in NO environment variable (the orchestrator class is unset, so the route refuses every caller before any list is consulted — F19, F26); "
  fact identity FAIL "the daemon cannot reach the label route: F19 — ${why}the daemon account, the invoker grant (Terraform) and the variable naming the service are the merge mint's unfinished wiring (docs/forge-labels' prerequisite, E10b of docs/outer-loop-escalation). Until it lands the daemon builds no label seams at all (internal/cli/orchestrator_labels.go), so NO label is written and the rows below this one are not a reading of anything. This check stops here rather than failing them one by one: with the grant and the class in place, run it again — and if the labels then do not appear, the failure is the route's and the rows below say which part"
  stop "the daemon has no identity to the credentials service (F19), so no label can be written and nothing below was measured"
}

step_daemon() {
  local rc=0
  run_call "$CALL_SECONDS" "$SCRATCH/ls.json" "$SCRATCH/ls.err" \
    fugaro orchestrator ls --project "$INSTALLATION" --json || rc=$?
  if [ "$rc" -ne 0 ]; then
    fact daemon FAIL "fugaro orchestrator ls --project ${INSTALLATION} --json exited ${rc}, so whether the daemon is running on this machine could not be read: $(last_line "$SCRATCH/ls.err")"
    stop "the orchestrator listing could not be read, so nothing below was measured"
  fi
  local facts
  facts=$(reader ls "$SCRATCH/ls.json" "$INSTALLATION" 2>/dev/null || true)
  DAEMON_ENTRY=$(field_of entry "$facts")
  DAEMON_KIND=$(field_of kind "$facts")
  DAEMON_STALE=$(field_of stale "$facts")
  DAEMON_LEASE=$(field_of lease "$facts")
  if [ "$DAEMON_ENTRY" != yes ]; then
    fact daemon FAIL "the installation '${INSTALLATION}' has no orchestrator at all: fugaro orchestrator ls --project ${INSTALLATION} --json lists none held under that id, so no daemon launched the run this check reads"
    stop "no orchestrator is recorded for ${INSTALLATION}, so nothing below was measured"
  fi
  if [ "$DAEMON_KIND" != local ] || [ "$DAEMON_STALE" != no ]; then
    fact daemon FAIL "the orchestrator '${INSTALLATION}' is not one THIS machine runs and is beating: kind=${DAEMON_KIND:-unread} (want local, which is what a machine whose daemon keeps a cached brief reports), cache_stale=${DAEMON_STALE:-unread}, lease holder ${DAEMON_LEASE:-nobody}. This check reads the labels a daemon writes at its ticks, so it has to run on the machine the daemon runs on; start it with 'fugaro orchestrator run --project ${INSTALLATION}' and run the check again"
    stop "the daemon is not running on this machine, so nothing below was measured"
  fi
  if [ "$DAEMON_LEASE" = "-" ] || [ -z "$DAEMON_LEASE" ]; then
    fact daemon FAIL "the orchestrator '${INSTALLATION}' has a cached brief on this machine but its lease node names NO holder, so its last beat is stale and nothing is driving it: a daemon that is not beating writes no labels"
    stop "the daemon holds no lease, so nothing below was measured"
  fi
  fact daemon PASS "the orchestrator '${INSTALLATION}' is local to this machine, its cached brief is not stale, and the lease is held by ${DAEMON_LEASE} (fugaro orchestrator ls --project ${INSTALLATION} --json): a daemon really is ticking here, which is what makes the labels below readable at all"
}

step_item() {
  local rc=0
  run_call "$CALL_SECONDS" "$SCRATCH/state.json" "$SCRATCH/state.err" \
    fugaro orchestrator state --project "$INSTALLATION" --json || rc=$?
  if [ "$rc" -ne 0 ]; then
    fact item FAIL "fugaro orchestrator state --project ${INSTALLATION} --json exited ${rc}, so the daemon's own state could not be read and which task it launched is unknown: $(last_line "$SCRATCH/state.err")"
    stop "the daemon's state could not be read, so nothing below was measured"
  fi
  local facts
  facts=$(reader state "$SCRATCH/state.json" 2>/dev/null || true)
  ITEMS=$(field_of items "$facts")
  # The items of the daemon's state, as `id|phase|repo|run|spec` lines. The item
  # this check reads is the one in flight: phase running with a run id recorded
  # on its attempt (the run id the daemon launched, O3).
  local running="" count=0 line id phase repo run spec
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    id=${line%%|*}
    line=${line#*|}
    phase=${line%%|*}
    line=${line#*|}
    repo=${line%%|*}
    line=${line#*|}
    run=${line%%|*}
    spec=${line##*|}
    if [ "$phase" = running ] && [ -n "$run" ] && [ "$run" != "-" ]; then
      if [ -n "$RUN_ID_WANT" ]; then
        [ "$run" = "$RUN_ID_WANT" ] || continue
      fi
      running="${running}${id}|${phase}|${repo}|${run}|${spec}"$'\n'
      count=$((count + 1))
    fi
  done < <(printf '%s\n' "$facts" | sed -n 's/^ITEM //p')

  if [ "$count" -eq 0 ]; then
    local ended="" eline eid ephase erun
    while IFS= read -r eline; do
      [ -n "$eline" ] || continue
      eid=${eline%%|*}
      eline=${eline#*|}
      ephase=${eline%%|*}
      eline=${eline#*|}
      eline=${eline#*|}
      erun=${eline%%|*}
      [ "$erun" != "-" ] && [ "$ephase" != running ] || continue
      if [ -n "$RUN_ID_WANT" ]; then
        [ "$erun" = "$RUN_ID_WANT" ] || continue
      fi
      ended="${ended}${eid} is phase ${ephase} (run ${erun}); "
    done < <(printf '%s\n' "$facts" | sed -n 's/^ITEM //p')
    if [ -n "$ended" ]; then
      fact item FAIL "the item has already ended, so the running label can no longer be measured: ${ended}the daemon's state carries ${ITEMS:-unread} item(s). Only the settled rows are measurable for that run id: the settled label (fugaro:failed after a failed run, none after a succeeded one), the spec label and the colours, read from the pull request as it stands now. Run scripts/live-checks/check640-queue-probe.sh with --suffix <word> for a fresh item and run this check while that item is running"
      stop "the task ended before the check read it, so the label written while it was running was not measured"
    fi
    if [ -n "$RUN_ID_WANT" ]; then
      fact item FAIL "no work item of the daemon '${INSTALLATION}' is in phase running with the run id ${RUN_ID_WANT} (its state carries ${ITEMS:-unread} item(s)). Either that run has ended — in which case the labels to read are the settled ones and this check cannot measure the running label any more — or the id is not one of this daemon's own"
    else
      fact item FAIL "the daemon '${INSTALLATION}' has NO work item in phase running with a run id (its state carries ${ITEMS:-unread} item(s)): this check reads the labels of the pull request of a run the daemon launched, and there is none. Approve one task in the daemon's own backlog, let it launch (fugaro orchestrator state shows the item moving to running with a run id), and run this check again while it is in flight"
    fi
    stop "no approved task of the daemon is in flight, so the labels on a live run's pull request were not measured"
  fi
  if [ "$count" -gt 1 ]; then
    fact item FAIL "${count} work items of the daemon '${INSTALLATION}' are in phase running with a run id, and this check reads ONE pull request: $(printf '%s' "$running" | tr '\n' ' '). Name the one to read with --run-id <id> (the ids above) and run it again"
    stop "more than one approved task is in flight, so which pull request to read is ambiguous"
  fi
  ITEM_ID=${running%%|*}
  local rest=${running#*|}
  ITEM_PHASE=${rest%%|*}
  rest=${rest#*|}
  ITEM_REPO=${rest%%|*}
  rest=${rest#*|}
  ITEM_RUN=${rest%%|*}
  ITEM_SPEC=${rest%%$'\n'*}
  ITEM_SPEC=${ITEM_SPEC##*|}
  if [ -n "$RUN_ID_WANT" ] && [ "$ITEM_RUN" != "$RUN_ID_WANT" ]; then
    fact item FAIL "the daemon's item reads run ${ITEM_RUN} and --run-id named ${RUN_ID_WANT}"
    stop "the item's run id and --run-id disagree"
  fi
  fact item PASS "the daemon is carrying ONE task in flight and this check read it from the daemon's own state: item ${ITEM_ID} in phase running on ${ITEM_REPO}, its attempt's run is ${ITEM_RUN}$([ -n "$ITEM_SPEC" ] && echo ", its spec is ${ITEM_SPEC}" || echo ", and the item carries NO spec"). The run id is the daemon's own record of what it launched (O3), and the spec — when the item carries one — is what its pull request's spec: label is built from (D2438)"
}

# read_pr NUMBER — one `gh pr view` for the pull request, into PR_LABELS and
# PR_STATE. Answers non-zero when gh did not answer, so the caller can say
# whether it measured nothing or measured an absence.
read_pr() {
  local n=$1 rc=0
  run_call "$CALL_SECONDS" "$SCRATCH/pr.json" "$SCRATCH/gh.err" \
    gh pr view "$n" --repo "$REPO_SEEN" --json labels,state,number,url || rc=$?
  PR_GH_RC=$rc
  PR_LABELS=""
  PR_STATE=""
  if [ "$rc" -ne 0 ] || [ ! -s "$SCRATCH/pr.json" ]; then return 1; fi
  local facts
  facts=$(reader pr "$SCRATCH/pr.json" 2>/dev/null || true)
  PR_LABELS=$(field_of labels "$facts")
  PR_STATE=$(field_of state "$facts")
  [ -n "$PR_LABELS" ] || PR_LABELS="-"
  return 0
}

run_row() { # run_row — the run's own row, read into ROW_*; answers non-zero when the listing did not answer
  local rc=0
  run_call "$CALL_SECONDS" "$SCRATCH/runs.json" "$SCRATCH/runs.err" \
    fugaro runs ls --project "$INSTALLATION" --json --limit "$RUNS_LIMIT" || rc=$?
  if [ "$rc" -ne 0 ]; then
    RUNS_ERR=$(last_line "$SCRATCH/runs.err")
    return 1
  fi
  local facts
  facts=$(reader runs "$SCRATCH/runs.json" "$ITEM_RUN" 2>/dev/null || true)
  ROW_FOUND=$(field_of found "$facts")
  ROW_ROWS=$(field_of rows "$facts")
  ROW_REPO=$(field_of repo "$facts")
  ROW_STATUS=$(field_of status "$facts")
  ROW_OUTCOME=$(field_of outcome "$facts")
  ROW_TERMINAL=$(field_of terminal "$facts")
  ROW_PR=$(field_of pr "$facts")
  ROW_PUSHED=$(field_of pushed "$facts")
  ROW_PR_STATE=$(field_of pr_state "$facts")
  case "${ROW_PR:-}" in '' | *[!0-9]*) ROW_PR=0 ;; esac
  return 0
}

# status_labels_of LIST — the subset of the pull request's labels that is the
# two status names, one per word.
status_labels_of() {
  local l out=""
  for l in $(printf '%s' "$1" | tr ',' ' '); do
    case "$l" in
    fugaro:running | fugaro:failed) out="${out}${out:+ }$l" ;;
    esac
  done
  printf '%s' "$out"
}

has_label() { # has_label LIST NAME
  case ",$1," in
  *",$2,"*) return 0 ;;
  esac
  return 1
}

# step_run — the run's own row, the running label while it is in flight, the
# settled labels once it ended, and the spec label. This is the one step that
# WAITS: the run the daemon launched is still going when the check starts, and
# the labels it reads are written at the daemon's ticks (its default interval is
# 30s), so a reading taken once would be a reading of whatever the labels
# happened to be at that instant.
step_run() {
  local waited=0 end="" saw_live=no saw_running=no pr_seen=no pr_ok=0 pr_bad=0
  RUNS_ERR=""
  while :; do
    if ! run_row; then
      fact run FAIL "fugaro runs ls --project ${INSTALLATION} --json --limit ${RUNS_LIMIT} did not answer while waiting for the run ${ITEM_RUN}: ${RUNS_ERR:-it printed nothing}. The run's own row is where the pull request number comes from, so nothing about the labels was measured"
      stop "the run's row could not be read, so nothing below was measured"
    fi
    if [ "$ROW_FOUND" != yes ]; then
      fact run FAIL "the run ${ITEM_RUN} the daemon's own item names is in NO row of fugaro runs ls --project ${INSTALLATION} (the listing answered ${ROW_ROWS:-unread} row(s)). The item's run id is the daemon's record of what it launched (O3); a listing that does not hold it means the run went to another project's bucket, or the item's run id is not one of this installation's runs"
      stop "the daemon's run is not in the listing, so nothing below was measured"
    fi
    # Which repository every `gh` read below names: the run's OWN row (the
    # canonical owner/name the pipeline recorded for the repository the task was
    # for) when it carries one, and the daemon's item otherwise. Neither is
    # taken from the environment, and both are clipped to a repository's own
    # characters before gh sees them.
    if [ -z "$REPO_SEEN" ]; then
      case "$ROW_REPO" in
      '' | '-') REPO_SEEN=$ITEM_REPO ;;
      *) REPO_SEEN=$ROW_REPO ;;
      esac
      case "$REPO_SEEN" in
      '' | '-')
        fact run FAIL "neither the run's own row nor the daemon's item names the repository of the run ${ITEM_RUN}, so there is no repository to read the pull request's labels from: the row reads repo='${ROW_REPO:-none}' and the item's line reads repo='${ITEM_REPO:-none}'. A repository is named by owner and name (github's own spelling) and nothing else here is accepted"
        stop "the run's repository could not be read, so no label was measured"
        ;;
      esac
    fi
    if [ "$ROW_PR" != 0 ]; then
      pr_seen=yes
      if read_pr "$ROW_PR"; then
        pr_ok=$((pr_ok + 1))
        if has_label "$PR_LABELS" fugaro:running; then saw_running=yes; fi
        [ "$ROW_TERMINAL" = yes ] || saw_live=yes
      else
        pr_bad=$((pr_bad + 1))
      fi
    fi
    if [ "$ROW_TERMINAL" = yes ]; then end=terminal; break; fi
    if [ "$waited" -ge "$RUN_SECONDS" ]; then end=timeout; break; fi
    sleep "$RUN_POLL_SECONDS"
    waited=$((waited + RUN_POLL_SECONDS))
  done

  if [ "$ROW_PR" = 0 ]; then
    fact run FAIL "the run ${ITEM_RUN} on ${ROW_REPO:-${REPO_SEEN}} ended ${ROW_STATUS:-unread} (outcome ${ROW_OUTCOME:-none}, pushed=${ROW_PUSHED:-unread}) and left NO pull request (pr=0), so there is no pull request to read the labels on: this check exists for the labels a run's pull request carries, and a run that opened none settles nothing about them. Read the run's own record (fugaro runs show ${ITEM_RUN}) — a task whose tests fail ends a draft and still opens one, so a run with no pull request at all is the thing to look at first"
    stop "the run left no pull request, so the labels were not measured"
  fi
  fact run PASS "the run ${ITEM_RUN} on ${ROW_REPO:-${REPO_SEEN}} ended ${ROW_STATUS:-unread} (outcome ${ROW_OUTCOME:-none}, pushed=${ROW_PUSHED:-yes}) and left pull request #${ROW_PR} (pr_state ${ROW_PR_STATE:-unread}), read from fugaro runs ls --project ${INSTALLATION} --json$([ "$end" = timeout ] && echo " — the wait for its row timed out after ${RUN_SECONDS}s, so what follows was read from the last row that listing held"). The pull request number is the run's own record of what it opened (F25), and every gh read below is that pull request"

  # The running label: it is written while the item's phase is running and taken
  # off when the run ends. A check that started after the run had already ended
  # cannot have seen it, and says so rather than reading its absence as a
  # failure — the absence of a label after the fact is exactly what the settled
  # row below expects.
  if [ "$saw_running" = yes ]; then
    fact running PASS "the pull request #${ROW_PR} carried fugaro:running while the run was in flight, read with 'gh pr view ${ROW_PR} --repo ${ROW_REPO}', so the daemon wrote the running status label on a real pull request (D2437's table, R4: phase running wants fugaro:running) and its write reached GitHub: U1 is answered YES for this act — the installation's GitHub App does hold pull_requests: write"
  elif [ "$end" = timeout ]; then
    fact running UNVERIFIED "the run ${ITEM_RUN} never reached a terminal row within ${RUN_SECONDS}s, so the running label was NOT settled: the check saw $([ "$pr_seen" = yes ] && echo "pull request #${ROW_PR}" || echo "no pull request") and its labels read ${PR_LABELS:--}. A run that is still going is not a failure of the labels — run the check again, or read the pull request yourself with 'gh pr view ${ROW_PR} --repo ${REPO_SEEN} --json labels'"
  elif [ "$pr_ok" -eq 0 ]; then
    # Every read of the pull request failed, so there is no reading of its
    # labels at all: that is a criterion NOT measured, not a label that was
    # absent.
    fact running UNVERIFIED "'gh pr view ${ROW_PR} --repo ${REPO_SEEN}' did not answer once while the run was in flight (${pr_bad} attempt(s), exit ${PR_GH_RC:-?}: $(last_line "$SCRATCH/gh.err")), so whether the pull request carried fugaro:running was NOT measured. This check needs gh authenticated for ${REPO_SEEN} with pull request read access"
  elif [ "$pr_seen" != yes ] || [ "$saw_live" != yes ]; then
    fact running UNVERIFIED "the run ${ITEM_RUN} had ALREADY ended by the time this check first read its pull request (the row was terminal when the check started, or the pull request appeared only at the end). fugaro:running is written while the run is in flight and taken off when it ends, so a check that starts after the fact cannot see it: the label's absence here is NOT a reading. Run the check while an approved task is going — that is what the register's section asks for — and this row is the one it settles"
  else
    fact running FAIL "the pull request #${ROW_PR} (${REPO_SEEN}) never carried fugaro:running while the run was in flight, and the check watched it live for ${waited}s (the run ended ${ROW_STATUS:-unread}, outcome ${ROW_OUTCOME:-none}; the labels read ${PR_LABELS:--}). The daemon writes it from the item's phase (running) through the credentials service, so read in this order: the daemon row above (is it still ticking?), the audit row below (did the service's route answer, and with what status?), and the item's own phase in 'fugaro orchestrator state --project ${INSTALLATION}' — a run the daemon did NOT launch has no item and is never labelled"
  fi

  # The settled reading: what the pull request carries once the run has ended.
  # The expectation is the design's own composition and not this check's taste —
  # orchestratorOutcome (internal/cli) folds the run's record status, foldPhase
  # (internal/orchestrator) turns the outcome into the item's phase, and
  # phaseToLabels turns the phase into the label: succeeded and cancelled want
  # NO status label (the item is in review or done, and success is signalled by
  # the draft becoming ready), and every other ending — failed, infra_error,
  # halted — wants fugaro:failed. A status this release cannot place reads as a
  # finished failure, which is the same default the daemon uses.
  local want="" wanted_why=""
  case "$ROW_STATUS" in
  succeeded | cancelled)
    want=""
    wanted_why="a run whose record ended '${ROW_STATUS}' leaves the item in review or done, and both of those phases want NO status label: success is already signalled by the pull request becoming ready (D2705, phaseToLabels)"
    ;;
  *)
    want="fugaro:failed"
    wanted_why="a run whose record ended '${ROW_STATUS}' folds to the item's phase failed, which wants fugaro:failed (orchestratorOutcome, foldPhase, phaseToLabels)"
    ;;
  esac
  # A run that never reached a terminal row has no settled reading to take: the
  # run's own ending is what the daemon writes the settled label FROM, so the
  # wait is skipped and the row says the criterion was not settled rather than
  # reading a leaving label as a failure (a run in flight is not a label fault).
  local settled_wait=0 settle_budget=$SETTLE_SECONDS got=""
  if [ "$end" = timeout ]; then settle_budget=0; fi
  while :; do
    if ! read_pr "$ROW_PR"; then
      fact settled UNVERIFIED "'gh pr view ${ROW_PR} --repo ${ROW_REPO}' did not answer (exit ${PR_GH_RC:-?}: $(last_line "$SCRATCH/gh.err")), so what the pull request carries once the run has ended was NOT measured. This check needs gh authenticated for ${ROW_REPO} with pull request read access"
      fact spec-label UNVERIFIED "the pull request's labels could not be read at all (gh did not answer), so whether it carries the spec label was NOT measured"
      return 0
    fi
    got=$(status_labels_of "$PR_LABELS")
    if [ "$got" = "$want" ]; then break; fi
    if [ "$settled_wait" -ge "$settle_budget" ]; then break; fi
    sleep "$SETTLE_POLL_SECONDS"
    settled_wait=$((settled_wait + SETTLE_POLL_SECONDS))
  done
  if [ "$end" = timeout ]; then
    fact settled UNVERIFIED "the run ${ITEM_RUN} never reached a terminal row within ${RUN_SECONDS}s, so what the pull request carries once it has ended was NOT settled: #${ROW_PR} (${REPO_SEEN}) carries ${PR_LABELS:--} while the run is still going, and the settled label is written from the item's phase when the run ends (R4, R8). A run still in flight is not a failure of the labels — run the check again once it has ended, or read the pull request yourself with 'gh pr view ${ROW_PR} --repo ${REPO_SEEN} --json labels'"
  elif [ "$got" = "$want" ]; then
    fact settled PASS "the pull request #${ROW_PR} carries $([ -z "$want" ] && echo "NO status label" || echo "$want") once the run has ended, which is what this run's own ending wants: ${wanted_why}. Read with 'gh pr view ${ROW_PR} --repo ${ROW_REPO} --json labels' (all its labels: ${PR_LABELS:--}), waited ${settled_wait}s after the row turned terminal (the daemon ticks every 30s by default)"
  elif [ "$got" = "fugaro:running" ]; then
    fact settled FAIL "the pull request #${ROW_PR} still carries fugaro:running ${settled_wait}s after the run's row turned terminal (${ROW_STATUS:-unread}): the daemon takes the running label off when the item leaves the running phase (R4, R8), and it did not. Read in this order: the daemon row above (a daemon that stopped ticking holds the label where it was), the credentials service's audit row below (a refused 'remove' is written there with its own status), and the item's phase in 'fugaro orchestrator state --project ${INSTALLATION}' — an item stuck in running is one whose run's ending the daemon did not reconcile"
  elif [ -z "$want" ] && has_label "$PR_LABELS" fugaro:failed; then
    fact settled FAIL "the pull request #${ROW_PR} carries fugaro:failed and the run ended ${ROW_STATUS:-unread} (outcome ${ROW_OUTCOME:-none}): ${wanted_why}, so a failure label on it says the item went to failed — read the item's own phase in 'fugaro orchestrator state --project ${INSTALLATION}' against this run's row in 'fugaro runs show ${ITEM_RUN}'. The two are read from the same record, so a disagreement is a finding about the daemon's reconciliation and not about the label"
  elif [ -n "$want" ] && [ -z "$got" ]; then
    fact settled FAIL "the pull request #${ROW_PR} carries NO status label and this run's ending wants ${want}: ${wanted_why}. The daemon writes it from the item's phase, so read the item's phase in 'fugaro orchestrator state --project ${INSTALLATION}' (an item the daemon left in running or review after a failed run is the finding), then the audit row below for a refused write (a state_unavailable status there is GitHub refusing the App's token — U1 — or the route's own rate limit)"
  else
    fact settled FAIL "the pull request #${ROW_PR} carries '${got}' where this run's ending wants $([ -z "$want" ] && echo "no status label" || echo "$want"): ${wanted_why}. The label the pull request carries is read from the daemon's own phase for the item and the item's phase from the run's own record (orchestratorOutcome, foldPhase), so a pull request whose labels disagree with the run's ending is a finding about the daemon's reconciliation — read 'fugaro runs show ${ITEM_RUN}' beside the item's phase in 'fugaro orchestrator state --project ${INSTALLATION}'"
  fi

  # The spec label: the plan's folder the item belongs to, folded to lower case
  # and prefixed (R3, D2438). An item that carries no spec has no spec label by
  # design, and the check says that rather than reading it as a failure.
  if [ -z "${ITEM_SPEC:-}" ] || [ "$ITEM_SPEC" = "-" ]; then
    fact spec-label UNVERIFIED "the item ${ITEM_ID} carries NO spec (fugaro orchestrator state --project ${INSTALLATION} prints no 'spec:' line for it), and a spec label is built from the spec alone (D2438): an item nobody gave a spec to wants no spec: label, so this criterion was NOT measured. To settle it, approve a task whose item carries a spec — the item's declaration in the daemon's own patch, 'spec: <the plan's folder under docs/>' — and run the check again"
  else
    local spec_want
    spec_want=$(printf 'spec:%s' "$(lower "$ITEM_SPEC")")
    if has_label "$PR_LABELS" "$spec_want"; then
      fact spec-label PASS "the pull request #${ROW_PR} carries ${spec_want}, the label built from the spec '${ITEM_SPEC}' the item ${ITEM_ID} declares (R3: the name folded to lower case and prefixed; D2438: the spec is the plan's folder under docs/, written by a person or a session). Read with 'gh pr view ${ROW_PR} --repo ${ROW_REPO} --json labels'"
    else
      fact spec-label FAIL "the pull request #${ROW_PR} does NOT carry ${spec_want}, the label its item's spec asks for (the item ${ITEM_ID} declares spec '${ITEM_SPEC}'; all its labels read: ${PR_LABELS:--}). Read in this order: the colours row below (does the repository hold a ${spec_want} at all, and with which colour?), the audit row (a refused 'ensure' is written there), and the item's spec in 'fugaro orchestrator state --project ${INSTALLATION}'. A spec whose name is not a label Fugaro builds (R3: [A-Za-z0-9][A-Za-z0-9-]{0,43}) is skipped with one line in the daemon's own log and no label — the spec is rendered for a label only when it passes that shape"
    fi
  fi
}

# colour_of FILE NAME — the repository's colour for a folded label name, or
# nothing when the repository does not hold it.
colour_of() {
  grep -F "L $2|" "$1" 2>/dev/null | head -n 1 | cut -d'|' -f2-
}

# step_colours — the repository's own label table, and U3 with it: a label name
# that carries a colon, created by an App's installation token, and the colour
# each of D2437's labels is created with. The service creates a label on first
# use (D2440) and never repaints one, so a label that already existed keeps
# whatever colour it had — which is the one reading here that is a person's and
# not the route's.
step_colours() {
  local rc=0
  run_call "$CALL_SECONDS" "$SCRATCH/labels.json" "$SCRATCH/gh.err" \
    gh label list --repo "$REPO_SEEN" --json name,color --limit "$LABEL_LIMIT" || rc=$?
  if [ "$rc" -ne 0 ]; then
    fact colours UNVERIFIED "'gh label list --repo ${REPO_SEEN}' did not answer (exit ${rc}: $(last_line "$SCRATCH/gh.err")), so the colours of D2437's labels were NOT measured (U3). Run it yourself: gh label list --repo ${REPO_SEEN} --json name,color --limit ${LABEL_LIMIT}"
    return 0
  fi
  reader labels "$SCRATCH/labels.json" >"$SCRATCH/labels.txt" 2>"$SCRATCH/reader.err" \
    || stop "the repository's label list could not be read: $(head -n 1 "$SCRATCH/reader.err" 2>/dev/null)"
  local held
  held=$(field_of count "$(cat "$SCRATCH/labels.txt")")
  local bad="" seen=""
  check_colour() { # check_colour NAME COLOUR REQUIRED
    local name=$1 colour=$2 required=$3 got
    got=$(colour_of "$SCRATCH/labels.txt" "$name" || true)
    if [ -z "$got" ]; then
      if [ "$required" = yes ]; then
        bad="${bad}the repository holds NO label '${name}' at all, and this run's own pull request needed it; "
      fi
      return 0
    fi
    seen="${seen}${name} ${got}, "
    if [ "$got" != "$colour" ]; then
      bad="${bad}'${name}' is ${got} where D2437's table says ${colour}; "
    fi
  }
  check_colour "fugaro:running" "$COLOUR_RUNNING" yes
  check_colour "fugaro:failed" "$COLOUR_FAILED" no
  if [ -n "${ITEM_SPEC:-}" ] && [ "$ITEM_SPEC" != "-" ]; then
    check_colour "spec:$(lower "$ITEM_SPEC")" "$COLOUR_SPEC" yes
  fi
  # A failed label the repository holds is one the daemon created at some point,
  # so it is read against the table like the rest; a repository that has never
  # failed a run holds none, and that is not a failure.
  if [ -n "$bad" ]; then
    fact colours FAIL "${bad}the table is D2437's — fugaro:running ${COLOUR_RUNNING}, fugaro:failed ${COLOUR_FAILED}, spec:<name> ${COLOUR_SPEC} — and the credentials service creates a label on first use and never repaints one (D2440), so a label that already existed in the repository keeps the colour it had: delete it in the repository ('gh label delete <name> --repo ${REPO_SEEN}') so the next tick's ensure creates it with the table's colour, and read the repository's label list back with 'gh label list --repo ${REPO_SEEN} --json name,color --limit ${LABEL_LIMIT}' (${held:-unread} labels read)"
    return 0
  fi
  # U3's own reading, said out loud when a colon-named label is in the
  # repository: GitHub accepted the name and the service created it.
  if [ -n "${ITEM_SPEC:-}" ] && [ "$ITEM_SPEC" != "-" ]; then
    fact colours PASS "the repository ${REPO_SEEN} holds every label this run's pull request needed, each with D2437's colour: ${seen:-none read}read from 'gh label list --repo ${REPO_SEEN} --json name,color --limit ${LABEL_LIMIT}' (${held:-unread} labels). The spec label settles U3: GitHub holds a label whose NAME CARRIES A COLON (spec:<name>), created by the credentials service with the App's own installation token, and the colour read back is the table's ${COLOUR_SPEC}"
    return 0
  fi
  fact colours PASS "the repository ${REPO_SEEN} holds fugaro:running with D2437's colour ${COLOUR_RUNNING}$([ -n "$(colour_of "$SCRATCH/labels.txt" fugaro:failed)" ] && echo ", and fugaro:failed with ${COLOUR_FAILED}"), read from 'gh label list --repo ${REPO_SEEN} --json name,color --limit ${LABEL_LIMIT}' (${held:-unread} labels). The item carries no spec, so no spec: label was read here — the spec-label row above says the same about the pull request"
}

# audit_read — the credentials service's label lines, retried while Cloud
# Logging catches up: ingestion lags the request by seconds, and a reading taken
# the instant the check needs it is often empty.
audit_read() {
  local waited=0 got=0
  local filter="resource.type=\"cloud_run_revision\" resource.labels.service_name=\"$SERVICE_NAME\" jsonPayload.purpose=\"label\""
  AUDIT_GCLOUD_ERROR=""
  while :; do
    if ! gcloud logging read "$filter" --project="$GCP_PROJECT" --freshness="$AUDIT_AGE" \
      --limit="$AUDIT_LIMIT" --format=json >"$SCRATCH/audit.json" 2>"$SCRATCH/audit.err"; then
      # A gcloud that refuses has answered nothing, and the row says gcloud was
      # what failed rather than the service having written no line.
      AUDIT_GCLOUD_ERROR=$(head -n 1 "$SCRATCH/audit.err" 2>/dev/null || true)
      [ -n "$AUDIT_GCLOUD_ERROR" ] || AUDIT_GCLOUD_ERROR="gcloud logging read exited non-zero and wrote nothing"
      printf '[]\n' >"$SCRATCH/audit.json"
      return 0
    fi
    got=$(reader audit "$SCRATCH/audit.json" "$AUDIT_SLUG" 2>/dev/null | sed -n 's/^repo-lines=//p' | head -n 1 || true)
    case "$got" in '' | *[!0-9]*) got=0 ;; esac
    if [ "$got" -ge 1 ]; then return 0; fi
    if [ "$waited" -ge "$AUDIT_WAIT_SECONDS" ]; then return 0; fi
    sleep "$AUDIT_POLL_SECONDS"
    waited=$((waited + AUDIT_POLL_SECONDS))
  done
}

# step_audit — the credentials service's own lines, which is the only place the
# daemon's CALL to the label route is written down: the caller (the daemon's
# own account), the repository the act was for, and the status of the act. A
# label line carries no run, no job and no label name (internal/credsvc's
# auditLabel), so this row is the reading of "the daemon really reached the
# route, as itself, for this repository" — and of U1 when a write was refused.
step_audit() {
  local slug
  slug=$(field_of slug "$(reader slug "$REPO_SEEN" 2>/dev/null || true)")
  AUDIT_SLUG=$slug
  if [ -z "$slug" ] || [ "$slug" = "-" ]; then
    fact audit UNVERIFIED "the repository slug the audit line carries could not be computed for ${REPO_SEEN}, so the service's own lines for it were NOT read. The line names the repository as internal/task's slug (a readable form and a hash of the canonical owner/name); this check computes it with the same rule and printed nothing for ${REPO_SEEN}"
    return 0
  fi
  audit_read
  if [ -n "$AUDIT_GCLOUD_ERROR" ]; then
    fact audit UNVERIFIED "'gcloud logging read' did not answer ('${AUDIT_GCLOUD_ERROR}'), so the credentials service's own lines were NOT read and whether the daemon reached the label route for ${REPO_SEEN} was NOT measured. The reading needs roles/logging.viewer (or a role holding logging.views.access) on ${GCP_PROJECT} for the account 'gcloud auth' is using, and the service's lines are under resource.labels.service_name=\"${SERVICE_NAME}\" with jsonPayload.purpose=\"label\""
    return 0
  fi
  local facts lines repo_lines refused callers statuses repos
  facts=$(reader audit "$SCRATCH/audit.json" "$slug" 2>"$SCRATCH/reader.err") \
    || stop "the service's own lines could not be read: $(head -n 1 "$SCRATCH/reader.err" 2>/dev/null)"
  lines=$(field_of lines "$facts")
  repo_lines=$(field_of repo-lines "$facts")
  refused=$(field_of refused "$facts")
  callers=$(field_of callers "$facts")
  statuses=$(field_of statuses "$facts")
  repos=$(field_of repos "$facts")
  local note="read with gcloud logging read over the last ${AUDIT_AGE} (up to ${AUDIT_LIMIT} entries): ${lines:-0} label line(s) in total, ${repo_lines:-0} naming ${slug} (the slug of ${REPO_SEEN})"
  if [ "${repo_lines:-0}" -lt 1 ]; then
    if [ "${lines:-0}" -ge 1 ]; then
      fact audit UNVERIFIED "the credentials service's own label lines name NO act for ${slug}, the slug of ${REPO_SEEN} (${note}; the repositories they do name: ${repos:--}). Which repository a line is for is the service's own resolution of the call (the installation's inventory slug), and this check computed ${slug} from ${REPO_SEEN}: a mismatch is either a repository this installation does not hold under that spelling or a slug rule that moved, and NEITHER is a fact about the labels — the pull request's own rows above are the reading of the labels themselves. Run 'gcloud logging read \"jsonPayload.purpose=\\\"label\\\"\" --project=${GCP_PROJECT} --freshness=${AUDIT_AGE} --format=json' and compare the repositories it names"
    else
      fact audit UNVERIFIED "the credentials service wrote NO label line at all in the last ${AUDIT_AGE} (${note}). A call of the label route that reaches a verified caller writes exactly one line (C1.13), so none written means the daemon never called the route for any repository — the daemon's seams are built only when the installation records the credentials service's own URL and the daemon holds an identity (internal/cli/orchestrator_labels.go: '--url == \"\" || orchestratorLabelIdentity == nil' builds none, R7), and the URL half is the one the identity row above cannot read from outside. The pull request's own rows above are the reading of the labels themselves: labels that appeared while no line was written are a contradiction worth reporting, and labels that did not appear beside no line are the daemon having no seams at all"
    fi
    return 0
  fi
  # A line naming this repository is the daemon having reached the route as
  # itself: the caller is the account the identity row read.
  case ",${callers:-}," in
  *",${ORCH_ACCOUNT},"*) ;;
  *)
    fact audit FAIL "the credentials service's own lines for ${slug}, the slug of ${REPO_SEEN}, name the caller(s) '${callers:--}' and NOT ${ORCH_ACCOUNT}, the installation's orchestrator account: a label act is performed for the caller the route verified (the orchestrator class), so a line naming another caller means the acts for this repository came from something else. ${note}"
    return 0
    ;;
  esac
  if [ "${refused:-0}" -eq 0 ]; then
    fact audit PASS "the credentials service's own lines name ${ORCH_ACCOUNT} as the caller of the label acts for ${slug}, the slug of ${REPO_SEEN}, and every one of them is 'ok' (${note}; statuses ${statuses}). That is the daemon reaching the route as itself and the acts being performed — the reads of every tick and the writes when a phase changed — so U1 is answered YES for every act the pull request's rows above show: the App could write this repository's pull requests, which is the permission the route's token asks for. This row is the SERVICE's record and never the run's word (R9, C1.13)"
  else
    fact audit FAIL "the credentials service's own lines for ${slug}, the slug of ${REPO_SEEN}, carry a REFUSED act: statuses ${statuses} (${note}; callers ${callers}). The status is the service's own refusal type and each one leads somewhere different: 'state_unavailable' is the service or GitHub refusing to perform the act — the App's token and the repository's own permission table (U1: raise pull_requests: write on the App's settings page), the bundle holding no App key, a provider that could not be built, or the route's own rate limit for this repository; 'request_refused' is the call itself being refused — a write whose pull request is not corroborated (its head branch is not exactly fugaro/<run id>, or its author is not Fugaro's own App identity). Read the line's own message in Cloud Logging, and the daemon's own log line for the same act"
  fi
}

# ------------------------------------------------------------------ the block
# The keys, in the order they are measured: the shell, the installation and the
# one it must never touch, then the F19 premise (which stops the check when it is
# missing), the daemon, the item and the run it launched, then the labels on that
# run's pull request, the repository's own table, and the service's record of the
# acts.
BLOCK_KEYS=(xtrace agent-session installation belong identity daemon item run
  running settled spec-label colours audit restore)

# criteria_unmet FACTS — the block's criteria that are not a PASS row. A row
# reading UNVERIFIED (the criterion could not be verified: a reading taken after
# the run ended, an item carrying no spec, a listing that named another
# repository) and a key no step wrote at all are criteria that were NOT settled,
# and they are what makes a run INCOMPLETE (exit 3) rather than a pass.
criteria_unmet() {
  local facts=$1 key line
  for key in "${BLOCK_KEYS[@]}"; do
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    case "$line" in
    "$key|PASS|"*) ;;
    *) printf '%s\n' "$key" ;;
    esac
  done
}

# verdict_of FACTS STATUS — the block's own last word, and the one the exit
# status carries: 0 is PASS, 1 is FAIL, and both 2 (the run stopped before it
# could measure everything) and 3 (it finished with criteria left unsettled) are
# INCOMPLETE.
verdict_of() {
  case "$2" in
  0) printf 'PASS' ;;
  1) printf 'FAIL' ;;
  *) printf 'INCOMPLETE' ;;
  esac
}

print_block() {
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 640 RESULT\n' >&3
  for key in "${BLOCK_KEYS[@]}"; do
    local line
    line=$(printf '%s\n' "$facts" | grep -m1 "^$key|" || true)
    if [ -n "$line" ]; then
      printf 'FACT %s: %s — %s\n' "$key" \
        "$(printf '%s' "$line" | cut -d'|' -f2)" \
        "$(printf '%s' "$line" | cut -d'|' -f3-)" >&3
    else
      printf 'FACT %s: FAIL — not reached: nothing measured this criterion (the step that writes this row did not run, or the check stopped before it)\n' "$key" >&3
    fi
    printed="$printed$key"$'\n'
  done
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 640 RESULT: %s (exit %s)\n' "$(verdict_of "$facts" "$status")" "$status" >&3
}

# ------------------------------------------------------------------- the plan
print_plan() {
  local RUN_SHOWN
  RUN_SHOWN=${RUN_ID_WANT:-the ONE item the daemon state shows in phase running}
  cat <<PLAN_EOF
Check 640 — the status and spec labels on a REAL pull request, written by the
daemon. This is the PLAN. --dry-run touches nothing: nothing is read, no call is
made, and no file of yours is opened.

  installation   ${INSTALLATION}   (GCP project ${GCP_PROJECT}, region ${REGION}; hard-coded, overridable)
  service        ${SERVICE_NAME}   (the credentials service the daemon asks for every label act)
  daemon account ${ORCH_ACCOUNT}   (the orchestrator class the route admits; F19's own account)
  run            ${RUN_SHOWN}

What the run will do, in this order:
  1. read the installation with 'fugaro doctor --project ${INSTALLATION} --json' and refuse it if it is
     not ${INSTALLATION} in GCP project ${GCP_PROJECT}, or if it names 'belong';
  2. read F19 FIRST — 'gcloud run services get-iam-policy ${SERVICE_NAME}' for ${ORCH_ACCOUNT}
     holding roles/run.invoker, and 'gcloud run services describe' for the account in the service's own
     revision: when either is missing, ${PROG} reports ONE row ("the daemon cannot reach the label route:
     F19") and STOPS (exit 2), because no label can be written and every row below would fail for that
     reason rather than for its own;
  3. read 'fugaro orchestrator ls' (a daemon this machine runs and is beating) and
     'fugaro orchestrator state' (the ONE item in phase running, with the run id and the spec its
     declaration carries);
  4. wait for that run in 'fugaro runs ls' until its row is terminal, reading 'gh pr view' all the
     while: the pull request it opened must carry fugaro:running while the run is in flight;
  5. read the pull request again once the run has ended and wait (up to ${SETTLE_SECONDS}s) for the
     status labels the run's own ending wants — NONE for a run that ended succeeded or cancelled, and
     fugaro:failed for every other ending — plus the spec:<name> label its item's spec asks for;
  6. read the repository's own label table ('gh label list') for D2437's colours — the spec label with
     a colon in its name is U3's own reading;
  7. read the credentials service's own lines ('gcloud logging read', purpose label) for the caller,
     the repository and the status of every act: that is the daemon reaching the route as itself, and
     the status of a refused write is U1's reading;
  8. write NOTHING: every call above is a read — no run is launched, no label is written, no policy is
     changed — and the scratch directory is removed on the way out, however the run ends.
PLAN_EOF
}

# ----------------------------------------------------------------------- main
USAGE="check640.sh [--installation <name>] [--run-id <id>] [--gcp-project <project>] [--region <region>] [--dry-run]"

usage() {
  printf 'usage: %s\n' "$USAGE"
  printf '\n'
  printf 'Check 640 — the status and spec labels on a real pull request, written by the\n'
  printf 'installation'"'"'s orchestrator daemon. Run it while ONE approved task is going:\n'
  printf 'it is read-only, it launches nothing and it costs nothing billable.\n'
  printf '\n'
  printf '  --installation NAME  the installation whose daemon, runs and logs are read (default fugaro)\n'
  printf '  --run-id ID          the run whose pull request is read, when more than one item is in flight\n'
  printf '  --gcp-project ID     the GCP project (default fugaro-dev; belong is refused by name)\n'
  printf '  --region REGION      the Cloud Run region (default us-east5)\n'
  printf '  --dry-run            print the plan and touch nothing\n'
  printf '\n'
  printf 'The full description, the exit codes and every knob (CHECK640_*) are the comment\n'
  printf 'block at the top of this file: read it before you run the check for real.\n'
}

parse_args() {
  INSTALLATION=${CHECK640_INSTALLATION:-fugaro}
  GCP_PROJECT=${CHECK640_GCP_PROJECT:-fugaro-dev}
  REGION=${CHECK640_REGION:-us-east5}
  RUN_ID_WANT=${CHECK640_RUN_ID:-}
  SERVICE_NAME=${CHECK640_SERVICE_NAME:-fugaro-credentials}
  ORCH_ACCOUNT_ID=${CHECK640_ORCH_ACCOUNT:-fugaro-orch}
  COLOUR_RUNNING=${CHECK640_COLOUR_RUNNING:-1d76db}
  COLOUR_FAILED=${CHECK640_COLOUR_FAILED:-d73a4a}
  COLOUR_SPEC=${CHECK640_COLOUR_SPEC:-6e7781}
  DRY_RUN=no
  PROJECT_NAME=""
  GCP_PROJECT_SEEN=""
  REGION_SEEN=""
  AUDIT_SLUG=""
  AUDIT_GCLOUD_ERROR=""
  IDENTITY_MISSING=""
  ITEM_ID=""
  ITEM_PHASE=""
  ITEM_REPO=""
  REPO_SEEN=""
  ITEM_RUN=""
  ITEM_SPEC=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --installation | --project)
      [ $# -ge 2 ] || die "$1 needs an installation name. usage: $USAGE"
      INSTALLATION=$2
      shift 2
      ;;
    --gcp-project)
      [ $# -ge 2 ] || die "--gcp-project needs a project id. usage: $USAGE"
      GCP_PROJECT=$2
      shift 2
      ;;
    --region)
      [ $# -ge 2 ] || die "--region needs a Cloud Run region. usage: $USAGE"
      REGION=$2
      shift 2
      ;;
    --run-id)
      [ $# -ge 2 ] || die "--run-id needs a run id. usage: $USAGE"
      RUN_ID_WANT=$2
      shift 2
      ;;
    --service)
      [ $# -ge 2 ] || die "--service needs the credentials service's name. usage: $USAGE"
      SERVICE_NAME=$2
      shift 2
      ;;
    --dry-run)
      DRY_RUN=yes
      shift
      ;;
    -h | --help)
      usage
      REACHED_END=yes
      PRINTED=1
      trap - ERR
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  [ -n "$INSTALLATION" ] || die "--installation needs a name. usage: $USAGE"
  [ -n "$GCP_PROJECT" ] || die "--gcp-project needs a project id. usage: $USAGE"
  case "$ORCH_ACCOUNT_ID" in
  *[!a-z0-9-]*) die "the orchestrator account id '${ORCH_ACCOUNT_ID}' is not one a service account may have (lower-case letters, digits and dashes). usage: $USAGE" ;;
  esac
  ORCH_ACCOUNT="${ORCH_ACCOUNT_ID}@${GCP_PROJECT}.iam.gserviceaccount.com"
  if [ -n "$RUN_ID_WANT" ]; then
    case "$RUN_ID_WANT" in
    *[!A-Za-z0-9._-]*) die "--run-id '${RUN_ID_WANT}' holds a character a run id may not have. usage: $USAGE" ;;
    esac
  fi
}

main() {
  parse_args "$@"
  CALL_SECONDS=${CHECK640_CALL_SECONDS:-60}
  RUN_SECONDS=${CHECK640_RUN_SECONDS:-5400}
  RUN_POLL_SECONDS=${CHECK640_RUN_POLL_SECONDS:-20}
  SETTLE_SECONDS=${CHECK640_SETTLE_SECONDS:-600}
  SETTLE_POLL_SECONDS=${CHECK640_SETTLE_POLL_SECONDS:-15}
  AUDIT_WAIT_SECONDS=${CHECK640_AUDIT_WAIT_SECONDS:-120}
  AUDIT_POLL_SECONDS=${CHECK640_AUDIT_POLL_SECONDS:-5}
  AUDIT_AGE=${CHECK640_AUDIT_AGE:-3h}
  AUDIT_LIMIT=${CHECK640_AUDIT_LIMIT:-500}
  RUNS_LIMIT=${CHECK640_RUNS_LIMIT:-100}
  LABEL_LIMIT=${CHECK640_LABEL_LIMIT:-1000}
  if [ "${CHECK640_DRY_RUN:-}" = yes ]; then DRY_RUN=yes; fi
  guard_belong

  if [ "$DRY_RUN" = yes ]; then
    print_plan
    REACHED_END=yes
    PRINTED=1
    trap - ERR
    exit 0
  fi

  # After the two endings above rather than before the arguments are read: a run
  # that only prints the plan, or only the usage, calls none of these tools, and
  # refusing it for a missing `gh` would deny the owner the one output they can
  # have without a GitHub login at all.
  need_tools
  scratch_up
  write_reader
  guard_xtrace
  guard_agent_session

  step_installation
  step_identity
  step_daemon
  step_item
  step_run
  step_colours
  step_audit

  REACHED_END=yes
  trap - ERR
  local failed
  failed=$(grep -c '|FAIL|' "$FACTS_FILE" 2>/dev/null || true)
  [ "${failed:-0}" -eq 0 ] || return 1
  return 0
}

# on_signal: the ONE signal handler, armed for the whole run and never disarmed
# -- including through the cleanup, which is where its second answer matters.
on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

trap finish EXIT
trap on_signal INT TERM HUP
trap on_error ERR
main "$@"
