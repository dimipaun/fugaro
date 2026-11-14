#!/usr/bin/env bash
# Check 420 — pool failover on a real installation.
#
# Plan task MC10 writes this check; what it settles is docs/model-crafts/
# implementation-plan.md's "Checks 420, 421, 422" — Check 420's own sentence:
# "pool failover on a real installation: add two API-key credentials, exhaust one
# (a deliberately tiny cap at the provider), run, see the second serve and
# `craft_credentials` name it".
#
# What it measures, on YOUR installation and with one sandbox run:
#
#   * the feature itself: the build on PATH carries the model-crafts vocabulary
#     (`fugaro craft ls`, `fugaro accounts pool ls`, `fugaro project policy
#     show`, `fugaro creds ls`). A build without it stops here rather than
#     measuring a pool that cannot exist;
#   * the premise, from the installation's own words: the coder craft's
#     effective access names a POOL (`fugaro craft ls --json`), that pool is a
#     failover pool of two or more members the CREDENTIALS SERVICE serves
#     (`fugaro project policy show --json` — the node the service itself reads),
#     every member is in the service's bundle (`fugaro creds ls --json`), and a
#     pool listing a Claude OAuth subscription has the project's
#     `subscription_spread` switch on (D1622, D1626: with it off each
#     subscription member inside a mixed pool is stepped over);
#   * one sandbox run whose task is small, launched with `fugaro run --json` and
#     followed to its end;
#   * `result.json`, read from the runs bucket: the run ended succeeded, and its
#     `switches` log carries the move from the pool's first member to another —
#     which is the failover, and is the credentials service's own record
#     (C1.16, D140) rather than a reading this script made up;
#   * the credential the record names: `stage_accounts`/`oauth_account`, and the
#     record's additive `craft_credentials` (D1629), which MC8 ships. **This
#     build's record carries no `craft_credentials`**, so that row is a finding
#     until MC8 lands — and it is reported rather than hidden, because Check
#     420's own sentence is that the field names the member that served.
#
# **The pool is yours to declare.** This check adds no credential and writes no
# config: no command of this release stores a key under a credential's own name
# (`fugaro accounts add --type` takes claude-oauth alone), so the two API-key
# credentials Check 420's sentence names are entries you declare in the project
# configuration with the keys stored under their own names, and the pool that
# lists them is yours too. The FAIL rows say exactly which piece is missing.
#
# Usage:      bash scripts/live-checks/check420.sh --sandbox <a sandbox repository checkout> --yes
#             bash scripts/live-checks/check420.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check420.sh --help
# Needs:      bash, mktemp, date, python3 (the `--json` documents are read with
#             it), gcloud (authenticated as YOU, able to read the runs bucket)
#             and fugaro (the installed release: the whole point is the build you
#             have).
# Where:      your own machine and the **sandbox project** and the **sandbox
#             repository** only. A project or region naming belong is refused by
#             name, and so is a coding agent's session: every command this check
#             runs is one you could type yourself.
# Creates:    one sandbox run and the pull request it opens. On disk: one scratch
#             directory, removed at the end, holding the commands' output.
# Costs:      ONE sandbox run and what its task costs, on the sandbox's own
#             budget. Every other call is a read. `--yes` is required, and
#             CHECK420_MAX_MODEL_USD turns the figure into a criterion when you
#             set it.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check420_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. The run's pull request and its branch are
#             NOT closed by this script: the Restore row names them.
# Output:     progress on stderr, ONE block on stdout headed CHECK 420 RESULT,
#             then CHECK 420 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold, or a guard stopped the run. 2: the
#             check could not measure — a tool is missing, the checkout is not
#             one, a `--json` document could not be read, or the run did not end.
#             A key the run never reached reads `FAIL — not reached`.
#
# A NOTE ON WHAT IS NEVER PRINTED: no credential value reaches this script's
# stdout, its stderr, the block or the scratch directory. The rows carry
# credential NAMES, pool names, model IDs, statuses and dollars — never a value,
# and `fugaro creds ls` is the one command that could print one and does not.
#
# Knobs (all CHECK420_*): DRY_RUN, KEEP_SCRATCH, SANDBOX (the checkout, when you
# would rather set it in the environment), YES, REPO (the project configuration's
# key for the sandbox repository; the checkout directory's name by default), TASK
# and RECIPE (what the run is launched with), REGION (us-east5),
# RUN_TIMEOUT_SECONDS (3600), POLL_SECONDS (15), CLI_TIMEOUT_SECONDS (180),
# LAUNCH_TIMEOUT_SECONDS (600), DOCTOR_TIMEOUT_SECONDS (300), MAX_MODEL_USD
# (unset: the run's figure is read and not graded).
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: this check drives a real
# `fugaro` at a real terminal, and a trace of that is a second copy of what its
# own output holds.
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
  command -v bash >/dev/null 2>&1 || die "Check 420 needs bash"
  local t
  for t in mktemp date python3 gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 420 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk420-mine"
SCRATCH_REMOVED=no
FACTS_CAPTURED=""
CLEANUP_RUNNING=no
RESTORE_VERDICT=""
RESTORE_NOTE=""
PROJECT=""
CRAFT_ACCESS=""
CRAFT_MODEL=""
POOL_NAME=""
POOL_MEMBERS=""
POOL_SECOND=""
POOL_FIRST=""
RUN_ID=""
LAUNCH_REPO=""
RUN_REF=""
SETTLED=no
MODEL_USD=""
# PREMISE_OK says whether the whole premise holds: the pool step_pool read AND the
# bundle step_bundle read, each cleared by its own one branch. step_run reads it
# and nothing else: a run launched after a failed premise would spend the sandbox's
# money measuring a pool this check could not name, or one the credentials service
# cannot hand in full.
PREMISE_OK=no

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk420.XXXXXXXX") || die "Check 420 cannot make a scratch directory"
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
    say "CHECK420_KEEP_SCRATCH is set: $SCRATCH is kept"
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  if [ ! -e "$SCRATCH" ]; then SCRATCH_REMOVED=yes; fi
}

# The `--json` documents this check reads, in one program: a live check on the
# owner's machine has python3 and no jq, and a row that read a document with grep
# would report a credential name the release did not write the day the document
# is re-wrapped. Every reader prints `key=value` lines or `\t`-separated rows, so
# a shell can hold them without a second parser.
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


what = sys.argv[1]
doc = load(sys.argv[2])

if what == "doctor":
    print("project=%s" % ((doc.get("project") or {}).get("gcp_project") or ""))
elif what == "crafts":
    print("problems=%d" % len(doc.get("problems") or []))
    for problem in doc.get("problems") or []:
        print("problem\t%s" % flat(problem))
    for row in doc.get("crafts") or []:
        print("craft\t%s\t%s\t%s\t%s\t%s" % (
            row.get("craft") or "",
            flat(row.get("model")),
            flat(row.get("model_from")),
            flat(row.get("access")),
            yes(row.get("inert")),
        ))
elif what == "policy":
    pol = doc.get("policy")
    if not pol:
        print("published=false")
    else:
        print("published=true")
        print("spread=%s" % yes(pol.get("subscription_spread")))
        for name in ("coder", "review", "senior-review", "security-review"):
            craft = (pol.get("crafts") or {}).get(name) or {}
            print("craft\t%s\t%s\t%s" % (name, flat(craft.get("model")), flat(craft.get("access"))))
        for pool in pol.get("craft_pools") or []:
            print("pool\t%s\t%s\t%s" % (
                flat(pool.get("name")),
                flat(pool.get("strategy")),
                ",".join(pool.get("members") or []),
            ))
        for cred in pol.get("craft_credentials") or []:
            print("cred\t%s\t%s" % (flat(cred.get("name")), flat(cred.get("type"))))
elif what == "creds":
    rows = doc.get("credentials") or []
    print("count=%d" % len(rows))
    for row in rows:
        print("name\t%s" % flat(row.get("name")))
elif what == "launch":
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("repo=%s" % (doc.get("repo") or ""))
    print("status=%s" % (doc.get("status") or ""))
elif what == "rows":
    rows = doc.get("runs") or []
    print("count=%d" % len(rows))
    for row in rows:
        print("row\t%s\t%s\t%s\t%s\t%s" % (
            row.get("run_id") or "",
            row.get("status") or "",
            yes(row.get("terminal")),
            row.get("stage") or "",
            row.get("run") or "",
        ))
elif what == "record":
    cost = doc.get("cost") or {}
    print("run_id=%s" % (doc.get("run_id") or ""))
    print("repo=%s" % (doc.get("repo") or ""))
    print("status=%s" % (doc.get("status") or ""))
    print("outcome=%s" % (doc.get("outcome") or ""))
    print("failure_class=%s" % (doc.get("failure_class") or ""))
    print("reason=%s" % flat(doc.get("reason")))
    print("cost_usd=%s" % (cost.get("model_usd") or 0))
    print("oauth_account=%s" % flat(doc.get("oauth_account")))
    for stage, account in sorted((doc.get("stage_accounts") or {}).items()):
        print("stage_account\t%s\t%s" % (flat(stage), flat(account)))
    for switch in doc.get("switches") or []:
        print("switch\t%s\t%s\t%s" % (
            flat(switch.get("from")),
            flat(switch.get("to")),
            flat(switch.get("reason")),
        ))
    # The additive field D1629 names (MC8). It is read in whatever shape the
    # release writes it — a map of stage to credential name is the shape the
    # reconciliation gives it — and its ABSENCE is a reading of its own, so it is
    # printed as one rather than left to a caller's guess at an empty value.
    creds = doc.get("craft_credentials")
    print("craft_credentials=%s" % ("absent" if creds is None else "present"))
    if isinstance(creds, dict):
        for stage in sorted(creds):
            print("craft_credential\t%s\t%s" % (flat(stage), flat(creds[stage])))
    elif isinstance(creds, list):
        for entry in creds:
            print("craft_credential\t%s\t%s" % (
                flat((entry or {}).get("stage")),
                flat((entry or {}).get("credential") or (entry or {}).get("name")),
            ))
else:
    sys.stderr.write("read.py: unknown query %s\n" % what)
    sys.exit(2)
PY
  chmod 700 "$SCRATCH/read.py"
}

# read_json QUERY FILE DEST: answer one query into a file. The step that calls
# it reads the file next and decides there; this only says the document could not
# be read at all.
read_json() { # read_json QUERY FILE DEST
  python3 "$SCRATCH/read.py" "$1" "$2" >"$3" 2>"$3.err"
}

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1
}

# craft_field FILE CRAFT COLUMN — one column of a `craft\t<name>\t...` row.
craft_field() { # craft_field FILE NAME COLUMN
  awk -F'\t' -v name="$2" -v col="$3" '$1 == "craft" && $2 == name { print $col; exit }' "$1" 2>/dev/null || true
}

# pool_field FILE POOL COLUMN — one column of a `pool\t<name>\t...` row.
pool_field() { # pool_field FILE NAME COLUMN
  awk -F'\t' -v name="$2" -v col="$3" '$1 == "pool" && $2 == name { print $col; exit }' "$1" 2>/dev/null || true
}

# cred_type FILE NAME — the type the node gives that credential, "" when the
# node holds no credential of that name.
cred_type() { # cred_type FILE NAME
  awk -F'\t' -v name="$2" '$1 == "cred" && $2 == name { print $3; exit }' "$1" 2>/dev/null || true
}

# switch_to FILE MEMBER — the switch row whose `to` is MEMBER, as
# `from<TAB>reason`, "" when no switch landed on that member.
switch_to() { # switch_to FILE MEMBER
  awk -F'\t' -v member="$2" '$1 == "switch" && $3 == member { print $2 "\t" $4; exit }' "$1" 2>/dev/null || true
}

# row_field COLUMN: one column of the run this check launched, out of the last
# `fugaro runs ls --json` read. The run's own id is the only row that counts: a
# list read by position alone could be another run's.
row_field() { # row_field COLUMN
  awk -F'\t' -v id="$RUN_ID" -v col="$1" '$1 == "row" && $2 == id { print $col; exit }' "$SCRATCH/rows.txt" 2>/dev/null
}

# member_list LIST NAME — whether the comma-joined LIST holds NAME whole, rather
# than as the beginning of a longer name.
member_list() { # member_list LIST NAME
  case ",$1," in *",$2,"*) return 0 ;; esac
  return 1
}

# run_bounded SECONDS KEY DIR CMD...: run one command from the directory it has
# to be run in and under a bound. Every command here names the project through
# the checkout it is run in, and the record read is the one exception.
#
# The child execs, so the pid the bound kills IS the command: a hung cloud call
# would otherwise outlive the check that gave up on it, which is the one thing
# the bound exists to prevent. The ERR trap is dropped in it too: this check runs
# commands whose failing exit status is one of its own readings (a run that
# failed, a record that is not there), and a trap inherited here would turn each
# of them into "an internal error" and a status of 2 before the step could
# report it.
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
}

# ---------------------------------------------------------------- the guards
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check420.sh --sandbox <a sandbox repository checkout> --yes — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy the run's own output to the terminal"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can copy the run's output"
}

# guard_agent_session: the check is the owner's, and it drives commands whose
# writes a coding agent's session is refused (`fugaro creds ls` among them).
# Refusing here rather than letting each command refuse one at a time is what
# makes the block say why: a session that ran this far would report the refusals
# as findings about the release.
guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker="$name"; break; fi
  done
  if [ -n "$marker" ]; then
    stop "${marker} is set, so this is a coding agent's session: Check 420 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

guard_belong() {
  case "${ACCOUNT:-}${PROJECT:-}${REGION}" in
  *belong*) stop "the account, project or region this check would use names belong: this check is never run there" ;;
  esac
  fact belong PASS "the project this check reads is ${PROJECT:-the one the sandbox checkout belongs to} and the account it runs as is yours; nothing here names belong"
}

# step_features: the release gate. The model-crafts vocabulary is what every row
# below reads, and a build without it has no craft, no pool and no `craft ls` to
# read one from — so the check stops naming the command it is missing rather than
# measuring a pool that cannot exist.
step_features() {
  local missing="" c
  for c in "craft ls" "accounts pool ls" "project policy show" "creds ls"; do
    if ! "$FUGARO_BIN" $c --help >/dev/null 2>&1; then
      missing="fugaro $c"
      break
    fi
  done
  if [ -n "$missing" ]; then
    fact features FAIL "this build has no '${missing}': the model-crafts vocabulary (credentials, pools and crafts, docs/model-crafts/implementation-plan.md MC1–MC7) is not in the fugaro on PATH, so there is no pool for a run to fail over on. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again on the build it leaves you"
    stop "the installed fugaro has no '${missing}': this check needs the model-crafts vocabulary"
  fi
  fact features PASS "fugaro craft ls, accounts pool ls, project policy show and creds ls are all there: the build on PATH carries the crafts and pools this check reads"
}

# step_bound: the spend this check is about to commit, said before it commits
# it. `--yes` is the owner's answer to it; a dry run prints the same sentence and
# asks for nothing.
step_bound() {
  local extra=""
  if [ -n "$MAX_MODEL_USD" ]; then
    extra=" The run's own figure is graded against CHECK420_MAX_MODEL_USD=${MAX_MODEL_USD}: a run that spends more is a FAIL row, and the money is spent either way."
  fi
  fact bound PASS "ONE sandbox run and what its task costs, on the sandbox's own budget. Every other call this check makes is a read: craft ls, project policy show, creds ls, doctor, the runs list and the record.${extra} --yes is required to launch it"
}

# ---------------------------------------------------------------- the steps
# step_sandbox: the checkout the run is launched from, and the project it belongs
# to. The project comes from the checkout itself (`fugaro doctor --json`), so the
# check reads the project you actually work in rather than one it guessed.
step_sandbox() {
  if [ ! -d "$SANDBOX" ]; then
    die "--sandbox is not a directory: $SANDBOX (pass the checkout of a repository of the sandbox project)"
  fi
  if [ ! -f "$SANDBOX/fugaro.yaml" ]; then
    die "--sandbox carries no fugaro.yaml: $SANDBOX is not a Fugaro repository's checkout, and the run this check launches is launched from one"
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
  PROJECT=$(field "$SCRATCH/doctor.txt" project)
  if [ -z "$PROJECT" ]; then
    unmeasured sandbox "fugaro doctor --json answered without a project block: this check cannot say which project the sandbox checkout belongs to, and the rows below are about the project and the runs bucket. What it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  case "$PROJECT" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${PROJECT}: this check is never run there" ;;
  esac
  fact sandbox PASS "${SANDBOX} is a Fugaro repository's checkout, and it belongs to ${PROJECT}: the check launches from this checkout and reads its record in this project's bucket"
}

# step_crafts: the coder craft's effective access, from the command that answers
# "which credential or pool, and from where". The access has to be SET and to
# name something other than the built-in vertex, because the run below is a
# reading of a pool and a craft that resolves to the default credential has no
# pool in play.
step_crafts() {
  say "reading the crafts: fugaro craft ls --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" crafts "$SANDBOX" "$FUGARO_BIN" craft ls --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured crafts "fugaro craft ls --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): the coder craft's access is the pool this check reads the failover on, and without it there is nothing to look the pool up by. Its output is in $SCRATCH/crafts.out and $SCRATCH/crafts.err"
    return 0
  fi
  if ! read_json crafts "$SCRATCH/crafts.out" "$SCRATCH/crafts.txt"; then
    unmeasured crafts "fugaro craft ls --json printed a document this check could not read: what it printed is in $SCRATCH/crafts.out"
    return 0
  fi
  local problems
  problems=$(field "$SCRATCH/crafts.txt" problems)
  if [ "${problems:-0}" != 0 ]; then
    fact crafts FAIL "fugaro craft ls --json reports ${problems} problem(s): $(awk -F'\t' '$1 == "problem" { printf "%s; ", $2 }' "$SCRATCH/crafts.txt")— a craft list with problems on it is one where an access may not be the access a run would use, and the run below would be a reading of something this check cannot name. Settle them ('fugaro craft ls' prints the same problems as prose) and run this check again"
    return 0
  fi
  CRAFT_ACCESS=$(craft_field "$SCRATCH/crafts.txt" coder 5)
  CRAFT_MODEL=$(craft_field "$SCRATCH/crafts.txt" coder 3)
  if [ -z "$CRAFT_ACCESS" ]; then
    fact crafts FAIL "the coder craft resolves to no access at all (model=${CRAFT_MODEL:-none}): every craft that sets no access anywhere resolves to the project's default credential, which is the built-in vertex and not a pool, so the run below would measure nothing about failover. Declare the pool and point the craft at it ('fugaro accounts pool add <name> <member> <member>' then set agent.crafts.coder.access to that name) and run this check again"
    return 0
  fi
  if [ "$CRAFT_ACCESS" = vertex ]; then
    fact crafts FAIL "the coder craft's access is the built-in vertex credential: vertex is the project's own GCP service and no pool of it exists, so a run here measures nothing about failover. Point the craft at the pool you declared and run this check again"
    return 0
  fi
  fact crafts PASS "the coder craft's access is '${CRAFT_ACCESS}' (model ${CRAFT_MODEL:-none}, from $(craft_field "$SCRATCH/crafts.txt" coder 4)): the run below goes out on this access, and the rows that follow read the pool it names"
}

# step_pool: the premise, from the published node — the document the credentials
# service itself reads (`fugaro project policy show --json`), so this check reads
# what the service will read rather than a config file the merge may narrow.
#
# The shape it needs is the one D1634 gives failover: every member served by the
# CREDENTIALS SERVICE (a Claude OAuth subscription or an Anthropic key), because
# that is the only path that fails over inside a run — the runner path selects
# one member for a stage and cannot move to the next (F23, D1637) — and, where
# the pool lists a subscription, the project's subscription_spread switch on
# (D1622, D1626: with it off a subscription member inside a mixed pool is
# stepped over, and the pool is left with fewer candidates than it lists).
step_pool() {
  if [ -z "$CRAFT_ACCESS" ]; then
    unmeasured pool "there is no access to look a pool up by: the row above says why"
    return 0
  fi
  say "reading the published policy: fugaro project policy show --json"
  run_bounded "$CLI_TIMEOUT" policy "$SANDBOX" "$FUGARO_BIN" project policy show --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured pool "fugaro project policy show --json did not answer (exit ${BOUNDED_STATUS}): the pool's members and its strategy are in that node and nowhere else this check may read them, so the run below would be a reading nobody could grade. Its output is in $SCRATCH/policy.out and $SCRATCH/policy.err"
    return 0
  fi
  if ! read_json policy "$SCRATCH/policy.out" "$SCRATCH/policy.txt"; then
    unmeasured pool "fugaro project policy show --json printed a document this check could not read: what it printed is in $SCRATCH/policy.out"
    return 0
  fi
  if [ "$(field "$SCRATCH/policy.txt" published)" != true ]; then
    fact pool FAIL "no project policy is published for ${PROJECT}, so no pool is published either: a craft's access is read by the credentials service out of the published node, and with none published the service offers the whole bundle rather than the pool you declared. Publish it ('fugaro project publish') and run this check again"
    return 0
  fi
  local node_access strategy members first second spread typ
  node_access=$(craft_field "$SCRATCH/policy.txt" coder 4)
  strategy=$(pool_field "$SCRATCH/policy.txt" "$CRAFT_ACCESS" 3)
  members=$(pool_field "$SCRATCH/policy.txt" "$CRAFT_ACCESS" 4)
  spread=$(field "$SCRATCH/policy.txt" spread)
  if [ -z "$members" ] && [ -z "$strategy" ]; then
    typ=$(cred_type "$SCRATCH/policy.txt" "$CRAFT_ACCESS")
    if [ -n "$typ" ]; then
      fact pool FAIL "the coder craft's access '${CRAFT_ACCESS}' is a CREDENTIAL of type ${typ} and not a pool: a single credential has nothing to fail over TO, which is the whole of what this check measures. Declare a pool of two or more members ('fugaro accounts pool add <name> <member> <member>') and point the craft at the pool, then run this check again"
      return 0
    fi
    fact pool FAIL "the coder craft's access '${CRAFT_ACCESS}' is neither a pool nor a credential the published node holds: the node's crafts name ${node_access:-nothing} for the coder, and a name the node does not hold is one no run can resolve. Re-publish the node ('fugaro project publish') and run this check again"
    return 0
  fi
  POOL_NAME=$CRAFT_ACCESS
  POOL_MEMBERS=$members
  if [ "$strategy" != failover ]; then
    fact pool FAIL "the pool '${POOL_NAME}' walks its members with strategy '${strategy:-none}' (members: ${members}): Check 420 is about FAILOVER — a pool that moves to the next member when one is exhausted — and a round-robin pool shares the load out instead, which is Check 421's reading. Write the pool with --strategy failover and run this check again"
    return 0
  fi
  first=${members%%,*}
  second=${members#*,}
  if [ "$second" = "$members" ] || [ -z "$second" ]; then
    fact pool FAIL "the pool '${POOL_NAME}' lists one member (${members}): failover needs a second member to move TO, and a pool of one is the credential it names. Add the member you want the run to fall over onto ('fugaro accounts pool add ${POOL_NAME} <first> <second>') and run this check again"
    return 0
  fi
  POOL_FIRST=$first
  POOL_SECOND=${second%%,*}
  local served=yes why="" m t
  for m in ${members//,/ }; do
    t=$(cred_type "$SCRATCH/policy.txt" "$m")
    case "$t" in
    anthropic | claude-oauth) ;;
    "")
      served=no
      why="${why}${why:+; }${m} is not a credential the published node defines (its type is unknown, and the built-in vertex is not a member this path can serve)"
      ;;
    *)
      served=no
      why="${why}${why:+; }${m} is a ${t} credential, which a run's own gateway serves and the credentials service does not: that path SELECTS one member for a stage and cannot move to the next (F23, D1637), so a pool of it cannot fail over inside a run"
      ;;
    esac
    case "$t" in
    claude-oauth)
      if [ "$spread" != true ]; then
        served=no
        why="${why}${why:+; }${m} is a Claude OAuth subscription and the project's subscription_spread switch is off, so this service steps the subscription over inside a pool that also holds another kind (D1622, D1626) and the pool is left with fewer candidates than it lists"
      fi
      ;;
    esac
  done
  if [ "$served" != yes ]; then
    fact pool FAIL "the pool '${POOL_NAME}' (members: ${members}) is not a pool this release can fail over on: ${why}. Check 420 needs a failover pool of two or more members the credentials service serves — a Claude OAuth subscription (with subscription_spread on) or an Anthropic key — because that is the one path that moves to the next member inside a run. Settle the pool and run this check again"
    return 0
  fi
  PREMISE_OK=yes
  fact pool PASS "the pool '${POOL_NAME}' is a failover pool of ${members//,/, }, every member of it served by the credentials service (subscription_spread=${spread:-absent}): the run below goes out on this pool, and the first member — ${POOL_FIRST} — is the one whose cap at the provider you set tiny, so the second — ${POOL_SECOND} — is what a failover serves the run with"
}

# step_bundle: the service can only serve what its bundle holds. A member the
# bundle does not hold is a member the service cannot hand to a call at all, and
# the pool would be refused rather than failed over (credsvc's poolOfferingOf).
step_bundle() {
  if [ "$PREMISE_OK" != yes ]; then
    unmeasured bundle "there is no pool to check the members of: the row above says why"
    return 0
  fi
  # The bundle is part of the premise and not a row beside it: a pool the service
  # cannot hand in full is one a run would be refused on, and the run below would
  # spend the sandbox's money on a reading nobody could grade. It is cleared again
  # by the one branch that establishes the whole pool is held.
  PREMISE_OK=no
  say "reading the credentials service's bundle: fugaro creds ls --json (names and metadata, never a value)"
  run_bounded "$CLI_TIMEOUT" creds "$SANDBOX" "$FUGARO_BIN" creds ls --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured bundle "fugaro creds ls --json did not answer (exit ${BOUNDED_STATUS}): whether the service holds the pool's members is what decides whether the run below can be served by the pool at all. Its output is in $SCRATCH/creds.out and $SCRATCH/creds.err"
    return 0
  fi
  if ! read_json creds "$SCRATCH/creds.out" "$SCRATCH/creds.txt"; then
    unmeasured bundle "fugaro creds ls --json printed a document this check could not read: what it printed is in $SCRATCH/creds.out"
    return 0
  fi
  local missing="" m held
  # Held is read into a variable: a `${field …}` inside a double-quoted string is
  # a nested-quote substitution bash refuses at parse time.
  held=$(field "$SCRATCH/creds.txt" count)
  for m in ${POOL_MEMBERS//,/ }; do
    if ! awk -F'\t' -v name="$m" '$1 == "name" && $2 == name { found = 1 } END { exit !found }' "$SCRATCH/creds.txt"; then
      missing="${missing}${missing:+, }${m}"
    fi
  done
  if [ -n "$missing" ]; then
    fact bundle FAIL "the credentials service's bundle does not hold ${missing} (it holds ${held} credential(s): $(awk -F'\t' '$1 == "name" { printf "%s ", $2 }' "$SCRATCH/creds.txt")): the pool names a member the service cannot hand to a call, and the service refuses the call rather than failing over. Store each one's key under its own name ('fugaro creds set <member>') and run this check again"
    return 0
  fi
  PREMISE_OK=yes
  fact bundle PASS "every member of '${POOL_NAME}' is in the credentials service's bundle (${held} credential(s) held, read as names and metadata only): the service can hand each one to a call, which is what makes a failover onto the second possible"
}

# step_run: ONE sandbox run. It is launched only once the premise holds: a run
# launched after a failed premise would spend the sandbox's money on a reading
# nobody could grade.
step_run() {
  if [ -z "$PROJECT" ]; then
    unmeasured run-launched "there is no project to launch a run in: the row above says why"
    return 0
  fi
  if [ "$PREMISE_OK" != yes ]; then
    unmeasured run-launched "nothing was launched: the rows above did not establish the whole failover pool this run is a reading of, and a run launched without it would spend the sandbox's money on a reading nobody could grade. Settle the row named above and run this check again"
    return 0
  fi
  say "launching one run from ${SANDBOX} (a real sandbox run, with what its task costs)"
  run_bounded "$LAUNCH_TIMEOUT" launch "$SANDBOX" \
    "$FUGARO_BIN" run --repo "$SANDBOX" ${RECIPE:+--recipe "$RECIPE"} --json "$TASK"
  if [ "$BOUNDED_TIMED_OUT" = yes ]; then
    unmeasured run-launched "the launch did not finish inside ${LAUNCH_TIMEOUT}s and was ended: what it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if [ "$BOUNDED_STATUS" != 0 ]; then
    fact run-launched FAIL "fugaro run exited ${BOUNDED_STATUS} from ${SANDBOX}: the check needs a run to read a record from, and a launch that refuses says the sandbox's configuration is not one a run starts on. What it printed is in $SCRATCH/launch.out and $SCRATCH/launch.err"
    return 0
  fi
  if ! read_json launch "$SCRATCH/launch.out" "$SCRATCH/launch.txt"; then
    fact run-launched FAIL "fugaro run --json printed a document this check could not read: the launch is the step this check follows, and its own --json view is how the run is named. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  RUN_ID=$(field "$SCRATCH/launch.txt" run_id)
  LAUNCH_REPO=$(field "$SCRATCH/launch.txt" repo)
  if [ -z "$RUN_ID" ] || [ -z "$LAUNCH_REPO" ]; then
    fact run-launched FAIL "fugaro run --json named no run (run_id=${RUN_ID:-none}, repo=${LAUNCH_REPO:-none}): this check follows the run it launched, and a launch that does not name it cannot be followed. What it printed is in $SCRATCH/launch.out"
    return 0
  fi
  fact run-launched PASS "fugaro run launched ${RUN_ID} on ${LAUNCH_REPO} from the sandbox checkout (status $(field "$SCRATCH/launch.txt" status)): the run is the check's own, and the rows below follow this one and no other"
}

# step_terminal: the run has to end. A check that read a record while a run was
# still going would read the previous run's, and the record of a run that is
# still working is not the reading this check is after.
step_terminal() {
  if [ -z "$RUN_ID" ]; then
    unmeasured terminal "there is no run to follow: the row above says why"
    return 0
  fi
  say "waiting for ${RUN_ID} to end (up to ${RUN_TIMEOUT}s; fugaro runs ls --repo ${LAUNCH_REPO} --json --limit 1)"
  local waited=0 run_status="" ended="" stage=""
  while [ "$waited" -lt "$RUN_TIMEOUT" ]; do
    run_bounded "$CLI_TIMEOUT" rows "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$LAUNCH_REPO" --json --limit 1
    run_status="" ended="" stage=""
    if [ "$BOUNDED_STATUS" = 0 ] && read_json rows "$SCRATCH/rows.out" "$SCRATCH/rows.txt"; then
      run_status=$(row_field 3)
      ended=$(row_field 4)
      stage=$(row_field 5)
      RUN_REF=$(row_field 6)
      if [ "$ended" = true ]; then break; fi
    fi
    sleep "$POLL_SECONDS"
    waited=$((waited + POLL_SECONDS))
  done
  if [ "$ended" != true ]; then
    unmeasured terminal "${RUN_ID} had not ended after ${RUN_TIMEOUT}s (last row: status=${run_status:-unknown}, stage=${stage:-unknown}): this check reads a terminal run's own record, and reading one while the run is still working would read whatever was there before. Watch it with 'fugaro watch ${RUN_ID}' and run this check again when it has ended — the run it launched is a real one and is not cancelled by stopping here"
    return 0
  fi
  SETTLED=yes
  fact terminal PASS "${RUN_ID} ended in ${waited}s (status=${run_status:-unknown}, stage=${stage:-unknown}, read from fugaro runs ls --json): the run reached its end, which is the state the record below is written in"
}

# step_record: the record, read from the runs bucket as the owner would read it.
# The record's own run_id is what ties it to the run this check launched.
step_record() {
  if [ -z "$RUN_ID" ] || [ -z "$LAUNCH_REPO" ]; then
    unmeasured result-json "there is no run whose record could be read: the row above says why"
    return 0
  fi
  if [ "$SETTLED" != yes ]; then
    unmeasured result-json "the run ${RUN_ID} had not ended by the time the row above gave up, so this check did not read its record: a record read while a run is still working is whatever was there before it. Watch it with 'fugaro watch ${RUN_ID}' and run this check again when it has ended"
    return 0
  fi
  case "$RUN_REF" in
  */"$RUN_ID") ;;
  *)
    unmeasured result-json "fugaro runs ls --json named this run's row with no ref this check could take a bucket key from (run=${RUN_REF:-none}), so there was no key to read: the record of run ${RUN_ID} lives under the repository's slug, and the slug is what that row's ref carries"
    return 0
    ;;
  esac
  local key="runs/${RUN_REF}/result.json"
  say "reading the record: gcloud storage cat gs://fugaro-runs-${PROJECT}/${key}"
  run_bounded "$CLI_TIMEOUT" record "$SANDBOX" gcloud storage cat "gs://fugaro-runs-${PROJECT}/${key}" --project="${PROJECT}"
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    fact result-json FAIL "gs://fugaro-runs-${PROJECT}/${key} could not be read (exit ${BOUNDED_STATUS}): a run that ended has a record, and the record is where the run's switch log is written. What gcloud printed is in $SCRATCH/record.err"
    return 0
  fi
  if ! read_json record "$SCRATCH/record.out" "$SCRATCH/record.txt"; then
    fact result-json FAIL "${key} is not a document this check could read: what gcloud printed is in $SCRATCH/record.out"
    return 0
  fi
  local recorded
  recorded=$(field "$SCRATCH/record.txt" run_id)
  if [ "$recorded" != "$RUN_ID" ]; then
    fact result-json FAIL "${key} records run_id=${recorded} and this check launched ${RUN_ID}: the record at that key is another run's, and nothing below can be read as this run's"
    return 0
  fi
  MODEL_USD=$(field "$SCRATCH/record.txt" cost_usd)
  fact result-json PASS "${key} records ${RUN_ID} (status=$(field "$SCRATCH/record.txt" status), outcome=$(field "$SCRATCH/record.txt" outcome), model_usd=${MODEL_USD:-0}): the run this check launched reached its own record, read from the bucket as you would read it"
}

# step_succeeded: the run has to have passed. A run that failed mid-stage is not
# a reading of the failover — it is a reading of whatever stopped it — and the
# rows below it are left unmeasured rather than graded.
step_succeeded() {
  if [ ! -s "$SCRATCH/record.txt" ]; then
    unmeasured run-succeeded "there is no record to read the run's outcome from: the row above says why"
    return 0
  fi
  local status
  status=$(field "$SCRATCH/record.txt" status)
  if [ "$status" != succeeded ]; then
    fact run-succeeded FAIL "${RUN_ID} ended ${status:-unknown} (outcome=$(field "$SCRATCH/record.txt" outcome), failure_class=$(field "$SCRATCH/record.txt" failure_class), reason=$(field "$SCRATCH/record.txt" reason)): the failover below is read from a run that ran, and a run that stopped is a reading of whatever stopped it. Read it with 'fugaro diagnose ${RUN_ID}'"
    return 0
  fi
  fact run-succeeded PASS "${RUN_ID} ended succeeded (outcome=$(field "$SCRATCH/record.txt" outcome), model_usd=${MODEL_USD:-0}): the run reached its own end with the pool's calls answered, which is the state the failover row below is read in"
}

# step_budget: the money, graded only when you said what the run may cost. The
# figure is the record's own model cost, so the row is a reading of the bill and
# not of an estimate; without the knob it says what the run cost and grades
# nothing, which is what the bound row above promised.
step_budget() {
  if [ ! -s "$SCRATCH/record.txt" ]; then
    unmeasured budget "there is no record to read the run's cost from: the row above says why"
    return 0
  fi
  if [ -z "$MAX_MODEL_USD" ]; then
    fact budget INFO "${RUN_ID} cost ${MODEL_USD:-0} USD of model spend, read from its own record. CHECK420_MAX_MODEL_USD is not set, so this check does not grade the figure: set it to turn the cost into a criterion"
    return 0
  fi
  if [ -z "$MODEL_USD" ]; then
    unmeasured budget "${RUN_ID}'s record carries no cost figure, so there is nothing to grade against CHECK420_MAX_MODEL_USD=${MAX_MODEL_USD}: read the run with 'fugaro runs show ${RUN_ID} --json'"
    return 0
  fi
  if awk -v spent="$MODEL_USD" -v cap="$MAX_MODEL_USD" 'BEGIN { exit !(spent > cap) }'; then
    fact budget FAIL "${RUN_ID} cost ${MODEL_USD} USD of model spend and CHECK420_MAX_MODEL_USD=${MAX_MODEL_USD}: the run went over the figure you set, and the money is spent. Raise the knob if this run's task is simply worth more than you allowed, or point the pool at members whose models are cheaper, and run the check again"
    return 0
  fi
  fact budget PASS "${RUN_ID} cost ${MODEL_USD} USD of model spend, inside CHECK420_MAX_MODEL_USD=${MAX_MODEL_USD}: the run's own cost figure held to the bound you set"
}

# step_failover: THE row. The run's switch log (C1.16, D140) is the credentials
# service's own record of a call that moved from one credential to another, read
# back by the gateway off the answer and written into result.json — so a switch
# whose `to` is the pool's second member is the failover happening, in the
# service's words and not in this script's.
step_failover() {
  if [ ! -s "$SCRATCH/record.txt" ]; then
    unmeasured failover "there is no record to read a switch out of: the row above says why"
    return 0
  fi
  local switches to from_reason
  switches=$(awk -F'\t' '$1 == "switch" { n++ } END { print n + 0 }' "$SCRATCH/record.txt")
  to=$(switch_to "$SCRATCH/record.txt" "$POOL_SECOND")
  if [ -z "$to" ]; then
    if [ "$switches" = 0 ]; then
      fact failover FAIL "${RUN_ID}'s record carries NO switch at all (switches absent): the credentials service reports a switch on the answer of the call that moved, and a run with none never moved — the first member of '${POOL_NAME}' served every call. Check that the member the pool lists FIRST (${POOL_FIRST}) is the one whose cap you set tiny at the provider, that the run really reached the model that member serves, and that the provider's refusal is the one the service reads as exhaustion (a 429 or the account out of credit); then run this check again"
      return 0
    fi
    fact failover FAIL "${RUN_ID}'s record carries ${switches} switch(es) and none of them lands on '${POOL_SECOND}', the pool's second member (the log reads: $(awk -F'\t' '$1 == "switch" { printf "%s→%s(%s) ", $2, $3, $4 }' "$SCRATCH/record.txt")): the run moved, but not onto the member this check set out to see serve. Read the log above against the pool's order ('fugaro accounts pool ls') and run this check again"
    return 0
  fi
  from_reason=$(printf '%s' "$to")
  local from=${from_reason%%$'\t'*}
  local reason=${from_reason#*$'\t'}
  if [ "$from" = "$POOL_FIRST" ]; then
    fact failover PASS "${RUN_ID}'s record logs the switch ${from} → ${POOL_SECOND} (reason '${reason}', from the credentials service's own header, C1.16): the pool's first member was exhausted and the run was served by the second, which is the failover Check 420 measures"
    return 0
  fi
  fact failover PASS "${RUN_ID}'s record logs the switch ${from:-an unnamed credential} → ${POOL_SECOND} (reason '${reason}'): the call moved onto the pool's second member '${POOL_SECOND}', which is the failover Check 420 measures — the first member of the pool is '${POOL_FIRST}', so read the log beside the pool's order if you meant to exhaust that one"
}

# step_served: the credential the record NAMES. The run's own record carries the
# account a stage ran as (`stage_accounts`, D876) and the last credential that
# answered (`oauth_account`, C1.19) — so a record naming the pool's second member
# is the same reading as the switch above, from the record's own fields rather
# than from the service's header.
step_served() {
  if [ ! -s "$SCRATCH/record.txt" ]; then
    unmeasured served "there is no record to read the serving credential from: the row above says why"
    return 0
  fi
  local oauth stages named=""
  oauth=$(field "$SCRATCH/record.txt" oauth_account)
  stages=$(awk -F'\t' '$1 == "stage_account" { printf "%s=%s ", $2, $3 }' "$SCRATCH/record.txt")
  member_list "$POOL_MEMBERS" "$oauth" && named="$oauth"
  if [ -z "$named" ]; then
    local s
    while IFS=$'\t' read -r kind stage account; do
      [ "$kind" = stage_account ] || continue
      if member_list "$POOL_MEMBERS" "$account"; then named="$account"; break; fi
    done <"$SCRATCH/record.txt"
  fi
  if [ -z "$named" ]; then
    fact served FAIL "${RUN_ID}'s record names no member of '${POOL_NAME}' (oauth_account=${oauth:-none}, stage_accounts: ${stages:-none}): the record's own fields are where the credential that served a run is written, and a run whose fields name nothing the pool holds is one this check cannot tie to the pool at all. Read it with 'fugaro runs show ${RUN_ID} --json' and 'fugaro diagnose ${RUN_ID}'"
    return 0
  fi
  if [ "$named" = "$POOL_SECOND" ]; then
    fact served PASS "${RUN_ID}'s record names '${named}' — the pool's SECOND member — as the credential that served it (oauth_account=${oauth:-none}, stage_accounts: ${stages:-none}): the failover above is visible in the record's own fields and not only in the service's header"
    return 0
  fi
  fact served PASS "${RUN_ID}'s record names '${named}', a member of '${POOL_NAME}' (oauth_account=${oauth:-none}, stage_accounts: ${stages:-none}): the run was served by a member of the pool. The second member is '${POOL_SECOND}' — the switch row above says which one the failover landed on"
}

# step_craft_credentials: Check 420's own last clause — "see the second serve and
# `craft_credentials` name it". D1629 gives the run record an additive
# `craft_credentials` (stage → the credential that served it) beside
# `stage_accounts`, and MC8 is the task that writes it. A build without the field
# is a finding rather than a pass: the check's sentence names the field, and the
# alternative — reading `stage_accounts` and calling it `craft_credentials` —
# would be this script asserting a field the release does not have.
step_craft_credentials() {
  if [ ! -s "$SCRATCH/record.txt" ]; then
    unmeasured craft-credentials "there is no record to read the field out of: the row above says why"
    return 0
  fi
  local present rows
  present=$(field "$SCRATCH/record.txt" craft_credentials)
  rows=$(awk -F'\t' '$1 == "craft_credential" { printf "%s=%s ", $2, $3 }' "$SCRATCH/record.txt")
  if [ "$present" != present ]; then
    fact craft-credentials FAIL "${RUN_ID}'s record carries no craft_credentials: Check 420's sentence is to see the second member serve AND craft_credentials name it, and D1629's additive field (stage → the credential that served it) is not in this build's record — MC8 is the task that writes it. The two rows above are the same reading out of the fields this release has (the service's switch header and stage_accounts/oauth_account); this row holds once the field ships"
    return 0
  fi
  if [ -z "$rows" ]; then
    fact craft-credentials FAIL "${RUN_ID}'s record carries craft_credentials as an empty block: the field is there and names no credential, so the run that failed over onto '${POOL_SECOND}' recorded nothing about it. Read it with 'fugaro runs show ${RUN_ID} --json'"
    return 0
  fi
  case "$rows" in
  *"=${POOL_SECOND} "* | *"=${POOL_SECOND}")
    fact craft-credentials PASS "${RUN_ID}'s craft_credentials names the pool's second member '${POOL_SECOND}' (${rows}): the credential that served the run is in the record's own additive field, which is what Check 420's sentence asks for"
    ;;
  *)
    fact craft-credentials FAIL "${RUN_ID}'s craft_credentials names no member of the pool the failover landed on (it reads: ${rows}; the pool is ${POOL_MEMBERS//,/, } and the second member is ${POOL_SECOND}): the field is there and its content disagrees with the switch log above, which is the reading Check 420 exists to catch"
    ;;
  esac
}

# restore: what this check changed, said rather than assumed.
restore() {
  RESTORE_VERDICT=PASS
  local run_note="Nothing was launched, so there is no run and no pull request to clean up after."
  if [ -n "$RUN_ID" ]; then
    run_note="The run ${RUN_ID} is a real sandbox run and it opened a pull request: close that pull request and delete its branch yourself once you have read the run — this check does not do it for you, because the pull request is the run's own work and may be worth reading first."
  fi
  RESTORE_NOTE="this check wrote no config, no pool and no credential: it read the installation's own crafts, published node and bundle, and launched at most one run. ${run_note} The pool, its members and their keys were yours before this check and are unchanged by it. The scratch directory held the commands' output"
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
  say "CHECK 420 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [offline] fugaro craft ls --help, accounts pool ls --help, project policy show --help, creds ls --help — the model-crafts vocabulary must be in the build"
  say "  [network, read] fugaro doctor --json in ${SANDBOX} — the project this checkout belongs to"
  say "  [read] fugaro craft ls --json — the coder craft's effective access"
  say "  [network, read] fugaro project policy show --json — the published node: the pool's members and strategy, and subscription_spread"
  say "  [network, read] fugaro creds ls --json — the credential names the service's bundle holds (never a value)"
  say "  [network, BILLABLE] fugaro run --repo ${SANDBOX}${RECIPE:+ --recipe ${RECIPE}} --json \"${TASK}\" — ONE sandbox run, followed with fugaro runs ls --json until it is terminal"
  say "  [read] gcloud storage cat gs://fugaro-runs-<the project>/runs/<the repository's slug>/<run>/result.json — the record: its switch log, its stage_accounts and its craft_credentials"
  say "  [nothing else] no config, no pool, no credential and no policy is written; the scratch directory is removed at the end"
  fact bound PASS "planned: ONE sandbox run and what its task costs. --dry-run runs nothing, and a real run requires --yes"
  for key in features sandbox crafts pool bundle run-launched terminal result-json \
    run-succeeded budget failover served craft-credentials; do
    fact "$key" INFO "planned: a real installation and one run measure this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no doctor, no craft read, no policy read, no bundle read, no run and no record"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version features xtrace agent-session belong bound sandbox
  crafts pool bundle run-launched terminal result-json run-succeeded budget
  failover served craft-credentials restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 420 RESULT\n' >&3
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
  printf 'CHECK 420 RESULT END (exit %s)\n' "$status" >&3
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
RECIPE_CLI=""
YES_CLI=""
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --sandbox)
    [ $# -ge 2 ] || die "--sandbox needs a path: the checkout of a repository of the sandbox project, which is what the run is launched from"
    SANDBOX_CLI=$2
    shift 2
    ;;
  --task)
    [ $# -ge 2 ] || die "--task needs the text the sandbox run is launched with"
    TASK_CLI=$2
    shift 2
    ;;
  --recipe)
    [ $# -ge 2 ] || die "--recipe needs a recipe name (agent, default, review, ...)"
    RECIPE_CLI=$2
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

SANDBOX=${CHECK420_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 420 needs --sandbox: the checkout of a repository of the sandbox project. It is what the run is launched from and what its crafts are read through. Usage: bash scripts/live-checks/check420.sh --sandbox <a sandbox repository checkout> --yes"
fi
# Absolute, before anything changes directory: every command below is run from
# somewhere else, and a relative path would name a different repository there.
case "$SANDBOX" in
/*) ;;
*) SANDBOX="$PWD/$SANDBOX" ;;
esac
# The project configuration's key for this repository, and the name
# `fugaro runs ls --json` lists it under: the checkout directory's own name by
# default, and the knob exists because the two need not agree.
REPO=${CHECK420_REPO:-${SANDBOX##*/}}
TASK=${CHECK420_TASK:-${TASK_CLI:-"Reply with one line saying the repository is reachable. Do not change any file."}}
RECIPE=${CHECK420_RECIPE:-${RECIPE_CLI:-}}
MAX_MODEL_USD=${CHECK420_MAX_MODEL_USD:-}
YES=${CHECK420_YES:-$YES_CLI}
DRY_RUN=${CHECK420_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK420_KEEP_SCRATCH:-}
REGION=${CHECK420_REGION:-us-east5}
RUN_TIMEOUT=${CHECK420_RUN_TIMEOUT_SECONDS:-3600}
POLL_SECONDS=${CHECK420_POLL_SECONDS:-15}
CLI_TIMEOUT=${CHECK420_CLI_TIMEOUT_SECONDS:-180}
LAUNCH_TIMEOUT=${CHECK420_LAUNCH_TIMEOUT_SECONDS:-600}
DOCTOR_TIMEOUT=${CHECK420_DOCTOR_TIMEOUT_SECONDS:-300}
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
  [ -n "$FUGARO_BIN" ] || die "Check 420 needs 'fugaro' on PATH: the check drives the build you have installed, and it is not there"
  scratch_up
  trap on_error ERR
  write_reader
  guard_xtrace
  guard_agent_session
  guard_belong
  fact fugaro-version INFO "$($FUGARO_BIN version 2>/dev/null | head -n 1 || true)"
  step_features
  step_bound
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  if [ -z "$YES" ]; then
    die "Check 420 launches ONE sandbox run and spends what its task costs, so it asks for --yes. Nothing has been launched. Type: bash scripts/live-checks/check420.sh --sandbox ${SANDBOX} --yes (or read the plan first with --dry-run)"
  fi
  step_sandbox
  step_crafts
  step_pool
  step_bundle
  step_run
  step_terminal
  step_record
  step_succeeded
  step_budget
  step_failover
  step_served
  step_craft_credentials
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
