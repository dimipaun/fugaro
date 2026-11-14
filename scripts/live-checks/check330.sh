#!/usr/bin/env bash
# Check 330 — the api-key review's own path, as ONE script YOU run.
#
# It is a READER: it launches nothing, publishes nothing, writes no object and
# makes no model call. Every row it prints is read off two things the plan's
# Check 330 names — a run's `result.json` (its stage timings and its cost split)
# and the gateway's `model call` log lines of that run's execution — beside two
# reads of the installation itself (its published policy, and the review job's
# own description in slice B).
#
# The check has one shape per slice (docs/api-key-review, implementation-plan
# §5), and the owner runs the slices the answer to D1410 reaches:
#
# **A. The in-job path (AK1–AK3).** An api-key run whose coder is on the
# project's provider route and whose reviewer is a Claude model, with no
# `claude_accounts` stage account published, and the CONTRAST: the same task run
# again with a `review` stage account published. What it reads:
#   - the gateway's call log names BOTH: the run's own stages' lines carry a
#     `route` (the provider) and the review class's lines carry none (Anthropic);
#   - the review stage's `model_usd` is the gateway's figure and
#     `model_notional_usd` is ABSENT: the dollars are real;
#   - the run's `route_by` carries the run's own stages and not the review's;
#   - the contrast's review stage has `credential: oauth` and its
#     `model_notional_usd` EQUALS its `model_usd`.
# **B. The cloud Anthropic-key path (AK4–AK5)**, over a run the owner launched
# with `fugaro review --pr N` (or `fugaro run --pr N --recipe review-only`) on an
# installation that publishes the pairing's sentinel and has the credentials
# service on. This script reads the mechanical half: the run was launched on the
# sentinel's own review job variant, that job's environment holds no credential,
# and the run's model calls went through the gateway to Anthropic. WHICH key
# served them, and that a repository without the pairing is still refused, are
# yours to read — the steps it prints say how, and the script never fakes them.
# **C. The OpenRouter path (AK6a, AK6b, AK7)** is NOT run: it may not be run
# before Check 50, and `internal/creds/select.go`'s `checkCandidates` refuses
# every tier-C candidate, so a run of it today would measure that refusal rather
# than the path. `--provider` is refused for that reason.
#
# The stage names the two halves are split by are the runner's: the run's own
# stages (`plan`, `implement`, `fix`) and the cheap first review (`review_first`,
# level 1, which runs in the coder's slot) are served by the run's own
# credential, and only the review class (`review`, `adversarial`, `triage`) is
# what a `claude_accounts` stage account serves (config.MixedAccountStages;
# internal/runner's resolveMixedStages).
#
# Usage:      bash scripts/live-checks/check330.sh --sandbox <checkout>
#             --run <slug>/<run-id> --contrast <slug>/<run-id>
#             [--review <slug>/<run-id>] [--log-view <view>] [--dry-run]
#             --sandbox is a SANDBOX repository's checkout: the project, the
#             region and the run listing are read there, through the installed
#             `fugaro`, so this check never takes a project from an argument.
#             --run is the slice-A task's run and --contrast the same task's run
#             with the `review` stage account published: BOTH are required,
#             because the two runs side by side are what this check is for.
#             --review is slice B's run, when the pairing is published.
#             --log-view is the installation's `log_view`, when its config sets
#             one: without it the log read is scoped to the project's default
#             log bucket and finds nothing under log isolation.
#             `--dry-run` prints the guards and every command a real run would
#             make and runs nothing at all: no fugaro call, no gcloud call, and
#             no verdict.
# Needs:      bash, mktemp, sed, awk, python3, gcloud and fugaro on PATH.
#             Nothing is installed and nothing is fetched; a missing one stops
#             the run with one line naming it. python3 reads the JSON documents
#             (a row read with grep would report a shape the release did not
#             write the day a document is re-wrapped).
# Where:      a SANDBOX installation only, and NEVER the installation named
#             `belong`: the project the checkout belongs to is refused when it
#             names it, and so is a run reference. It is also never run in a
#             coding agent's session (CLAUDECODE and the like are refused), and
#             never on an installation whose published policy's budget mode is
#             `off` — the review class refuses a run with no gateway (F11), so
#             such an installation measures the refusal rather than the path.
# Creates:    nothing. Every call is a read: `fugaro doctor`, `fugaro runs ls`,
#             `fugaro project policy show`, `gcloud storage cat` of the runs'
#             own records, `gcloud logging read` of their executions and (slice
#             B) `gcloud run jobs describe`. The only thing it writes is its own
#             scratch directory, which is removed with the run.
# Costs:      nothing billable and no model call: reads alone.
# Runs:       by YOU. No agent runs any step of it.
# Cleanup:    always, including on a Ctrl-C: the scratch directory is removed.
#             There is nothing else to undo — this check makes nothing — and the
#             `restore` row says so rather than leaving it unsaid.
# Output:     progress on stderr, the numbered steps that stay yours, and ONE
#             block on stdout headed CHECK 330 RESULT, one FACT row per reading.
#             Exit 0 when every criterion held, 1 when one FAILED, and 2 when
#             the run could not tell — a guard stopped it, a document or a log
#             could not be read, a tool is missing, or an internal error ended
#             it. A row that could not be read is never a pass.
#
# No credential is handled anywhere in it as a value: every read is a name (a
# route, a model, a job, an account value) and no command here prints an
# environment's contents, a secret or a token. A `bash -x` of it would show the
# commands and nothing shaped like a credential.
#
# CHECK330_TIMEOUT bounds one command; CHECK330_KEEP_SCRATCH keeps the scratch
# directory. Neither can point the check at another project or another run.
set -euo pipefail
set -E

# A trace would copy every read this check makes to the terminal, and the log
# lines it reads are the run's own output. Refused before anything else runs.
XTRACE_AT_START=no
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
case "${BASHOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the RESULT block is
# written to the saved descriptors.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }
ABORT_REASON=""
# Set once every step has run, so the exit trap can tell a finished run from one
# that ended on an error of its own.
REACHED_END=no
# The verdict the steps reached: 0 every criterion held, 1 one failed, 2 the run
# could not tell. Set by step_verdict and read by the exit trap, so the block
# and the exit status cannot disagree.
CRITERIA_STATUS=0
die() { say "$*"; ABORT_REASON=$*; exit 2; }

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block, so a stop still gives you something to paste back.
stop() {
  fact aborted FAIL "$1"
  ABORT_REASON=$1
  owner "stopped: $1"
  exit 2
}

FACTS_FILE=""
fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
}

# unmeasured KEY "detail": a row this check could NOT read, for a reason that is
# not a finding about the release — a document that is not there, a log read
# that answered nothing, an execution nobody can name. It reads FAIL in the
# block (it is not a pass) and the run ends as one that could not tell, exit 2:
# a check that read nothing is not a check that found nothing.
#
# The keys are collected in a FILE and not in a variable: a reference is resolved
# inside a command substitution, which is a subshell, and a variable set there
# would be a row in the block that never reached the verdict.
UNMEASURED_FILE=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  [ -n "$UNMEASURED_FILE" ] && printf '%s\n' "$1" >>"$UNMEASURED_FILE"
  return 0
}

# on_error: an internal error is a fact, never a verdict. Under `set -e` the
# first command that fails outside a guard would end the run with no fact at
# all, and the block would then read as an answer about the api-key review for a
# run that measured nothing.
on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  if [ -n "$FACTS_FILE" ] && ! grep -q '^aborted|' "$FACTS_FILE"; then
    fact aborted FAIL "an internal error ended the run at line $line of the script; nothing is concluded about the api-key review"
  fi
  ABORT_REASON="an internal error ended the run (line $line)"
  owner "stopped: $ABORT_REASON"
  exit 2
}

# ---------------------------------------------------------------- requirements
need_tools() {
  command -v bash >/dev/null 2>&1 || die "Check 330 needs bash"
  local t
  for t in mktemp sed awk python3 gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 330 needs '$t' and it is not on PATH"
  done
}

# How long one command is given before its row reads as a call that did not
# answer. Every read here answers in seconds on a healthy installation.
TIMEOUT=${CHECK330_TIMEOUT:-120}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk330-mine"
SCRATCH_REMOVED=no
KEEP_SCRATCH=${CHECK330_KEEP_SCRATCH:-}
PRINTED=""
FACTS_CAPTURED=""
HELPED=no

# What the steps fill in.
P=""
REGION=""
SANDBOX=""
RUN_REF=""
CONTRAST_REF=""
REVIEW_REF=""
LOG_VIEW=""
BUCKET=""
RUN_ID=""
CONTRAST_ID=""
REVIEW_ID=""
RUN_KEY=""
CONTRAST_KEY=""
REVIEW_KEY=""
RUN_EXEC=""
CONTRAST_EXEC=""
REVIEW_EXEC=""
# One reason per read that failed, so a criterion row can say WHY it could not
# be read instead of reading as a finding about the release.
RUN_RECORD_PROBLEM=""
RUN_LAUNCH_PROBLEM=""
RUN_CALLS_PROBLEM=""
CONTRAST_RECORD_PROBLEM=""
CONTRAST_LAUNCH_PROBLEM=""
REVIEW_RECORD_PROBLEM=""
REVIEW_LAUNCH_PROBLEM=""
REVIEW_CALLS_PROBLEM=""
# The rows the slices read off the records and the logs.
ROUTES=""
PROVIDER_LINES=0
ANTHROPIC_LINES=0
ANTHROPIC_MODELS=""
CONTRAST_CREDENTIAL=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk330.XXXXXXXX") || die "Check 330 cannot make a scratch directory"
  chmod 700 "$SCRATCH"
  : >"$SCRATCH/$SCRATCH_MARKER"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  UNMEASURED_FILE="$SCRATCH/unmeasured"
  : >"$UNMEASURED_FILE"
  # A scratch directory inside a work tree could be committed by accident, and
  # this one holds a run's record and its log.
  if (cd "$SCRATCH" && git rev-parse --show-toplevel >/dev/null 2>&1); then
    die "Check 330 stopped: $SCRATCH is inside a git work tree, so what it writes could be committed by accident — set TMPDIR outside every repository and run again"
  fi
  say "scratch directory: $SCRATCH (mode 0700, removed at the end)"
}

remove_scratch() {
  case "$SCRATCH" in
  "") return 0 ;;
  # ${HOME:-}: reading it unguarded would abort cleanup under `set -u` on a
  # machine where HOME is unset, leaving the scratch directory behind.
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
    say "CHECK330_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The `--json` documents this check reads, in one program. Every query prints
# `key=value` lines or `\t`-separated rows, so a shell can hold them without a
# second parser, and an absent optional field is printed as `-`: absent and zero
# are different readings here (StageTiming omits a zero `model_usd`, so a review
# stage that spent nothing and one whose figure is missing would otherwise read
# the same).
write_reader() {
  cat >"$SCRATCH/read.py" <<'PY'
import json
import sys


def load(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def yes(value):
    return "true" if value else "false"


def flat(value):
    return " ".join(str(value or "").split())


def num(value):
    if value is None:
        return "-"
    return repr(float(value)) if isinstance(value, float) else str(value)


def text(value):
    value = flat(value)
    return value if value else "-"


what = sys.argv[1]
doc = load(sys.argv[2])

if what == "doctor":
    project = doc.get("project") or {}
    print("project=%s" % flat(project.get("gcp_project")))
    print("region=%s" % flat(project.get("region")))
    print("name=%s" % flat(project.get("name")))
    yml = doc.get("fugaro_yaml") or {}
    print("yaml_valid=%s" % yes(yml.get("valid")))
    print("yaml_path=%s" % flat(yml.get("path")))
elif what == "rows":
    rows = doc.get("runs") or []
    print("count=%d" % len(rows))
    for row in rows:
        print("row\t%s\t%s\t%s\t%s\t%s" % (
            flat(row.get("run_id")),
            flat(row.get("run")),
            flat(row.get("status")),
            flat(row.get("execution")),
            flat(row.get("repo")),
        ))
elif what == "policy":
    pol = doc.get("policy")
    print("project=%s" % flat(doc.get("project")))
    print("published=%s" % yes(pol is not None))
    pol = pol or {}
    budget = pol.get("budget") or {}
    print("mode=%s" % text(budget.get("mode")))
    print("default_account=%s" % text(pol.get("default_claude_account")))
    stages = pol.get("claude_account_stages") or {}
    print("stage_review=%s" % text(stages.get("review")))
    print("stage_accounts=%s" % (",".join(
        "%s=%s" % (k, flat(v)) for k, v in sorted(stages.items())) or "-"))
    # The axis's two values that name no Claude account carry a doubled dash
    # (config.AnthropicKeyAccount, config.ProviderKeyAccount), which is what
    # puts them outside the account-name rule: that is how a value read here is
    # told from a Claude account without this script spelling either sentinel.
    values = set()
    value = flat(pol.get("default_claude_account"))
    if value:
        values.add(value)
    for named in (pol.get("claude_account_stages") or {}).values():
        named = flat(named)
        if named:
            values.add(named)
    for profile in (pol.get("profiles") or {}).values():
        for named in profile.get("claude_accounts") or []:
            named = flat(named)
            if named:
                values.add(named)
    named = sorted(v for v in values if "--" in v)
    print("non_account=%s" % (",".join(named) if named else "-"))
elif what == "record":
    cost = doc.get("cost") or {}
    print("run_id=%s" % flat(doc.get("run_id")))
    print("status=%s" % flat(doc.get("status")))
    print("outcome=%s" % flat(doc.get("outcome")))
    print("cost_usd=%s" % num(cost.get("model_usd")))
    routes = cost.get("route_by")
    if routes is None:
        print("route_by_total=-")
    else:
        total = 0.0
        attributed = 0.0
        for name, usd in sorted(routes.items()):
            total += float(usd or 0)
            # "unattributed" is the split's own residual: spend the ledger holds
            # that no stage report carried, so it has no route to be told from
            # (internal/runner/cost.go, unattributedModel). It is not a provider
            # route's share, which is what the reader of this row compares.
            if name != "unattributed":
                attributed += float(usd or 0)
            print("route\t%s\t%s" % (flat(name), num(usd)))
        print("route_by_total=%s" % num(total))
        print("route_by_attributed=%s" % num(attributed))
    # Columns: 2 name, 3 model, 4 model_usd, 5 model_notional_usd,
    # 6 credential, 7 served_by, 8 calls, 9 duration_s.
    for stage in doc.get("stages") or []:
        print("stage\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s" % (
            flat(stage.get("name")),
            text(stage.get("model")),
            num(stage.get("model_usd")),
            num(stage.get("model_notional_usd")),
            text(stage.get("credential")),
            text(stage.get("served_by")),
            num(stage.get("calls")),
            num(stage.get("duration_s")),
        ))
elif what == "launch":
    print("run_id=%s" % flat(doc.get("run_id")))
    print("job=%s" % flat(doc.get("job")))
    print("execution=%s" % flat(doc.get("execution")))
    print("backend=%s" % flat(doc.get("backend")))
elif what == "calls":
    entries = doc if isinstance(doc, list) else (doc.get("entries") or [])
    count = 0
    for entry in entries:
        payload = entry.get("jsonPayload") or {}
        if flat(payload.get("message")) != "model call":
            continue
        count += 1
        print("call\t%s\t%s\t%s\t%s\t%s\t%s" % (
            text(payload.get("stage")),
            text(payload.get("model")),
            text(payload.get("serving_model")),
            text(payload.get("route")),
            text(payload.get("status")),
            num(payload.get("charged_micros")),
        ))
    print("count=%d" % count)
elif what == "job":
    spec = doc.get("spec") or {}
    template = ((spec.get("template") or {}).get("template")) or {}
    containers = template.get("containers") or []
    if not containers:
        print("shape=unexpected")
    else:
        print("shape=ok")
        container = containers[0] or {}
        env = container.get("env") or []
        secret_env = []
        for item in env:
            source = item.get("valueFrom") or {}
            if source.get("secretKeyRef"):
                secret_env.append(flat(item.get("name")))
        volumes = template.get("volumes") or []
        secret_volumes = [v for v in volumes if isinstance(v, dict) and v.get("secret")]
        mounts = container.get("volumeMounts") or []
        print("name=%s" % flat((doc.get("metadata") or {}).get("name")))
        print("env_count=%d" % len(env))
        print("secret_env=%d" % len(secret_env))
        print("secret_env_names=%s" % (",".join(secret_env) if secret_env else "-"))
        print("volumes=%d" % len(volumes))
        print("secret_volumes=%d" % len(secret_volumes))
        print("mounts=%d" % len(mounts))
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

read_json() { # read_json QUERY FILE DEST
  python3 "$SCRATCH/read.py" "$1" "$2" >"$3" 2>"$3.err"
}

# field FILE KEY — the value of one `key=value` line, "" when absent. A file
# that is not there is what a read that did not answer leaves behind, and the
# caller says so in a row of its own: under `set -o pipefail` sed's failure would
# otherwise end the run as this check's internal error.
field() { # field FILE KEY
  { sed -n "s/^$2=//p" "$1" 2>/dev/null || true; } | head -n 1
}

# stage_field FILE NAME COLUMN — one column of a `stage\t<name>\t...` row.
stage_field() { # stage_field FILE NAME COLUMN
  awk -F'\t' -v name="$2" -v col="$3" '$1 == "stage" && $2 == name { print $col; exit }' "$1" 2>/dev/null || true
}

# own_stages_usd FILE — the run's OWN stages' model_usd, summed over every row,
# not over the first row of each name: the bounce loop runs `fix` and
# `review_first` once per finding and once per bounce (internal/runner's
# bounce_test.go records implement, review_first, fix, review, fix, review_first,
# review), so a sum that stopped at the first row of each name would leave every
# later round out of the run's real spend and read a healthy run as a failed
# criterion.
own_stages_usd() { # own_stages_usd FILE
  awk -F'\t' '$1 == "stage" && ($2 == "plan" || $2 == "implement" || $2 == "fix" || $2 == "review_first") && $4 != "-" { s += $4 } END { printf "%.6f", s + 0 }' "$1" 2>/dev/null || true
}

stage_names() { # stage_names FILE — every stage the record ran, comma-separated
  awk -F'\t' '$1 == "stage" { print $2 }' "$1" 2>/dev/null | paste -sd, - 2>/dev/null || true
}

# close_enough A B [EPS]: 0 when the two figures are within EPS (a cent by
# default). The record's own split is exact to the micro-dollar, but the stages
# and the route split are priced from the same ledger on two paths, so the
# comparison says "the same money" rather than "the same float".
close_enough() { # close_enough A B [EPS]
  awk -v a="$1" -v b="$2" -v eps="${3:-0.01}" 'BEGIN { d = a - b; if (d < 0) d = -d; exit !(d <= eps) }'
}

above_zero() { # above_zero VALUE
  awk -v v="$1" 'BEGIN { exit !(v > 0) }'
}

# run_bounded SECONDS KEY DIR CMD...: one command, from the directory it has to
# be run in, under a bound. A cloud read that hangs must not outlive the check
# that gave up on it, and its exit status is one of this check's readings (a
# record that is not there), so the ERR trap is dropped in the child.
run_bounded() { # run_bounded SECONDS KEY DIR CMD...
  local secs=$1 key=$2 dir=$3
  shift 3
  BOUNDED_TIMED_OUT=no
  ( trap - ERR; cd "$dir" || exit 1; exec "$@" ) >>"$SCRATCH/$key.out" 2>>"$SCRATCH/$key.err" &
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
  return 0
}

# said KEY: the first line of a failed call's own output, so no row reports a
# refusal with no reason in it. gcloud and fugaro both write it to stderr.
said() { # said KEY
  { head -n 2 "$SCRATCH/$1.err" 2>/dev/null; head -n 1 "$SCRATCH/$1.out" 2>/dev/null; } |
    tr -d '\000' | tr '\n' ' ' | sed 's/[[:space:]]\+/ /g; s/^ //; s/ $//' | cut -c1-400
}

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    local why="this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check330.sh --sandbox <a sandbox checkout> --run <ref> --contrast <ref> — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy a run's own log output to the terminal"
    fact xtrace FAIL "$why"
    stop "$why"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy a run's own log output"
}

# guard_agent_session: the check is the owner's. A coding agent's session is
# refused here rather than at each read, because an agent that ran this far
# would report its own session's limits as findings about the release.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    fact agent-session FAIL "$marker is set, so this is a coding agent's session: Check 330 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
    stop "$marker is set, so this is a coding agent's session: Check 330 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

guard_belong() { # guard_belong NAME...
  local name
  for name in "$@"; do
    case "$(printf '%s' "$name" | tr '[:upper:]' '[:lower:]')" in
    *belong*)
      fact belong FAIL "'$name' names the owner's own installation: Check 330 reads a project's runs and their logs, and it is never run against the installation the owner's real work is on"
      stop "the value '$name' matches the installation 'belong', which this check must never read"
      ;;
    esac
  done
  fact belong PASS "nothing this check was given or read names the owner's own installation: the project is ${P:-the one the checkout belongs to}, the region is ${REGION:-the checkout region}${LOG_VIEW:+, the log view is $LOG_VIEW}, the checkout is ${SANDBOX:-not named}, and none of them contains the word"
}

# budget_guard MODE: the installation's published budget mode decides whether
# slice B measures the path at all. The review class refuses a run with no
# gateway (F11), so on a mode-`off` installation the launch is the refusal
# rather than the path, and the check says so instead of reading it.
budget_guard() { # budget_guard MODE
  if [ "$1" = "off" ]; then
    fact budget FAIL "the published policy's budget mode is 'off', so this installation's runs take no gateway: the review class refuses a run without one (F11), and a slice-B launch here would measure that refusal rather than the api-key path"
    stop "the published budget mode is 'off': this check must not be run against such an installation (implementation-plan §5)"
  fi
  fact budget PASS "the published policy's budget mode is ${1:-not published}, which is not 'off': a review run on this installation reaches a gateway, which is the path this check reads"
}

# ------------------------------------------------------------------- the reads
step_project() {
  say "reading the checkout's project: fugaro doctor --json (in $SANDBOX)"
  run_bounded "$TIMEOUT" doctor "$SANDBOX" fugaro doctor --json
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    stop "fugaro doctor --json did not answer within ${TIMEOUT}s in $SANDBOX"
  fi
  if [ "$BOUNDED_STATUS" -ne 0 ]; then
    stop "fugaro doctor --json failed in $SANDBOX (exit $BOUNDED_STATUS): $(said doctor) — the check cannot say which project the sandbox belongs to, and every row below is about that project's runs. Is $SANDBOX the sandbox repository's checkout, with the installed fugaro on PATH?"
  fi
  if ! read_json doctor "$SCRATCH/doctor.out" "$SCRATCH/doctor.txt"; then
    stop "fugaro doctor --json printed something this check cannot read as its document: $SCRATCH/doctor.out"
  fi
  P=$(field "$SCRATCH/doctor.txt" project)
  REGION=$(field "$SCRATCH/doctor.txt" region)
  [ -n "$P" ] || stop "fugaro doctor --json named no gcp_project for $SANDBOX: the runs bucket and every log filter this check builds are named after the project the checkout's config carries"
  [ -n "$REGION" ] || stop "fugaro doctor --json named no region for $SANDBOX: the log filter is scoped to the job's region"
  BUCKET="fugaro-runs-${P}"
  guard_agent_session
  # The log view is guarded with the rest: it is what every log read is scoped
  # to (--resource-names), so a view naming the owner's own installation is that
  # installation's logs read, whatever the checkout and the run references say.
  guard_belong "$P" "$REGION" "$SANDBOX" "$RUN_REF" "$CONTRAST_REF" "$REVIEW_REF" "$LOG_VIEW"
  fact project PASS "the sandbox checkout belongs to $P ($REGION), and its runs are read there: gs://$BUCKET/runs/<slug>/<run>/"
  fact sandbox PASS "$SANDBOX is a Fugaro checkout the installed fugaro reads its project config from (fugaro.yaml valid=$(field "$SCRATCH/doctor.txt" yaml_valid) at $(field "$SCRATCH/doctor.txt" yaml_path)); every fugaro call below is made from it"
}

step_policy() {
  say "reading the published policy: fugaro project policy show --json (in $SANDBOX)"
  run_bounded "$TIMEOUT" policy "$SANDBOX" fugaro project policy show --json
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    stop "fugaro project policy show --json did not answer within ${TIMEOUT}s in $SANDBOX"
  fi
  if [ "$BOUNDED_STATUS" -ne 0 ]; then
    stop "fugaro project policy show --json failed in $SANDBOX (exit $BOUNDED_STATUS): $(said policy) — the budget mode and the account values this check reads are in the published policy, and what a launch ran under cannot be read off a node nobody could read"
  fi
  read_json policy "$SCRATCH/policy.out" "$SCRATCH/policy.txt" ||
    stop "fugaro project policy show --json printed something this check cannot read as its document: $SCRATCH/policy.out"
  if [ "$(field "$SCRATCH/policy.txt" published)" != true ]; then
    fact policy INFO "no policy is published in $P, so this installation's runs take their accounts from the job their launch picks and no sentinel is named: slice B's job row reads the variant the run was launched on, and this row has no policy to read beside it"
    fact budget INFO "no published policy, so there is no configured budget mode to read ($(field "$SCRATCH/policy.txt" mode)): this check refuses a mode-'off' installation from the policy alone, and says here that it could not read one"
    return 0
  fi
  budget_guard "$(field "$SCRATCH/policy.txt" mode)"
  fact policy PASS "the published policy names the account values $(field "$SCRATCH/policy.txt" non_account) $(field "$SCRATCH/policy.txt" stage_accounts) for its profiles (default_account=$(field "$SCRATCH/policy.txt" default_account)): a value carrying a doubled dash is one of the two the axis gives no Claude account — the project's own Anthropic API key or its provider key — and that is the sentinel slice B's pairing publishes"
}

# resolve_run REF LABEL: one named run, out of the listing the sandbox's runs
# bucket answers. The reference may be a bare run id, `<slug>/<run-id>`, or the
# `run` field a row carries; exactly one row has to match, because a list read
# by position alone could be another run's.
resolve_run() { # resolve_run REF LABEL
  local ref=$1 label=$2 matched
  matched=$(awk -F'\t' -v ref="$ref" '
    $1 == "row" && ($2 == ref || $3 == ref || $3 == "*/" ref) {
      print $2 "\t" $3 "\t" $4 "\t" $5 "\t" $6
    }' "$SCRATCH/rows.txt" 2>/dev/null | head -n 2 || true)
  if [ -z "$matched" ]; then
    unmeasured "$label" "$ref names no run of $P's runs bucket (fugaro runs ls --all --json --since 0 listed $(field "$SCRATCH/rows.txt" count) runs): a run is named by its run id (20261114-130101-a101), by <slug>/<run-id>, or by the run field a row carries — and the reference has to be one this checkout's listing holds"
    return 1
  fi
  if [ "$(printf '%s\n' "$matched" | wc -l | tr -d ' ')" != 1 ]; then
    unmeasured "$label" "$ref matches more than one run of $P's listing, so this check cannot say which one it is about: name it by <slug>/<run-id>"
    return 1
  fi
  printf '%s' "$matched"
}

step_runs() {
  say "listing the runs: fugaro runs ls --all --json --since 0 (in $SANDBOX)"
  run_bounded "$TIMEOUT" rows "$SANDBOX" fugaro runs ls --all --json --since 0
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    stop "fugaro runs ls --all --json did not answer within ${TIMEOUT}s in $SANDBOX"
  fi
  if [ "$BOUNDED_STATUS" -ne 0 ]; then
    stop "fugaro runs ls --all --json failed in $SANDBOX (exit $BOUNDED_STATUS): $(said rows) — the runs this check reads are found through it, and a reference nobody can resolve is not a run"
  fi
  read_json rows "$SCRATCH/rows.out" "$SCRATCH/rows.txt" ||
    stop "fugaro runs ls --json printed something this check cannot read as its document: $SCRATCH/rows.out"

  local row
  if row=$(resolve_run "$RUN_REF" run); then
    RUN_ID=$(printf '%s' "$row" | cut -f1)
    RUN_KEY=$(printf '%s' "$row" | cut -f2)
    RUN_EXEC=$(printf '%s' "$row" | cut -f4)
    fact run PASS "$RUN_REF is run $RUN_ID of $(printf '%s' "$row" | cut -f5), status=$(printf '%s' "$row" | cut -f3), execution=$(printf '%s' "$row" | cut -f4): slice A's task, the run whose review class this check reads"
  fi
  if row=$(resolve_run "$CONTRAST_REF" contrast-run); then
    CONTRAST_ID=$(printf '%s' "$row" | cut -f1)
    CONTRAST_KEY=$(printf '%s' "$row" | cut -f2)
    CONTRAST_EXEC=$(printf '%s' "$row" | cut -f4)
    fact contrast-run PASS "$CONTRAST_REF is run $CONTRAST_ID of $(printf '%s' "$row" | cut -f5), status=$(printf '%s' "$row" | cut -f3), execution=$(printf '%s' "$row" | cut -f4): the same task with a review stage account published, the other half of the pair"
  fi
  if [ -n "$REVIEW_REF" ] && row=$(resolve_run "$REVIEW_REF" review-run); then
    REVIEW_ID=$(printf '%s' "$row" | cut -f1)
    REVIEW_KEY=$(printf '%s' "$row" | cut -f2)
    REVIEW_EXEC=$(printf '%s' "$row" | cut -f4)
    fact review-run PASS "$REVIEW_REF is run $REVIEW_ID of $(printf '%s' "$row" | cut -f5), status=$(printf '%s' "$row" | cut -f3), execution=$(printf '%s' "$row" | cut -f4): slice B's review-only run"
  fi
}

# read_record KEY TAG PREFIX: the run's own record and launch, out of the runs
# bucket, read exactly as the owner would read them. TAG names the files the
# read is kept in and PREFIX (upper case) the problem variables a failure is
# recorded in — never as a row of its own — so the
# criterion rows that stand on it say what they could not read. It returns
# success either way: a read that did not answer is the variable's own business,
# and a nonzero return would end the run as this check's internal error.
read_record() { # read_record KEY TAG PREFIX
  local key=$1 tag=$2 prefix=$3 problem=""
  run_bounded "$TIMEOUT" "$tag-record" "$SANDBOX" gcloud storage cat "gs://$BUCKET/runs/$key/result.json" --project="$P"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    problem="the record of run $key did not answer within ${TIMEOUT}s (gs://$BUCKET/runs/$key/result.json)"
  elif [ "$BOUNDED_STATUS" -ne 0 ]; then
    problem="the record of run $key could not be read from gs://$BUCKET/runs/$key/result.json (exit $BOUNDED_STATUS): $(said "$tag-record")"
  elif ! read_json record "$SCRATCH/$tag-record.out" "$SCRATCH/$tag-record.txt"; then
    problem="the record of run $key is not a document this check can read: $SCRATCH/$tag-record.out"
  fi
  if [ -n "$problem" ]; then
    printf -v "${prefix}_RECORD_PROBLEM" '%s' "$problem"
    return 0
  fi
  run_bounded "$TIMEOUT" "$tag-launch" "$SANDBOX" gcloud storage cat "gs://$BUCKET/runs/$key/launch.json" --project="$P"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    printf -v "${prefix}_LAUNCH_PROBLEM" '%s' "the launch record of run $key did not answer within ${TIMEOUT}s"
  elif [ "$BOUNDED_STATUS" -ne 0 ]; then
    printf -v "${prefix}_LAUNCH_PROBLEM" '%s' "the launch record of run $key could not be read from gs://$BUCKET/runs/$key/launch.json (exit $BOUNDED_STATUS): $(said "$tag-launch")"
  elif ! read_json launch "$SCRATCH/$tag-launch.out" "$SCRATCH/$tag-launch.txt"; then
    printf -v "${prefix}_LAUNCH_PROBLEM" '%s' "the launch record of run $key is not a document this check can read: $SCRATCH/$tag-launch.out"
  fi
  return 0
}

# read_calls EXEC TAG PREFIX: the gateway's `model call` lines for one
# execution, with the CLI's own filter (internal/backend/gcp's executionFilter)
# and its own scope (the installation's log view when there is one, the
# project's default log bucket otherwise). Like read_record it returns success
# either way and leaves the reason in the caller's own variable.
read_calls() { # read_calls EXEC TAG PREFIX
  local exec=$1 tag=$2 prefix=$3
  local region job name scope=(--resource-names="projects/$P")
  region=$(printf '%s' "$exec" | cut -d/ -f4)
  job=$(printf '%s' "$exec" | cut -d/ -f6)
  name=$(printf '%s' "$exec" | cut -d/ -f8)
  if [ -z "$region" ] || [ -z "$job" ] || [ -z "$name" ]; then
    printf -v "${prefix}_CALLS_PROBLEM" '%s' "the run's execution '$exec' is not projects/<project>/locations/<region>/jobs/<job>/executions/<name>, so there is no log to read: a run from before the launcher recorded an execution has none"
    return 0
  fi
  [ -z "$LOG_VIEW" ] || scope=(--resource-names="$LOG_VIEW")
  local filter
  filter="resource.type=\"cloud_run_job\" AND resource.labels.location=\"$region\" AND resource.labels.job_name=\"$job\" AND labels.\"run.googleapis.com/execution_name\"=\"$name\" AND jsonPayload.message=\"model call\""
  run_bounded "$TIMEOUT" "$tag-calls" "$SANDBOX" gcloud logging read "${scope[@]}" --limit=5000 --freshness=30d --format=json "$filter"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    printf -v "${prefix}_CALLS_PROBLEM" '%s' "gcloud logging read of execution $name did not answer within ${TIMEOUT}s"
    return 0
  fi
  if [ "$BOUNDED_STATUS" -ne 0 ]; then
    printf -v "${prefix}_CALLS_PROBLEM" '%s' "gcloud logging read of execution $name failed (exit $BOUNDED_STATUS): $(said "$tag-calls") — no access, a view that is not there and the wrong project are the three usual reasons"
    return 0
  fi
  if ! read_json calls "$SCRATCH/$tag-calls.out" "$SCRATCH/$tag-calls.txt"; then
    printf -v "${prefix}_CALLS_PROBLEM" '%s' "gcloud logging read of execution $name answered something this check cannot read as a log: $SCRATCH/$tag-calls.out"
    return 0
  fi
  return 0
}

# review_stage FILE — the first review-class stage the record holds, or "".
review_stage() { # review_stage FILE
  local name
  for name in review adversarial triage; do
    if [ -n "$(stage_field "$1" "$name" 2)" ]; then printf '%s' "$name"; return 0; fi
  done
  return 0
}

step_slice_a() {
  local record="$SCRATCH/run-record.txt" contrast="$SCRATCH/contrast-record.txt"
  local name review_usd review_notional detail ok found
  local kind stage model serving route charged provider_stages="" anthropic_stages=""

  # The log's own split, read off the gateway's lines: a stage timing says what
  # a stage spent, and only the log says which upstream answered.
  if [ -f "$SCRATCH/run-calls.txt" ]; then
    while IFS=$'\t' read -r kind stage model serving route status charged; do
      [ "$kind" = call ] || continue
      case "$stage" in
      plan | implement | fix | review_first)
        if [ "$route" != "-" ]; then
          PROVIDER_LINES=$((PROVIDER_LINES + 1))
          case ",$ROUTES," in *",$route,"*) ;; *) ROUTES="${ROUTES:+$ROUTES,}$route" ;; esac
          case ",$provider_stages," in *",$stage,"*) ;; *) provider_stages="${provider_stages:+$provider_stages,}$stage" ;; esac
        fi
        ;;
      review | adversarial | triage)
        if [ "$route" = "-" ]; then
          ANTHROPIC_LINES=$((ANTHROPIC_LINES + 1))
          case ",$ANTHROPIC_MODELS," in *",$serving,"*) ;; *) ANTHROPIC_MODELS="${ANTHROPIC_MODELS:+$ANTHROPIC_MODELS,}$serving" ;; esac
          case ",$anthropic_stages," in *",$stage,"*) ;; *) anthropic_stages="${anthropic_stages:+$anthropic_stages,}$stage" ;; esac
        fi
        ;;
      esac
    done <"$SCRATCH/run-calls.txt"
    say "the run's gateway lines: $PROVIDER_LINES on a route (${provider_stages:-none}, routes ${ROUTES:-none}), $ANTHROPIC_LINES with no route (${anthropic_stages:-none}, served as ${ANTHROPIC_MODELS:-none})"
  fi

  local why=""
  if [ -z "$RUN_ID" ]; then
    why="slice A's run was not resolved, so there was nothing to read (see the run row)"
  elif [ -n "$RUN_CALLS_PROBLEM" ]; then
    why="$RUN_CALLS_PROBLEM"
  fi
  # The provider half: the run's own stages reached the provider.
  if [ -n "$why" ]; then
    unmeasured log-coder "$why"
  elif [ "$PROVIDER_LINES" -gt 0 ]; then
    fact log-coder PASS "the gateway's log for this run's execution names ${PROVIDER_LINES} call(s) of the run's own stages (${provider_stages}) on route ${ROUTES}: the coder's calls reached the provider the project's routing claims"
  else
    fact log-coder FAIL "the gateway's log for this run's execution names no call of the run's own stages (plan, implement, fix, review_first) carrying a route: the slice-A premise is a coder on the project's provider route, and a run whose own calls went nowhere but Anthropic is not that run — or the log read is short of the run's entries, which --log-view (the installation's own view) is the fix for"
  fi
  # The Anthropic half: the review class reached Anthropic, which no route
  # claims.
  if [ -n "$why" ]; then
    unmeasured log-review "$why"
  elif [ "$ANTHROPIC_LINES" -gt 0 ] && printf '%s' "$ANTHROPIC_MODELS" | grep -q 'claude'; then
    fact log-review PASS "the gateway's log names ${ANTHROPIC_LINES} call(s) of the review class (${anthropic_stages}) with NO route, served as ${ANTHROPIC_MODELS}: the review's calls went to Anthropic, not to the provider the run's own stages reached"
  else
    fact log-review FAIL "the gateway's log for this run's execution names no route-less call of the review class (review, adversarial, triage) served as a Claude model (${ANTHROPIC_LINES} such call(s), served as ${ANTHROPIC_MODELS:-none}): the api-key review's whole claim is that this stage reaches Anthropic while the coder reaches the provider, and the log does not show it"
  fi

  local stage_found="" why_record=""
  if [ -z "$RUN_ID" ]; then
    why_record="slice A's run was not resolved, so there was nothing to read (see the run row)"
  elif [ -n "$RUN_RECORD_PROBLEM" ]; then
    why_record="$RUN_RECORD_PROBLEM"
  elif [ -z "$(stage_names "$record")" ]; then
    why_record="run $RUN_ID's record holds no stage timing at all, so there is nothing to read off it"
  else
    stage_found=$(review_stage "$record")
    [ -n "$stage_found" ] ||
      why_record="run $RUN_ID's record holds no review-class stage (it ran $(stage_names "$record")): a task whose review never ran measures nothing about the api-key review"
  fi
  # The review stage's dollars are real: the gateway priced them and none of
  # them is a notional figure.
  if [ -n "$why_record" ]; then
    unmeasured review-dollars "$why_record"
  else
    detail=""
    ok=yes
    for name in review adversarial triage; do
      [ -n "$(stage_field "$record" "$name" 2)" ] || continue
      review_usd=$(stage_field "$record" "$name" 4)
      review_notional=$(stage_field "$record" "$name" 5)
      detail="$detail $name: model_usd=$review_usd, model_notional_usd=$review_notional, credential=$(stage_field "$record" "$name" 6), served_by=$(stage_field "$record" "$name" 7), calls=$(stage_field "$record" "$name" 8);"
      [ "$review_notional" = "-" ] || ok=no
      if [ "$review_usd" = "-" ] || ! above_zero "$review_usd"; then ok=no; fi
    done
    if [ "$ok" = yes ]; then
      fact review-dollars PASS "the review class's stage timings carry a model_usd above zero and NO model_notional_usd —$detail the gateway's figure is the stage's own money, and no dollar of it is notional"
    else
      fact review-dollars FAIL "the review class's stage timings do not read as real dollars —$detail a stage whose model_notional_usd is written spent notional dollars (an account's own token, or a tier-A credential), and one whose model_usd is absent or zero spent nothing the gateway priced"
    fi
  fi

  # route_by carries the run's own stages and not the review's.
  if [ -n "$why_record" ]; then
    unmeasured route-by "$why_record"
  else
    local route_total route_attributed cost_total own_total
    route_total=$(field "$record" route_by_total)
    route_attributed=$(field "$record" route_by_attributed)
    cost_total=$(field "$record" cost_usd)
    own_total=$(own_stages_usd "$record")
    # The routes' share, not the split's whole: the residual ("unattributed") is
    # the ledger's leftovers and belongs to no route, so counting it as a
    # provider's would read a run that is healthy as one whose split carries
    # money the coder's stages never spent.
    if [ "$route_total" = "-" ] || [ -z "$route_total" ]; then
      fact route-by FAIL "run $RUN_ID's cost carries no route_by at all, and its own stages' calls carried a route in the log above: the split that says which route paid for what was never written"
    elif [ -z "$route_attributed" ] || ! above_zero "$route_attributed"; then
      fact route-by FAIL "run $RUN_ID's route_by is $route_total, of which no provider route carries anything ($route_attributed), for a run whose own stages' calls carried a route in the log: the split and the log disagree"
    elif ! close_enough "$route_attributed" "$own_total" 0.02; then
      fact route-by FAIL "run $RUN_ID's provider routes total \$$route_attributed (of $route_total written) and its own stages' (plan, implement, fix, review_first, every round) model_usd totals \$$own_total: the split is not the run's own stages' spend, so the review class's dollars may be in it"
    elif close_enough "$route_attributed" "$cost_total" 0.02; then
      fact route-by FAIL "run $RUN_ID's provider routes total \$$route_attributed and the run's whole model_usd is \$$cost_total: the two are the same money, so the review class's dollars are inside the provider's split — the one thing this reading is for"
    else
      fact route-by PASS "run $RUN_ID's provider routes total \$$route_attributed (of $route_total written), which is its own stages' spend (\$$own_total, every round of them), against a run total of \$$cost_total: the split carries the coder's stages and not the review class's"
    fi
  fi

  # The contrast: the same task with a review stage account published, where the
  # stage's credential is the account's and every dollar of it is notional.
  local cwhy="" cdetail="" cok=yes cfound=no cname=""
  if [ -z "$CONTRAST_ID" ]; then
    cwhy="the contrast run was not resolved, so there was nothing to read (see the contrast-run row)"
  elif [ -n "$CONTRAST_RECORD_PROBLEM" ]; then
    cwhy="$CONTRAST_RECORD_PROBLEM"
  elif [ -z "$(stage_names "$contrast")" ]; then
    cwhy="the contrast run $CONTRAST_ID's record holds no stage timing at all"
  else
    cfound=$(review_stage "$contrast")
    [ -n "$cfound" ] ||
      cwhy="the contrast run $CONTRAST_ID's record holds no review-class stage (it ran $(stage_names "$contrast")): the pair is what this check is for, and half of it ran no review"
  fi
  if [ -n "$cwhy" ]; then
    unmeasured contrast "$cwhy"
  else
    for name in review adversarial triage; do
      [ -n "$(stage_field "$contrast" "$name" 2)" ] || continue
      local ccred cnotional cusd
      ccred=$(stage_field "$contrast" "$name" 6)
      cnotional=$(stage_field "$contrast" "$name" 5)
      cusd=$(stage_field "$contrast" "$name" 4)
      cname="$name"
      cdetail="$cdetail $name: credential=$ccred, model_usd=$cusd, model_notional_usd=$cnotional, served_by=$(stage_field "$contrast" "$name" 7);"
      [ "$ccred" = oauth ] || cok=no
      if [ "$cnotional" = "-" ] || [ "$cusd" = "-" ] || ! above_zero "$cusd"; then
        cok=no
      elif ! close_enough "$cnotional" "$cusd" 0.000001; then
        cok=no
      fi
    done
    CONTRAST_CREDENTIAL=$(stage_field "$contrast" "$cname" 6)
    if [ "$cok" = yes ]; then
      fact contrast PASS "with a review stage account published, the review class's timings read credential=oauth and model_notional_usd equal to model_usd —$cdetail the account served the stage, so every dollar of it is the subscription's and none is the gateway's"
    else
      fact contrast FAIL "the contrast run's review class does not read as an account-served stage —$cdetail a stage account published for the review class has to leave credential=oauth and model_notional_usd equal to model_usd (both above zero); anything else means the second run did not measure the other half of the pair"
    fi
  fi
}

step_slice_b() {
  local key
  if [ -z "$REVIEW_REF" ]; then
    for key in review-launch review-env review-log; do
      fact "$key" INFO "slice B was not named (no --review), so the cloud Anthropic-key path was not read: it needs a run you launched with fugaro review --pr N (or fugaro run --pr N --recipe review-only) on an installation that publishes the pairing's sentinel"
    done
    fact review-run INFO "slice B was not named (no --review), so no review run was resolved and the cloud Anthropic-key path was not read"
    return 0
  fi
  # Slice B's three rows stand on documents the owner launched and this check
  # only reads: one that is not there is a row nobody could read, not a finding
  # about the api-key path (unmeasured, as slice A's identical paths are), and
  # the register's section says so.
  if [ -z "$REVIEW_ID" ]; then
    for key in review-launch review-env review-log; do
      unmeasured "$key" "not read: slice B's run reference did not resolve (see the review-run row)"
    done
    return 0
  fi
  if [ -n "$REVIEW_RECORD_PROBLEM" ]; then
    for key in review-launch review-env review-log; do
      unmeasured "$key" "not read: $REVIEW_RECORD_PROBLEM"
    done
    return 0
  fi
  if [ -n "$REVIEW_LAUNCH_PROBLEM" ]; then
    for key in review-launch review-env review-log; do
      unmeasured "$key" "not read: $REVIEW_LAUNCH_PROBLEM"
    done
    return 0
  fi

  local job
  job=$(field "$SCRATCH/review-run-launch.txt" job)
  if [ -z "$job" ]; then
    for key in review-launch review-env review-log; do
      unmeasured "$key" "not read: run $REVIEW_ID's launch record names no job, so this check cannot say which job the review ran on"
    done
    return 0
  fi

  # The variant's name carries the account segment with the review suffix, and
  # the account is the value the published policy names for the profile: a value
  # carrying a doubled dash is one of the axis's two that name no Claude account,
  # which is the sentinel this slice publishes. The comparison drops the dashes
  # and the case because a job name is sanitized (the project job naming), so the
  # stem and not the exact spelling is what a reader has.
  local stem
  stem=$(printf '%s' "$job" | tr 'A-Z' 'a-z' | tr -cd 'a-z0-9')
  case "$stem" in
  *anthropicapikeyprojectreview*)
    fact review-launch PASS "slice B's run was launched on $job: the job's own name carries the review variant of the sentinel anthropic-api-key--project, which is the variant a review of a repository whose profile names the project's Anthropic key runs on"
    ;;
  *providerkeyprojectreview*)
    fact review-launch PASS "slice B's run was launched on $job: the job's own name carries the review variant of provider-key--project — the axis's OTHER non-account value, which is slice C's (AK6a) and not the Anthropic key this slice measures; read the key that served it with the step below, and note in the register which of the two this run was"
    ;;
  *)
    fact review-launch FAIL "slice B's run was launched on $job, which is not the review variant of either of the axis's non-account values (anthropic-api-key--project, provider-key--project): a review of a repository whose profile names a Claude account runs on that account's variant, and this run is not the cloud Anthropic-key path slice B measures"
    ;;
  esac

  # The job's own environment: the review container mounts no credential of the
  # repository's, because its read-only token and its model calls both come from
  # the credentials service.
  run_bounded "$TIMEOUT" review-job "$SANDBOX" gcloud run jobs describe "$job" --project="$P" --region="$REGION" --format=json
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured review-env "gcloud run jobs describe $job did not answer within ${TIMEOUT}s"
  elif [ "$BOUNDED_STATUS" -ne 0 ]; then
    unmeasured review-env "gcloud run jobs describe $job --project=$P --region=$REGION failed (exit $BOUNDED_STATUS): $(said review-job) — the job the review ran on is the only place its own environment can be read, and a job nobody could describe is not a job whose environment held nothing"
  elif ! read_json job "$SCRATCH/review-job.out" "$SCRATCH/review-job.txt"; then
    unmeasured review-env "gcloud run jobs describe $job printed something this check cannot read as a job: $SCRATCH/review-job.out"
  elif [ "$(field "$SCRATCH/review-job.txt" shape)" != ok ]; then
    unmeasured review-env "gcloud run jobs describe $job answered a document with no container template, so this check cannot read what the job's environment holds: $SCRATCH/review-job.out"
  else
    local secret_env volumes secret_volumes env_count mounts
    secret_env=$(field "$SCRATCH/review-job.txt" secret_env)
    env_count=$(field "$SCRATCH/review-job.txt" env_count)
    volumes=$(field "$SCRATCH/review-job.txt" volumes)
    secret_volumes=$(field "$SCRATCH/review-job.txt" secret_volumes)
    mounts=$(field "$SCRATCH/review-job.txt" mounts)
    if [ "$secret_env" = 0 ] && [ "$secret_volumes" = 0 ]; then
      fact review-env PASS "$job describes with $env_count environment entr(ies), none of them secret-sourced, and $volumes volume(s) with $mounts mount(s), none of them a secret: the job the review ran in held no credential of the repository's (its read-only installation token and its model calls both come from the credentials service)"
    else
      fact review-env FAIL "$job describes with $secret_env secret-sourced environment entr(ies) ($(field "$SCRATCH/review-job.txt" secret_env_names)) and $secret_volumes secret volume(s): the review-only variant mounts no credential of the repository's, so a job carrying one is not the variant this slice measures"
    fi
  fi

  # The review run's own calls, through the gateway to Anthropic.
  if [ -n "$REVIEW_CALLS_PROBLEM" ]; then
    unmeasured review-log "$REVIEW_CALLS_PROBLEM"
  elif [ -f "$SCRATCH/review-run-calls.txt" ]; then
    local kind stage model serving route charged lines=0 models="" route_lines=0
    while IFS=$'\t' read -r kind stage model serving route status charged; do
      [ "$kind" = call ] || continue
      if [ "$route" = "-" ]; then
        lines=$((lines + 1))
        case ",$models," in *",$serving,"*) ;; *) models="${models:+$models,}$serving" ;; esac
      else
        route_lines=$((route_lines + 1))
      fi
    done <"$SCRATCH/review-run-calls.txt"
    if [ "$lines" -gt 0 ] && printf '%s' "$models" | grep -q 'claude'; then
      fact review-log PASS "the gateway's log for slice B's execution names $lines route-less call(s) served as ${models}${route_lines:+, and $route_lines call(s) on a route}: the review-only run's model calls went through the gateway to Anthropic, which is the upstream the project's own key is held for"
    elif [ "$lines" -gt 0 ]; then
      fact review-log FAIL "the gateway's log for slice B's execution names $lines route-less call(s) but none served as a Claude model (served as ${models:-none}): a review-only run's calls are Anthropic calls, and this log does not show them"
    else
      fact review-log FAIL "the gateway's log for slice B's execution names no route-less model call at all (${route_lines} on a route): a review whose calls never reached the gateway with no route did not go through the project's Anthropic key, and the log read itself is worth a second look with --log-view"
    fi
  fi
}

step_verdict() {
  local facts status key keys
  facts=$(cat "$FACTS_FILE" 2>/dev/null || true)
  status=0
  for key in budget log-coder log-review review-dollars route-by contrast review-launch review-env review-log; do
    if printf '%s\n' "$facts" | grep -q "^$key|FAIL|"; then status=1; fi
  done
  keys=$(awk '!seen[$0]++' "$UNMEASURED_FILE" 2>/dev/null | paste -sd, - 2>/dev/null || true)
  if [ -n "$keys" ]; then
    fact unmeasured FAIL "these rows could not be READ at all: $keys — a reading nobody took, which is not a finding about the api-key review and not a pass either"
    status=2
  fi
  CRITERIA_STATUS=$status
}

# ------------------------------------------------------------------ the Restore
# This check creates nothing, so its Restore is one row that says so: there is
# no object, no grant, no account, no policy and no run of its own to delete.
restore_run() {
  fact restore PASS "nothing of this check's was created: it launches no run, writes no object, publishes no policy and mints no credential. Its whole footprint is the reads above (fugaro doctor/runs ls/project policy show, gcloud storage cat of the named runs' records, gcloud logging read of their executions, gcloud run jobs describe of the review job) and its scratch directory, which is removed with this run"
}

cleanup() {
  restore_run
  FACTS_CAPTURED=$(cat "$FACTS_FILE" 2>/dev/null || true)
  remove_scratch
}

# ------------------------------------------------- what stays yours to read
# The numbered steps of the register's section that no script can take: a
# browser, a second launch's refusal, a key's own name in the service's record.
# The script prints them rather than faking them, and the register prints the
# same list.
print_owner_steps() {
  owner "THE STEPS THAT STAY YOURS (Check 330; the register's section has them too):"
  owner "  1. Slice A's setup and its two runs: a sandbox repository whose fugaro.yaml writes agent.auth: api-key, whose coder runs a model the project's provider route claims, whose models.reviewer is a Claude model, with the published policy naming NO review stage account. Run one task; then publish a review stage account and run the same task again — that second run is --contrast."
  owner "  2. Slice B's key: open the credentials service's own record for the review run (or its console) and read that the route-less calls this check saw were served by THE PROJECT'S ANTHROPIC KEY the sentinel names, and not by another credential the service holds. Neither the run's record nor the gateway's log names a key, and this check never guesses one."
  owner "  3. Slice B's refusal: in a sandbox repository whose agent.auth is api-key and whose profile's claude_accounts does NOT name the key, run fugaro review --pr N (or fugaro run --pr N --recipe review-only) and read that the launch is REFUSED — nothing launched, no claim, no budget token — with the sentence that names the value, its two kinds and the fix (profiles.<profile>.claude_accounts, then fugaro update)."
  owner "  4. Slice C (the OpenRouter path) is NOT run: it may not be run before Check 50, which gates a third-party route for a Claude model, and internal/creds/select.go refuses every tier-C candidate today — a run of it would measure that refusal rather than the path."
  owner "  5. Record what you read in the register's section: the run references, the routes and serving models the block names, and how steps 2 and 3 answered."
}

# -------------------------------------------------------------------- the plan
print_plan() {
  local bucket="gs://${BUCKET:-fugaro-runs-<project>}"
  say "dry run: NOTHING below was run. A real run would run, in this order:"
  say "  [network, reads] fugaro doctor --json (in $SANDBOX) — the project and the region every read below is scoped to, and the checkout's fugaro.yaml"
  say "  [network, reads] fugaro project policy show --json (in $SANDBOX) — the published policy's budget mode (a mode of 'off' stops the run: the review class refuses a run with no gateway) and the account values it names for its profiles"
  say "  [network, reads] fugaro runs ls --all --json --since 0 (in $SANDBOX) — the listing --run $RUN_REF, --contrast $CONTRAST_REF${REVIEW_REF:+ and --review $REVIEW_REF} are resolved against: one row each, or the row cannot be read"
  say "  [network, reads] gcloud storage cat $bucket/runs/<slug>/<run>/result.json --project=<project>, for each named run — the run's stage timings (name, model, model_usd, model_notional_usd, credential, served_by, calls) and its cost split (route_by)"
  say "  [network, reads] gcloud storage cat .../launch.json --project=<project>, for each named run — the execution the log filter is built from, and (slice B) the job the review ran on"
  say "  [network, reads] gcloud logging read --resource-names=<the installation's log view, or projects/<project>> --limit=5000 --freshness=30d --format=json 'resource.type=\"cloud_run_job\" AND resource.labels.location=\"<region>\" AND resource.labels.job_name=\"<job>\" AND labels.\"run.googleapis.com/execution_name\"=\"<name>\" AND jsonPayload.message=\"model call\"', for each named run — the gateway's own lines, which is where a call's route, its serving model and its charged dollars are"
  say "  [network, reads, slice B only] gcloud run jobs describe <the review run's job> --project=<project> --region=<region> --format=json — the job's own environment, which must hold no secret-sourced entry and no secret volume"
  say "and the rows it would print, in the block's own order:"
  say "  the guards and the reads: project, sandbox, agent-session, belong, budget, policy, run, contrast-run${REVIEW_REF:+, review-run}"
  say "  slice A: log-coder (the run's own stages' lines carry a route), log-review (the review class's lines carry none and are served as a Claude model), review-dollars (the review stage's model_usd is real and its model_notional_usd absent), route-by (the split carries the run's own stages and not the review's), contrast (the second run's review class reads credential=oauth with model_notional_usd equal to model_usd)"
  say "  slice B (with --review): review-launch (the job is the review variant of the sentinel the policy names), review-env (the job's environment holds no credential), review-log (the run's calls reached the gateway with no route)"
  say "  restore: nothing to undo — this check creates nothing"
  say "Nothing was read and no verdict was taken: this dry run made no fugaro call and no gcloud call at all. The steps that stay yours (a browser, a refusal, the key's own name) are printed at the end of a real run, and they are in the register's section."
}

# ------------------------------------------------------------- the block
# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads FAIL
# — not reached — and any other recorded key follows, in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(project sandbox agent-session belong budget policy run contrast-run review-run
  log-coder log-review review-dollars route-by contrast
  review-launch review-env review-log
  restore)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 330 RESULT\n' >&3
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
    printed="$printed"$'\n'"$key"
  done
  printed="$printed"$'\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key verdict detail; do
    [ -n "$key" ] || continue
    case $'\n'"$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 330 RESULT END (exit %s)\n' "$status" >&3
}

finish() {
  local status=$?
  trap - EXIT ERR
  cleanup
  local facts=$FACTS_CAPTURED
  # --help printed the usage on its own and read nothing: it is not a run, so it
  # gets no block and no verdict.
  if [ "$HELPED" = yes ]; then exit 0; fi
  if [ -n "$ABORT_REASON" ] && ! printf '%s' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT_REASON"
  fi
  # A run that never reached its verdict ends honestly, whatever stopped it: bash
  # exits by itself for an error the ERR trap does not see (an unbound variable,
  # a bad substitution), and a block that then read as a finished run would offer
  # a FAILed criterion row as an answer about the api-key review when the reads
  # that would have filled it never ran.
  if [ "$REACHED_END" = no ]; then
    if ! printf '%s' "$facts" | grep -q '^aborted|'; then
      facts="${facts}"$'\n'"aborted|FAIL|the run ended before it reached its verdict (exit ${status}): the rows that read 'not reached' were NOT read, which is this script ending on an error rather than an answer about the api-key review"
    fi
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ -z "$SCRATCH" ]; then
    facts="${facts}"$'\n'"restore|PASS|nothing was created: the run stopped before it made a scratch directory, and this check creates nothing anyway"
  fi
  if [ -n "$SCRATCH" ] && [ "$SCRATCH_REMOVED" != yes ]; then
    facts="${facts}"$'\n'"scratch|FAIL|the scratch directory is STILL THERE at $SCRATCH — it holds a run's record and its log; remove it yourself"
    if [ "$status" -eq 0 ]; then status=1; fi
  fi
  # The verdict comes from the facts, not from the exit path: an exit 0 beside a
  # FAILed criterion, or beside a row nobody could read, would read as a pass.
  if [ "$status" -eq 0 ]; then status=${CRITERIA_STATUS:-0}; fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    if [ -n "$RUN_ID" ]; then
      printf '\nTHE PAIR THIS CHECK READ: %s (routes %s) and %s (review credential %s)\n' \
        "$RUN_REF" "${ROUTES:-none}" "$CONTRAST_REF" "${CONTRAST_CREDENTIAL:-not read}" >&3
    fi
    print_block "$facts" "$status"
  fi
  exit "$status"
}

on_signal() {
  owner "interrupted: printing what was read and cleaning up"
  exit 2
}

# -------------------------------------------------------------------- main
USAGE="check330.sh --sandbox <checkout> --run <slug>/<run-id> --contrast <slug>/<run-id> [--review <slug>/<run-id>] [--log-view <view>] [--dry-run]"

parse_args() {
  SANDBOX=""
  RUN_REF=""
  CONTRAST_REF=""
  REVIEW_REF=""
  LOG_VIEW=""
  DRY_RUN=${CHECK330_DRY_RUN:-}
  while [ $# -gt 0 ]; do
    case "$1" in
    --sandbox)
      [ $# -ge 2 ] || die "--sandbox needs a path. usage: $USAGE"
      SANDBOX=$2
      shift 2
      ;;
    --run)
      [ $# -ge 2 ] || die "--run needs a run reference (<slug>/<run-id>). usage: $USAGE"
      RUN_REF=$2
      shift 2
      ;;
    --contrast)
      [ $# -ge 2 ] || die "--contrast needs a run reference (<slug>/<run-id>). usage: $USAGE"
      CONTRAST_REF=$2
      shift 2
      ;;
    --review)
      [ $# -ge 2 ] || die "--review needs a run reference (<slug>/<run-id>). usage: $USAGE"
      REVIEW_REF=$2
      shift 2
      ;;
    --log-view)
      [ $# -ge 2 ] || die "--log-view needs the view (projects/<p>/locations/<l>/buckets/<b>/views/<v>). usage: $USAGE"
      LOG_VIEW=$2
      shift 2
      ;;
    --provider | --provider-key | --slice-c)
      die "slice C (the OpenRouter path, AK6a/AK6b/AK7) is NOT run: it may not be run before Check 50, which gates a third-party route for a Claude model on, and internal/creds/select.go refuses every tier-C candidate today — a run of it would measure that refusal rather than the path. Nothing was read. usage: $USAGE"
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    -h | --help)
      printf 'usage: %s\n' "$USAGE"
      HELPED=yes
      PRINTED=1
      exit 0
      ;;
    *)
      die "unknown argument '$1'. usage: $USAGE"
      ;;
    esac
  done
  [ -n "$SANDBOX" ] || die "--sandbox is required, and it has to be a SANDBOX repository's checkout: the project and every run this check reads come from there. usage: $USAGE"
  [ -n "$RUN_REF" ] || die "--run is required: slice A's task, the api-key run whose review class this check reads. usage: $USAGE"
  [ -n "$CONTRAST_REF" ] || die "--contrast is required: the same task run again with a review stage account published. The two runs side by side are what this check is for, and one of them alone measures half of it. usage: $USAGE"
  local ref
  for ref in "$RUN_REF" "$CONTRAST_REF" "$REVIEW_REF"; do
    case "$ref" in
    */*/*) die "a run reference is <slug>/<run-id> or a bare run id, and '$ref' carries two slashes. usage: $USAGE" ;;
    esac
  done
}

main() {
  need_tools
  parse_args "$@"
  # The scratch directory first, so a refusal below is a row of the block: every
  # guard stops the run, and a row recorded before there is anywhere to record it
  # would leave the reader with a key that reads "not reached" for no reason.
  scratch_up
  guard_xtrace
  write_reader
  say "checking $SANDBOX: slice A $RUN_REF against $CONTRAST_REF${REVIEW_REF:+, slice B $REVIEW_REF}"
  if [ -n "$DRY_RUN" ]; then
    # The dry run names no project — reading one would be a fugaro call — so its
    # belong guard is over what you typed; the plan says that a real run reads
    # the project and guards over it.
    guard_belong "$SANDBOX" "$RUN_REF" "$CONTRAST_REF" "$REVIEW_REF" "$LOG_VIEW"
    fact dry-run PASS "guards and the plan only: nothing was read, no fugaro call and no gcloud call was made, and no verdict was taken"
    print_plan
    # A dry run measured nothing, so its block carries only what it did settle:
    # the belong guard over what was typed, the dry-run row and the Restore row
    # that says this check creates nothing. The keys a real run would fill read
    # as "not reached" for no reason here, and the plan above is the dry run's
    # own end, so the exit trap must not read it as a run that stopped.
    REACHED_END=yes
    BLOCK_KEYS=(belong dry-run restore)
    return 0
  fi
  step_project
  step_policy
  step_runs
  if [ -n "$RUN_ID" ]; then
    read_record "$RUN_KEY" run RUN
    read_calls "$RUN_EXEC" run RUN
  fi
  if [ -n "$CONTRAST_ID" ]; then
    read_record "$CONTRAST_KEY" contrast CONTRAST
  fi
  if [ -n "$REVIEW_ID" ]; then
    read_record "$REVIEW_KEY" review-run REVIEW
    read_calls "$REVIEW_EXEC" review-run REVIEW
  fi
  step_slice_a
  step_slice_b
  step_verdict
  print_owner_steps
  REACHED_END=yes
}

trap finish EXIT
trap on_error ERR
trap on_signal INT TERM HUP
main "$@"
