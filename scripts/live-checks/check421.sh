#!/usr/bin/env bash
# Check 421 — round-robin spread over N runs.
#
# Plan task MC10 writes this check; what it settles is docs/model-crafts/
# implementation-plan.md's "Checks 420, 421, 422" — Check 421's own sentence:
# "round-robin spread over N runs". MC7 is the task behind it, and its red step
# is the reading this script takes: "three selections must not all pick member
# 1".
#
# What it measures, on YOUR installation and with N sandbox runs (two by
# default):
#
#   * the feature itself: the build on PATH carries the model-crafts vocabulary
#     (`fugaro craft ls`, `fugaro accounts pool ls`, `fugaro project policy
#     show`, `fugaro creds ls`). A build without it stops here rather than
#     measuring a pool that cannot exist;
#   * the premise, from the installation's own words. The coder craft's
#     effective access names a pool (`fugaro craft ls --json`), that pool's
#     strategy is `round-robin` and it lists two or more members the credentials
#     service serves (`fugaro project policy show --json` — the node the service
#     itself reads), every member is in the service's bundle (`fugaro creds ls
#     --json`), and — MC7's own rule, internal/credsvc/roundrobin.go — NO craft
#     the service serves is pinned: a project whose crafts go out on two
#     different pools hands one call one candidate list, so the rotation is taken
#     up only when every craft that contributes to it goes out on a pool asking
#     for it. A craft on the built-in vertex or on an OpenRouter key contributes
#     nothing to that list and is named as such;
#   * N sandbox runs whose task is small, each launched with `fugaro run --json`
#     and followed to its end;
#   * each run's `result.json`, read from the runs bucket, and the credential it
#     names as the one that served it (`craft_credentials` when the build writes
#     it — D1629, MC8 — else `oauth_account`, `stage_accounts` or the switch log);
#   * THE reading: over the N runs, at least two different members of the pool
#     served one run each. One member serving every run is the pool NOT rotating,
#     which is the state MC7 exists to remove.
#
# **The pool is yours to declare.** This check writes no config and no
# credential: it reads the installation's own crafts, published node and bundle,
# and launches the N runs. The header of check420.sh says why (no command of this
# release stores a key under a credential's own name); the FAIL rows say exactly
# which piece is missing.
#
# Usage:      bash scripts/live-checks/check421.sh --sandbox <a sandbox repository checkout> --yes
#             bash scripts/live-checks/check421.sh --sandbox <path> --runs 3 --yes
#             bash scripts/live-checks/check421.sh --sandbox <path> --dry-run
#             bash scripts/live-checks/check421.sh --help
# Needs:      bash, mktemp, date, python3 (the `--json` documents are read with
#             it), gcloud (authenticated as YOU, able to read the runs bucket)
#             and fugaro (the installed release: the whole point is the build you
#             have).
# Where:      your own machine and the **sandbox project** and the **sandbox
#             repository** only. A project or region naming belong is refused by
#             name, and so is a coding agent's session: every command this check
#             runs is one you could type yourself.
# Creates:    N sandbox runs (two by default) and the pull requests they open. On
#             disk: one scratch directory, removed at the end, holding the
#             commands' output.
# Costs:      N sandbox runs and what their tasks cost, on the sandbox's own
#             budget. Every other call is a read. `--yes` is required, and
#             CHECK421_MAX_MODEL_USD turns the TOTAL figure into a criterion when
#             you set it.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check421_test.sh) drives it against fake tools only.
# Cleanup:    the scratch directory, removed from the exit trap even on a failed
#             assertion or a Ctrl-C. The runs' pull requests and their branches
#             are NOT closed by this script: the Restore row names them.
# Output:     progress on stderr, ONE block on stdout headed CHECK 421 RESULT,
#             then CHECK 421 RESULT END (exit N). Exit 0: every criterion held.
#             1: a criterion did not hold, or a guard stopped the run. 2: the
#             check could not measure — a tool is missing, the checkout is not
#             one, a `--json` document could not be read, or a run did not end.
#             A key the run never reached reads `FAIL — not reached`.
#
# A NOTE ON WHAT IS NEVER PRINTED: no credential value reaches this script's
# stdout, its stderr, the block or the scratch directory. The rows carry
# credential NAMES, pool names, model IDs, statuses and dollars — never a value,
# and `fugaro creds ls` is the one command that could print one and does not.
#
# Knobs (all CHECK421_*): DRY_RUN, KEEP_SCRATCH, SANDBOX (the checkout, when you
# would rather set it in the environment), YES, REPO (the project configuration's
# key for the sandbox repository; the checkout directory's name by default), TASK
# and RECIPE (what each run is launched with), RUNS (how many runs the spread is
# read over: 2 or more), REGION (us-east5), RUN_TIMEOUT_SECONDS (3600, PER RUN),
# POLL_SECONDS (15), CLI_TIMEOUT_SECONDS (180), LAUNCH_TIMEOUT_SECONDS (600),
# DOCTOR_TIMEOUT_SECONDS (300), MAX_MODEL_USD (unset: the runs' figure is read
# and not graded).
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

# recorded KEY — whether a row for that key is already in the block. The batch
# steps write their own row where the failure is theirs (a run that never ended is
# the end row's, a record that could not be read is the record row's), and a step
# that finds the row already taken leaves it rather than writing a second one
# about the same failure.
recorded() { # recorded KEY
  grep -q "^$1|" "$FACTS_FILE" 2>/dev/null
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
  command -v bash >/dev/null 2>&1 || die "Check 421 needs bash"
  local t
  for t in mktemp date python3 gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || die "Check 421 needs '$t' and it is not on PATH"
  done
}

# ------------------------------------------------------------- scratch + trap
SCRATCH=""
SCRATCH_MARKER=".chk421-mine"
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
# The runs this check launches, one row each, in the order they were launched:
# run id, the repository the launch named, the status the record ended with, the
# credential it names, and where that credential was read from. The rows below are
# readings of this file, so a case can see exactly which run gave which answer.
RUNS_ROWS=""
LAUNCHED=0
ENDED=0
RECORDS=0
SUCCEEDED=0
TOTAL_USD=0
# The crafts the published node will serve this call with, one `<name>=<access>`
# per craft as `fugaro craft ls --json` reports them: step_pool walks them to
# answer MC7's own question, whether ANY craft the credentials service serves is
# pinned to something that is not a round-robin pool.
CRAFT_TABLE=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk421.XXXXXXXX") || die "Check 421 cannot make a scratch directory"
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
    say "CHECK421_KEEP_SCRATCH is set: $SCRATCH is kept"
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

# run_row ID COLUMN: one column of one run, out of the last `fugaro runs ls
# --json` read. The run's own id is the only row that counts: a list read by
# position alone could be another run's, and this check follows N of them.
run_row() { # run_row ID COLUMN
  awk -F'\t' -v id="$1" -v col="$2" '$1 == "row" && $2 == id { print $col; exit }' "$SCRATCH/rows.txt" 2>/dev/null
}

# member_of FILE: the credential a run's record names as the one that served it,
# as `source<TAB>member`, "" when it names no member of the pool. The record's own
# fields are the reading, in the order of how directly each one answers it: MC8's
# craft_credentials (D1629) when the build writes it, then the account the run
# ended as, then the accounts its stages ran as, then the far end of the switch
# log.
member_of() { # member_of FILE
  awk -F'\t' -v members=",$POOL_MEMBERS," '
    function member(x) { return x != "" && index(members, "," x ",") > 0 }
    $1 == "craft_credential" && cc == "" && member($3) { cc = $3 }
    $1 ~ /^oauth_account=/ { split($1, a, "="); if (oa == "" && member(a[2])) oa = a[2] }
    $1 == "stage_account" && sa == "" && member($3) { sa = $3 }
    $1 == "switch" && sw == "" && member($3) { sw = $3 }
    END {
      if (cc != "") { print "craft_credentials\t" cc; exit }
      if (oa != "") { print "oauth_account\t" oa; exit }
      if (sa != "") { print "stage_accounts\t" sa; exit }
      if (sw != "") { print "switch\t" sw; exit }
    }
  ' "$1" 2>/dev/null || true
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
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check421.sh --sandbox <a sandbox repository checkout> --yes — the check prints its own RESULT block, so a trace is not needed to report a failure, and a trace of this run would copy the run's own output to the terminal"
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
    stop "${marker} is set, so this is a coding agent's session: Check 421 is yours to run at your own terminal, on the sandbox only, and no agent runs any step of it"
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
    fact features FAIL "this build has no '${missing}': the model-crafts vocabulary (credentials, pools and crafts, docs/model-crafts/implementation-plan.md MC1–MC7) is not in the fugaro on PATH, so there is no pool for the runs below to rotate over. Run 'fugaro update' at your terminal — that one command is the whole upgrade — and run this check again on the build it leaves you"
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
    extra=" The run's own figure is graded against CHECK421_MAX_MODEL_USD=${MAX_MODEL_USD}: a run that spends more is a FAIL row, and the money is spent either way."
  fi
  fact bound PASS "${RUNS} sandbox runs, one after another, and what their tasks cost, on the sandbox's own budget. Every other call this check makes is a read: craft ls, project policy show, creds ls, doctor, the runs list and the runs' records.${extra} --yes is required to launch them"
}

# ---------------------------------------------------------------- the steps
# step_sandbox: the checkout the runs are launched from, and the project it
# belongs to. The project comes from the checkout itself (`fugaro doctor --json`),
# so the check reads the project you actually work in rather than one it guessed.
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
  PROJECT=$(field "$SCRATCH/doctor.txt" project)
  if [ -z "$PROJECT" ]; then
    unmeasured sandbox "fugaro doctor --json answered without a project block: this check cannot say which project the sandbox checkout belongs to, and the rows below are about the project and the runs bucket. What it printed is in $SCRATCH/doctor.out"
    return 0
  fi
  case "$PROJECT" in
  *belong*) stop "the checkout at $SANDBOX belongs to ${PROJECT}: this check is never run there" ;;
  esac
  fact sandbox PASS "${SANDBOX} is a Fugaro repository's checkout, and it belongs to ${PROJECT}: the check launches ${RUNS} runs from this checkout and reads their records in this project's bucket"
}

# step_crafts: every craft's effective access, from the command that answers
# "which credential or pool, and from where". The coder's has to be SET and to
# name something other than the built-in vertex, because the runs below are a
# reading of a pool; the others are read because MC7's own rule needs them (a
# craft this service serves that is pinned stops the rotation).
step_crafts() {
  say "reading the crafts: fugaro craft ls --json, in ${SANDBOX}"
  run_bounded "$CLI_TIMEOUT" crafts "$SANDBOX" "$FUGARO_BIN" craft ls --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured crafts "fugaro craft ls --json did not answer in ${SANDBOX} (exit ${BOUNDED_STATUS}): the coder craft's access is the pool this check reads the spread on, and without it there is nothing to look the pool up by. Its output is in $SCRATCH/crafts.out and $SCRATCH/crafts.err"
    return 0
  fi
  if ! read_json crafts "$SCRATCH/crafts.out" "$SCRATCH/crafts.txt"; then
    unmeasured crafts "fugaro craft ls --json printed a document this check could not read: what it printed is in $SCRATCH/crafts.out"
    return 0
  fi
  local problems
  problems=$(field "$SCRATCH/crafts.txt" problems)
  if [ "${problems:-0}" != 0 ]; then
    fact crafts FAIL "fugaro craft ls --json reports ${problems} problem(s): $(awk -F'\t' '$1 == "problem" { printf "%s; ", $2 }' "$SCRATCH/crafts.txt")— a craft list with problems on it is one where an access may not be the access a run would use, and the runs below would be a reading of something this check cannot name. Settle them ('fugaro craft ls' prints the same problems as prose) and run this check again"
    return 0
  fi
  CRAFT_ACCESS=$(craft_field "$SCRATCH/crafts.txt" coder 5)
  CRAFT_MODEL=$(craft_field "$SCRATCH/crafts.txt" coder 3)
  CRAFT_TABLE=$(awk -F'\t' '$1 == "craft" { printf "%s=%s ", $2, ($5 == "" ? "none" : $5) }' "$SCRATCH/crafts.txt")
  if [ -z "$CRAFT_ACCESS" ]; then
    fact crafts FAIL "the coder craft resolves to no access at all (model=${CRAFT_MODEL:-none}): every craft that sets no access anywhere resolves to the project's default credential, which is the built-in vertex and not a pool, so the runs below would measure nothing about a rotation. Declare the round-robin pool and point the craft at it ('fugaro accounts pool add <name> <member> <member> --strategy round-robin') and run this check again"
    return 0
  fi
  if [ "$CRAFT_ACCESS" = vertex ]; then
    fact crafts FAIL "the coder craft's access is the built-in vertex credential: vertex is the project's own GCP service, no pool of it exists, and it is the runner path's rather than the credentials service's, so the runs below measure nothing about a rotation. Point the craft at the round-robin pool you declared and run this check again"
    return 0
  fi
  fact crafts PASS "the coder craft's access is '${CRAFT_ACCESS}' (model ${CRAFT_MODEL:-none}, from $(craft_field "$SCRATCH/crafts.txt" coder 4)); every craft reads: ${CRAFT_TABLE}— the runs below go out on this access, and the rows that follow read the pool it names"
}

# step_pool: the premise, from the published node — the document the credentials
# service itself reads (`fugaro project policy show --json`), so this check reads
# what the service will read rather than a config file the merge may narrow.
#
# Two things have to hold. The coder's pool has to be a ROUND-ROBIN pool of two or
# more members the credentials service serves (D1634: a Claude OAuth subscription
# with the project's subscription_spread switch on, or an Anthropic key — the
# runner path's members never reach this service). And no craft this service
# serves may be pinned: MC7's rule (internal/credsvc/roundrobin.go) is that the
# rotation is taken up only when EVERY craft that contributed a candidate goes out
# on a pool asking for it, because one candidate list is the union of the four
# crafts' accesses and rotating it would apply one pool's strategy to another
# pool's members. A craft on the built-in vertex or on an OpenRouter key
# contributes nothing to that list and stops nothing.
step_pool() {
  if [ -z "$CRAFT_ACCESS" ]; then
    unmeasured pool "there is no access to look a pool up by: the row above says why"
    return 0
  fi
  say "reading the published policy: fugaro project policy show --json"
  run_bounded "$CLI_TIMEOUT" policy "$SANDBOX" "$FUGARO_BIN" project policy show --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured pool "fugaro project policy show --json did not answer (exit ${BOUNDED_STATUS}): the pool's members, its strategy and the other crafts' accesses are in that node and nowhere else this check may read them, so the runs below would be a reading nobody could grade. Its output is in $SCRATCH/policy.out and $SCRATCH/policy.err"
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
  local strategy members spread typ first second
  strategy=$(pool_field "$SCRATCH/policy.txt" "$CRAFT_ACCESS" 3)
  members=$(pool_field "$SCRATCH/policy.txt" "$CRAFT_ACCESS" 4)
  spread=$(field "$SCRATCH/policy.txt" spread)
  if [ -z "$members" ] && [ -z "$strategy" ]; then
    typ=$(cred_type "$SCRATCH/policy.txt" "$CRAFT_ACCESS")
    if [ -n "$typ" ]; then
      fact pool FAIL "the coder craft's access '${CRAFT_ACCESS}' is a CREDENTIAL of type ${typ} and not a pool: a single credential has no members to rotate between, which is the whole of what this check measures. Declare the pool of two or more members ('fugaro accounts pool add <name> <member> <member> --strategy round-robin') and point the craft at the pool, then run this check again"
      return 0
    fi
    fact pool FAIL "the coder craft's access '${CRAFT_ACCESS}' is neither a pool nor a credential the published node holds: a name the node does not hold is one no run can resolve. Re-publish the node ('fugaro project publish') and run this check again"
    return 0
  fi
  if [ "$strategy" != round-robin ]; then
    fact pool FAIL "the pool '${CRAFT_ACCESS}' walks its members with strategy '${strategy:-failover}' (members: ${members}): Check 421 is about the ROTATION — the pool sharing its runs out over its members — and a failover pool hands every run the first member that answers, which is Check 420's reading. Write the pool with --strategy round-robin ('fugaro accounts pool add ${CRAFT_ACCESS} <member> <member> --strategy round-robin') and run this check again"
    return 0
  fi
  POOL_NAME=$CRAFT_ACCESS
  POOL_MEMBERS=$members
  first=${members%%,*}
  second=${members#*,}
  if [ "$second" = "$members" ] || [ -z "$second" ]; then
    fact pool FAIL "the pool '${POOL_NAME}' lists one member (${members}): a rotation over one member serves every run from it, so ${RUNS} runs here would be a reading of nothing. Add the member the runs should be spread over and run this check again"
    return 0
  fi
  POOL_FIRST=$first
  POOL_SECOND=${second%%,*}
  local why="" m t
  for m in ${members//,/ }; do
    t=$(cred_type "$SCRATCH/policy.txt" "$m")
    case "$t" in
    anthropic) ;;
    claude-oauth)
      if [ "$spread" != true ]; then
        why="${why}${why:+; }${m} is a Claude OAuth subscription and the project's subscription_spread switch is off, so this service steps the subscription over inside a pool that also holds another kind (D1622, D1626) and the pool is left with fewer candidates than it lists"
      fi
      ;;
    "")
      why="${why}${why:+; }${m} is not a credential the published node defines (its type is unknown, and the built-in vertex is not a member this path can serve)"
      ;;
    *)
      why="${why}${why:+; }${m} is a ${t} credential, which a run's own gateway serves and the credentials service does not (D1634), so it never reaches the counter this check reads a rotation from"
      ;;
    esac
  done
  if [ -n "$why" ]; then
    fact pool FAIL "the pool '${POOL_NAME}' (members: ${members}) is not a pool this release can rotate over: ${why}. Check 421 needs a round-robin pool of two or more members the credentials service serves — a Claude OAuth subscription (with subscription_spread on) or an Anthropic key — because that is the path whose selection the counter decides. Settle the pool and run this check again"
    return 0
  fi
  # MC7's own rule, walked over the crafts this check read: a craft this service
  # serves that is pinned to a credential, or to a pool of the other strategy,
  # makes the call's whole candidate list pinned with it.
  local pinned="" name access ptyp pstrategy quiet="" inert
  while IFS=$'\t' read -r kind name _ _ access inert; do
    [ "$kind" = craft ] || continue
    case "$access" in
    "" | vertex) quiet="${quiet}${quiet:+, }${name}=${access:-none}"; continue ;;
    esac
    # An inert craft is one whose access serves nothing at all (a pool of
    # subscriptions with the project's spread switch off, D1622): it contributes
    # no candidate and pins nothing, and it is named rather than counted.
    if [ "$inert" = true ]; then
      quiet="${quiet}${quiet:+, }${name}=${access} (inert)"
      continue
    fi
    # A pool is looked up FIRST: it is not a credential, so a credential lookup
    # would leave the pool the call is served by in the list below as though the
    # node did not hold it.
    pstrategy=$(pool_field "$SCRATCH/policy.txt" "$access" 3)
    if [ -n "$pstrategy" ] || [ -n "$(pool_field "$SCRATCH/policy.txt" "$access" 4)" ]; then
      [ "$pstrategy" = round-robin ] || pinned="${pinned}${pinned:+, }${name} goes out on the pool ${access}, whose strategy is ${pstrategy:-failover}"
      continue
    fi
    ptyp=$(cred_type "$SCRATCH/policy.txt" "$access")
    if [ -n "$ptyp" ]; then
      case "$ptyp" in
      anthropic | claude-oauth)
        pinned="${pinned}${pinned:+, }${name} goes out on the single credential ${access} (${ptyp})"
        ;;
      *)
        quiet="${quiet}${quiet:+, }${name}=${access} (${ptyp}, the runner path)"
        ;;
      esac
      continue
    fi
    # A craft whose access the node does not hold contributes nothing either: the
    # service refuses the call rather than rotating it, which is a state the
    # check's own FAIL row would have named, not a pin.
    quiet="${quiet}${quiet:+, }${name}=${access} (not in the node)"
  done <"$SCRATCH/crafts.txt"
  if [ -n "$pinned" ]; then
    fact pool FAIL "the pool '${POOL_NAME}' asks for the rotation, and this call would not get it: ${pinned}. The credentials service takes a rotation up only when EVERY craft that contributed a candidate goes out on a pool asking for it (MC7, internal/credsvc/roundrobin.go) — the wire carries no craft, so a mixture resolves to the cascade's own rule — and the runs below would then be a reading of that rule and not of the rotation. Put every craft of this project that the service serves on a round-robin pool (a craft on the built-in vertex or on an OpenRouter key contributes nothing and may stay where it is) and run this check again"
    return 0
  fi
  PREMISE_OK=yes
  fact pool PASS "the coder craft goes out on the round-robin pool '${POOL_NAME}' of ${members//,/, }, every member of it served by the credentials service (subscription_spread=${spread:-absent}), and no craft the service serves is pinned (it contributes nothing from: ${quiet:-none}): ${RUNS} runs on this pool take ${RUNS} turns of the counter, and the spread row reads what they were served by"
}

# step_bundle: the service can only serve what its bundle holds. A member the
# bundle does not hold is a member the service cannot hand to a call at all, and
# the pool would be refused rather than rotated (credsvc's poolOfferingOf).
step_bundle() {
  if [ "$PREMISE_OK" != yes ]; then
    unmeasured bundle "there is no pool to check the members of: the row above says why"
    return 0
  fi
  # The bundle is part of the premise and not a row beside it: a pool the service
  # cannot hand in full is one a run would be refused on, and the runs below would
  # spend the sandbox's money on a reading nobody could grade. It is cleared again
  # by the one branch that establishes the whole pool is held.
  PREMISE_OK=no
  say "reading the credentials service's bundle: fugaro creds ls --json (names and metadata, never a value)"
  run_bounded "$CLI_TIMEOUT" creds "$SANDBOX" "$FUGARO_BIN" creds ls --json
  if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
    unmeasured bundle "fugaro creds ls --json did not answer (exit ${BOUNDED_STATUS}): whether the service holds the pool's members is what decides whether the runs below can be served by the pool at all. Its output is in $SCRATCH/creds.out and $SCRATCH/creds.err"
    return 0
  fi
  if ! read_json creds "$SCRATCH/creds.out" "$SCRATCH/creds.txt"; then
    unmeasured bundle "fugaro creds ls --json printed a document this check could not read: what it printed is in $SCRATCH/creds.out"
    return 0
  fi
  local missing="" m held
  held=$(field "$SCRATCH/creds.txt" count)
  for m in ${POOL_MEMBERS//,/ }; do
    if ! awk -F'\t' -v name="$m" '$1 == "name" && $2 == name { found = 1 } END { exit !found }' "$SCRATCH/creds.txt"; then
      missing="${missing}${missing:+, }${m}"
    fi
  done
  if [ -n "$missing" ]; then
    fact bundle FAIL "the credentials service's bundle does not hold ${missing} (it holds ${held} credential(s): $(awk -F'\t' '$1 == "name" { printf "%s ", $2 }' "$SCRATCH/creds.txt")): the pool names a member the service cannot hand to a call, and the service refuses the call rather than rotating onto another. Store each one's key under its own name ('fugaro creds set <member>') and run this check again"
    return 0
  fi
  PREMISE_OK=yes
  fact bundle PASS "every member of '${POOL_NAME}' is in the credentials service's bundle (${held} credential(s) held, read as names and metadata only): the service can hand each one to a call, which is what makes a rotation between them possible"
}

# step_runs: N runs, one at a time. Each is launched, followed to its end, and
# read out of the runs bucket; the run's serving credential is taken from the
# record. The batch stops at the first run that cannot be launched: the runs after
# it would be a reading of a project whose configuration this check has just found
# it cannot use.
step_runs() {
  if [ -z "$PROJECT" ]; then
    unmeasured runs-launched "there is no project to launch runs in: the row above says why"
    return 0
  fi
  if [ "$PREMISE_OK" != yes ]; then
    unmeasured runs-launched "nothing was launched: the rows above did not establish the whole round-robin pool these runs are a reading of, and a run launched without it would spend the sandbox's money on a reading nobody could grade. Settle the row named above and run this check again"
    return 0
  fi
  RUNS_ROWS="$SCRATCH/runs.tsv"
  : >"$RUNS_ROWS"
  local i=1 id repo status member src cost started ended
  while [ "$i" -le "$RUNS" ]; do
    say "launching run ${i} of ${RUNS} from ${SANDBOX} (a real sandbox run, with what its task costs)"
    run_bounded "$LAUNCH_TIMEOUT" "launch$i" "$SANDBOX" \
      "$FUGARO_BIN" run --repo "$SANDBOX" ${RECIPE:+--recipe "$RECIPE"} --json "$TASK"
    if [ "$BOUNDED_TIMED_OUT" = yes ]; then
      unmeasured runs-launched "run ${i} of ${RUNS} did not finish launching inside ${LAUNCH_TIMEOUT}s and was ended: what it printed is in $SCRATCH/launch$i.out and $SCRATCH/launch$i.err"
      return 0
    fi
    if [ "$BOUNDED_STATUS" != 0 ]; then
      fact runs-launched FAIL "run ${i} of ${RUNS} could not be launched: fugaro run exited ${BOUNDED_STATUS} from ${SANDBOX}. The check needs ${RUNS} runs to read a spread over, and a launch that refuses says the sandbox's configuration is not one a run starts on. What it printed is in $SCRATCH/launch$i.out and $SCRATCH/launch$i.err"
      return 0
    fi
    if ! read_json launch "$SCRATCH/launch$i.out" "$SCRATCH/launch$i.txt"; then
      fact runs-launched FAIL "run ${i} of ${RUNS}: fugaro run --json printed a document this check could not read, so the run it launched cannot be named and cannot be followed. What it printed is in $SCRATCH/launch$i.out"
      return 0
    fi
    id=$(field "$SCRATCH/launch$i.txt" run_id)
    repo=$(field "$SCRATCH/launch$i.txt" repo)
    if [ -z "$id" ] || [ -z "$repo" ]; then
      fact runs-launched FAIL "run ${i} of ${RUNS}: fugaro run --json named no run (run_id=${id:-none}, repo=${repo:-none}), and a run this check cannot name is one it cannot follow or read. What it printed is in $SCRATCH/launch$i.out"
      return 0
    fi
    LAUNCHED=$((LAUNCHED + 1))
    LAUNCH_REPO=$repo
    say "waiting for ${id} to end (up to ${RUN_TIMEOUT}s; fugaro runs ls --repo ${repo} --json --limit 1)"
    started=0
    ended=no
    status=""
    while [ "$started" -lt "$RUN_TIMEOUT" ]; do
      # The output file is the run's own: every command here appends what it
      # printed, and a document read out of a file two runs had written to is not
      # a document at all.
      run_bounded "$CLI_TIMEOUT" "rows$i" "$SANDBOX" "$FUGARO_BIN" runs ls --repo "$repo" --json --limit 1
      status=""
      ended=no
      if [ "$BOUNDED_STATUS" = 0 ] && read_json rows "$SCRATCH/rows$i.out" "$SCRATCH/rows.txt"; then
        status=$(run_row "$id" 3)
        ended=$(run_row "$id" 4)
        RUN_REF=$(run_row "$id" 6)
      fi
      [ "$ended" = true ] && break
      sleep "$POLL_SECONDS"
      started=$((started + POLL_SECONDS))
    done
    if [ "$ended" != true ]; then
      unmeasured runs-ended "run ${i} of ${RUNS} (${id}) had not ended after ${RUN_TIMEOUT}s (last row: status=${status:-unknown}): this check reads a terminal run's own record, and reading one while the run is still working would read whatever was there before. Watch it with 'fugaro watch ${id}' and run this check again when it has ended — the run it launched is a real one and is not cancelled by stopping here"
      return 0
    fi
    ENDED=$((ENDED + 1))
    case "$RUN_REF" in
    */"$id") ;;
    *)
      unmeasured records "fugaro runs ls --json named run ${i}'s row with no ref this check could take a bucket key from (run=${RUN_REF:-none}), so there was no key to read: the record of run ${id} lives under the repository's slug, and the slug is what that row's ref carries"
      return 0
      ;;
    esac
    local key="runs/${RUN_REF}/result.json"
    say "reading the record: gcloud storage cat gs://fugaro-runs-${PROJECT}/${key}"
    run_bounded "$CLI_TIMEOUT" "record$i" "$SANDBOX" gcloud storage cat "gs://fugaro-runs-${PROJECT}/${key}" --project="${PROJECT}"
    if [ "$BOUNDED_TIMED_OUT" = yes ] || [ "$BOUNDED_STATUS" != 0 ]; then
      fact records FAIL "gs://fugaro-runs-${PROJECT}/${key} could not be read (exit ${BOUNDED_STATUS}): a run that ended has a record, and the record is where the run's serving credential is written. What gcloud printed is in $SCRATCH/record$i.err"
      return 0
    fi
    if ! read_json record "$SCRATCH/record$i.out" "$SCRATCH/record$i.txt"; then
      fact records FAIL "${key} is not a document this check could read: what gcloud printed is in $SCRATCH/record$i.out"
      return 0
    fi
    if [ "$(field "$SCRATCH/record$i.txt" run_id)" != "$id" ]; then
      fact records FAIL "${key} records run_id=$(field "$SCRATCH/record$i.txt" run_id) and this check launched ${id}: the record at that key is another run's, and nothing below can be read as this run's"
      return 0
    fi
    RECORDS=$((RECORDS + 1))
    src=""
    member=$(member_of "$SCRATCH/record$i.txt")
    if [ -z "$member" ]; then
      member="none"
    else
      src=${member%%$'\t'*}
      member=${member#*$'\t'}
    fi
    cost=$(field "$SCRATCH/record$i.txt" cost_usd)
    status=$(field "$SCRATCH/record$i.txt" status)
    [ "$status" = succeeded ] && SUCCEEDED=$((SUCCEEDED + 1))
    TOTAL_USD=$(awk -v a="$TOTAL_USD" -v b="${cost:-0}" 'BEGIN { printf "%.4f", a + b }')
    printf '%s\t%s\t%s\t%s\t%s\n' "$id" "$member" "${src:-none}" "$status" "${cost:-0}" >>"$RUNS_ROWS"
    i=$((i + 1))
  done
  fact runs-launched PASS "${RUNS} runs were launched from the sandbox checkout, one after another: $(cut -f1 "$RUNS_ROWS" | tr '\n' ' ')— each is the check's own, and the rows below read these runs and no others"
}

# step_ended: the whole batch has to have reached its end. A check that read a
# record while a run was still going would read whatever was there before it.
step_ended() {
  if [ "$LAUNCHED" = 0 ]; then
    unmeasured runs-ended "there is no run to follow: the row above says why"
    return 0
  fi
  if recorded runs-ended; then return 0; fi
  # A batch only gets past the step above when every run it launched ended in
  # turn, so a short count here is a batch that stopped part way — the row that
  # recorded why is already in the block, and what is missing is the whole batch.
  if [ "$ENDED" != "$RUNS" ]; then
    unmeasured runs-ended "only ${LAUNCHED} of the ${RUNS} runs this check means to launch were followed at all, and ${ENDED} of them reached an end: the row above says what stopped the batch, and a batch that is short is not the N runs the spread is read over. Watch them with 'fugaro runs ls --repo ${LAUNCH_REPO:-$REPO}' and run this check again once the whole batch has ended"
    return 0
  fi
  fact runs-ended PASS "all ${ENDED} runs launched reached their end (read from fugaro runs ls --json): they are in the state their records are written in"
}

# step_records: every run's record, read from the runs bucket as the owner would
# read it, and tied to its run by the record's own run_id.
step_records() {
  if [ "$LAUNCHED" = 0 ]; then
    unmeasured records "there is no run whose record could be read: the row above says why"
    return 0
  fi
  if recorded records; then return 0; fi
  if [ "$RECORDS" != "$RUNS" ]; then
    unmeasured records "${RECORDS} of the ${RUNS} runs this check read have a readable record: the row above names the run that is missing, and a spread read over part of a batch is a reading of a different batch"
    return 0
  fi
  fact records PASS "all ${RECORDS} runs reached their own record in the bucket (each record's run_id is the run this check launched): the credential each run was served by is written there"
}

# step_succeeded: every run has to have passed. A run that failed mid-stage is
# not a reading of the rotation — it is a reading of whatever stopped it.
step_succeeded() {
  if [ "$RECORDS" != "$RUNS" ]; then
    unmeasured succeeded "there is no whole batch to read an outcome over: the row above says which of the ${RUNS} runs is missing"
    return 0
  fi
  if [ "$SUCCEEDED" != "$RUNS" ]; then
    fact succeeded FAIL "${SUCCEEDED} of the ${RUNS} runs this check read ended succeeded, and the rest did not ($(awk -F'\t' '$4 != "succeeded" { split($1, a, "-"); printf "#%s=%s ", a[length(a)], $4 }' "$RUNS_ROWS")): the rotation is read from runs that ran, and a run that stopped is a reading of whatever stopped it. Read one with 'fugaro diagnose <run-id>'"
    return 0
  fi
  fact succeeded PASS "all ${SUCCEEDED} runs ended succeeded: the pool's calls were answered in each of them, which is the state the spread row below is read in"
}

# step_budget: the money, graded only when you said what the batch may cost. The
# figure is the sum of the records' own model costs, so the row is a reading of the
# bill and not of an estimate.
step_budget() {
  if [ "$RECORDS" != "$RUNS" ]; then
    unmeasured budget "there is no whole batch to add the cost of: the row above says which of the ${RUNS} runs is missing, and the total of the runs that did land is not the total this check set out to read"
    return 0
  fi
  if [ -z "$MAX_MODEL_USD" ]; then
    fact budget INFO "the ${RECORDS} runs cost ${TOTAL_USD} USD of model spend in total, read from their own records. CHECK421_MAX_MODEL_USD is not set, so this check does not grade the figure: set it to turn the batch's cost into a criterion"
    return 0
  fi
  if awk -v spent="$TOTAL_USD" -v cap="$MAX_MODEL_USD" 'BEGIN { exit !(spent > cap) }'; then
    fact budget FAIL "the ${RECORDS} runs cost ${TOTAL_USD} USD of model spend and CHECK421_MAX_MODEL_USD=${MAX_MODEL_USD}: the batch went over the figure you set, and the money is spent. Raise the knob if this many runs are simply worth more than you allowed, or read the spread over fewer of them, and run the check again"
    return 0
  fi
  fact budget PASS "the ${RECORDS} runs cost ${TOTAL_USD} USD of model spend in total, inside CHECK421_MAX_MODEL_USD=${MAX_MODEL_USD}: the batch's own cost figures held to the bound you set"
}

# step_spread: THE row. Over N runs on one round-robin pool, at least two of the
# pool's members have to have served a run: one member serving every run is the
# pool not rotating, which is the state MC7 removes. The evidence is each run's own
# record and nothing this script derived — the credential the record names, and
# the source it was read from.
step_spread() {
  if [ "$RECORDS" != "$RUNS" ]; then
    unmeasured spread "there is no whole batch to read a spread over: the row above says which of the ${RUNS} runs is missing"
    return 0
  fi
  local table distinct unnamed
  table=$(awk -F'\t' '{ split($1, a, "-"); printf "#%s→%s(%s) ", a[length(a)], $2, $3 }' "$RUNS_ROWS")
  distinct=$(awk -F'\t' '$3 != "none" { print $2 }' "$RUNS_ROWS" | sort -u | wc -l | tr -d ' ')
  unnamed=$(awk -F'\t' '$3 == "none" { n++ } END { print n + 0 }' "$RUNS_ROWS")
  if [ "$unnamed" != 0 ]; then
    fact spread FAIL "${unnamed} of the ${RECORDS} runs' records name NO member of '${POOL_NAME}' (${table}): the record's own fields are where the credential that served a run is written, and a run whose record names nothing the pool holds is one this check cannot tie to the pool at all. Read one with 'fugaro runs show <run-id> --json' and 'fugaro diagnose <run-id>'"
    return 0
  fi
  if [ "$distinct" -lt 2 ]; then
    fact spread FAIL "every one of the ${RECORDS} runs was served by the SAME member of '${POOL_NAME}' (${table}): a round-robin pool shares its runs out over its members, and one member serving all of them is the pool not rotating — MC7's red step, 'three selections must not all pick member 1'. Check that the pool's strategy is round-robin in the published node ('fugaro accounts pool ls') and that no craft the credentials service serves is pinned to another access (a pinned craft resolves the whole call to the cascade's own rule, which is what the pool row above walks); then run this check again"
    return 0
  fi
  fact spread PASS "${distinct} different members of '${POOL_NAME}' served the ${RECORDS} runs (${table}): the pool rotated, which is what Check 421 measures. The runs are one turn of the counter each, so with ${RECORDS} runs over ${POOL_MEMBERS//,/, } each member serves about $((RECORDS / distinct)) of them"
}

# restore: what this check changed, said rather than assumed.
restore() {
  RESTORE_VERDICT=PASS
  local run_note="Nothing was launched, so there is no run and no pull request to clean up after."
  if [ "$LAUNCHED" != 0 ]; then
    run_note="The ${LAUNCHED} runs this check launched (${RUNS_ROWS:+$(cut -f1 "$RUNS_ROWS" 2>/dev/null | tr '\n' ' ')}) are real sandbox runs and they opened a pull request each: close those pull requests and delete their branches yourself once you have read the runs — this check does not do it for you, because each pull request is that run's own work and may be worth reading first."
  fi
  RESTORE_NOTE="this check wrote no config, no pool and no credential: it read the installation's own crafts, published node and bundle, and launched at most ${RUNS} runs. ${run_note} The pool, its members and their keys were yours before this check and are unchanged by it. The scratch directory held the commands' output"
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
  say "CHECK 421 plan (dry run: nothing below is run, and no row in the block was measured)"
  say "  [offline] fugaro craft ls --help, accounts pool ls --help, project policy show --help, creds ls --help — the model-crafts vocabulary must be in the build"
  say "  [network, read] fugaro doctor --json in ${SANDBOX} — the project this checkout belongs to"
  say "  [read] fugaro craft ls --json — every craft's effective access (the rotation is only taken when no craft the service serves is pinned)"
  say "  [network, read] fugaro project policy show --json — the published node: the pool's members, its round-robin strategy and subscription_spread"
  say "  [network, read] fugaro creds ls --json — the credential names the service's bundle holds (never a value)"
  say "  [network, BILLABLE] fugaro run --repo ${SANDBOX}${RECIPE:+ --recipe ${RECIPE}} --json \"${TASK}\" — ${RUNS} sandbox runs, one after another, each followed with fugaro runs ls --json until it is terminal"
  say "  [read] gcloud storage cat gs://fugaro-runs-<the project>/runs/<the repository's slug>/<run>/result.json — each run's record: the credential it names as the one that served it"
  say "  [nothing else] no config, no pool, no credential and no policy is written; the scratch directory is removed at the end"
  fact bound PASS "planned: ${RUNS} sandbox runs and what their tasks cost. --dry-run runs nothing, and real runs require --yes"
  for key in features sandbox crafts pool bundle runs-launched runs-ended records \
    succeeded budget spread; do
    fact "$key" INFO "planned: a real installation and ${RUNS} runs measure this row; --dry-run did not"
  done
  fact dry-run PASS "the plan was printed and nothing was run: no doctor, no craft read, no policy read, no bundle read, no run and no record"
}


# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it. That is what makes the block safe to diff between two runs.
BLOCK_KEYS=(fugaro-version features xtrace agent-session belong bound sandbox
  crafts pool bundle runs-launched runs-ended records succeeded budget spread
  restore scratch-removed)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=""
  printf '\nCHECK 421 RESULT\n' >&3
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
  printf 'CHECK 421 RESULT END (exit %s)\n' "$status" >&3
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
RUNS_CLI=""
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
    [ $# -ge 2 ] || die "--task needs the text the sandbox runs are launched with"
    TASK_CLI=$2
    shift 2
    ;;
  --recipe)
    [ $# -ge 2 ] || die "--recipe needs a recipe name (agent, default, review, ...)"
    RECIPE_CLI=$2
    shift 2
    ;;
  --runs)
    [ $# -ge 2 ] || die "--runs needs a count: how many runs the spread is read over (2 or more)"
    RUNS_CLI=$2
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

SANDBOX=${CHECK421_SANDBOX:-${SANDBOX_CLI:-}}
if [ -z "$SANDBOX" ]; then
  die "Check 421 needs --sandbox: the checkout of a repository of the sandbox project. It is what the runs are launched from and what its crafts are read through. Usage: bash scripts/live-checks/check421.sh --sandbox <a sandbox repository checkout> --yes"
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
REPO=${CHECK421_REPO:-${SANDBOX##*/}}
TASK=${CHECK421_TASK:-${TASK_CLI:-"Reply with one line saying the repository is reachable. Do not change any file."}}
RECIPE=${CHECK421_RECIPE:-${RECIPE_CLI:-}}
RUNS=${CHECK421_RUNS:-${RUNS_CLI:-2}}
case "$RUNS" in
'' | *[!0-9]*) die "CHECK421_RUNS/--runs must be a whole number of runs (got '$RUNS')" ;;
esac
if [ "$RUNS" -lt 2 ]; then
  die "Check 421 needs at least 2 runs (CHECK421_RUNS=${RUNS}): a rotation is read over runs that had a choice of member, and one run has none. Two is the fewest that can show a spread at all"
fi
MAX_MODEL_USD=${CHECK421_MAX_MODEL_USD:-}
YES=${CHECK421_YES:-$YES_CLI}
DRY_RUN=${CHECK421_DRY_RUN:-$DRY_RUN_CLI}
KEEP_SCRATCH=${CHECK421_KEEP_SCRATCH:-}
REGION=${CHECK421_REGION:-us-east5}
RUN_TIMEOUT=${CHECK421_RUN_TIMEOUT_SECONDS:-3600}
POLL_SECONDS=${CHECK421_POLL_SECONDS:-15}
CLI_TIMEOUT=${CHECK421_CLI_TIMEOUT_SECONDS:-180}
LAUNCH_TIMEOUT=${CHECK421_LAUNCH_TIMEOUT_SECONDS:-600}
DOCTOR_TIMEOUT=${CHECK421_DOCTOR_TIMEOUT_SECONDS:-300}
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
  [ -n "$FUGARO_BIN" ] || die "Check 421 needs 'fugaro' on PATH: the check drives the build you have installed, and it is not there"
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
    die "Check 421 launches ${RUNS} sandbox runs and spends what their tasks cost, so it asks for --yes. Nothing has been launched. Type: bash scripts/live-checks/check421.sh --sandbox ${SANDBOX} --yes (or read the plan first with --dry-run)"
  fi
  step_sandbox
  step_crafts
  step_pool
  step_bundle
  step_runs
  step_ended
  step_records
  step_succeeded
  step_budget
  step_spread
}

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
