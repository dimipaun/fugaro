#!/usr/bin/env bash
# Check 680 — the cloud tier of `fugaro troubleshoot`, end to end.
#
# Plan task T-TC10 of docs/troubleshoot-cloud/implementation-plan.md is what this
# check was written for, and 680 is the number the owner gave it on 2026-11-21
# (the plan's own text read Check 620 until the renumber that came with this
# check). It measures D2408's three parts on a real installation, which is what
# no hermetic test can:
#
#   (a) a deliberately refused launch — the installation's own base image rolled
#       back to a release older than the project's policy — shows the CLOUD tier
#       declining, with the gate's own sentence and the command that fixes it,
#       and no execution created. The launch path being broken is what the tier
#       exists for: a job launched through it would meet the same gate and fail a
#       second time instead of explaining the first (spec acceptance 3, D2401).
#   (b) the cloud tier made unavailable — the tier pointed at a project that is
#       not there, so the reads it makes in its own project cannot be made —
#       shows tier 4 (evidence only) with the reason, and still creates nothing.
#   (c) on a healthy sandbox, the deliberate launch's own failed log, diagnosed
#       by `fugaro troubleshoot` with NO local login, shows a cloud answer, the
#       cost the run's record holds, the run record itself, the answer read back
#       out of the run's own artifacts and the bundle's deletion — and, because
#       an answer exists at all, that the class marker T-TC12 writes admitted the
#       run to the review-only variant and its model call went the review
#       class's way: a run without the marker is halted at bootstrap.
#
# What it reads, in the order the block reads it:
#
#   * `fugaro config list --json` and nothing else for setup: the installations
#     this machine has, each with its name, its GCP project, its region and the
#     checkouts it remembers. It is read-only, offline and needs no credential
#     (the document's own contract), which is why this check needs no flag on a
#     sandbox machine and reads the values the CLI itself resolved rather than
#     the ones this file would parse;
#   * `git` — the checkout's own toplevel and its `origin` remote, which is the
#     repository the launch and the diagnosis are scoped to;
#   * **the project config file the CLI read**, at
#     `$XDG_CONFIG_HOME/fugaro/projects/<name>.yaml` (else under `~/.config`):
#     its `base_images.<kind>` ref, its `runs_bucket` and its budget block, read
#     from the file's own text with python3 and no second YAML parser. This is
#     the ONE file the check writes, every write replaces one literal value in
#     it, and one copy of it is kept beside it (`<config>.check680-backup`) from
#     before the first write. NOTHING below proves the CLI reads this file by
#     asking doctor: the proof is the tier's own behaviour, which the parts read
#     anyway — a pre-flight that named the rolled-back image in (a), and a
#     refusal that is NOT that sentence in (b), can only come from a CLI that
#     read the file this check rewrote;
#   * **the newest failed log** the CLI would pick itself (`<state>/fugaro/logs/
#     <command>-<14 digits>-<pid>.log`, its closing line `Failed in …` or `Left
#     for you in …`): the subject the three parts diagnose. The check passes its
#     ID explicitly and holds the document's own `run_id` to it, so a diagnosis
#     of another log cannot be read as this one's;
#   * `gcloud` — what the project's jobs created while each part ran (T-TC10(a)
#     and (b): no execution created, and (c): exactly one), read as a window on
#     creation time — the newest
#     executions, newest first, of which the check counts the ones created at or
#     after the moment the window opened — rather than as a total of the
#     project's executions, which a project that has run for weeks can never be
#     counted against; and, in (c), the run's own objects in the runs bucket: the
#     record, the stored task, the answer's two artifacts and the bundle that
#     must be gone.
#
# What it writes, and what it puts back. This check doctors ONE file, the
# installation's own project config, and only ever by replacing a literal value
# in it: `base_images.<kind>`'s release for part (a), put back before part (b)
# starts (the image gate is the pre-flight's first question, and it would answer
# part (a)'s sentence again), then `gcp_project` and the budget block's own
# project and database for part (b), because the tier's own read in its project
# is what makes it unavailable. Before
# the first rewrite the original is copied to `<config>.check680-backup` (mode
# 0600), and that copy is what the exit trap puts back and removes — on a clean
# end, on a failed assertion and on Ctrl-C alike. A check killed outright
# (SIGKILL, a power cut) leaves the sidecar, and the register's Restore is one
# command: `cp <config>.check680-backup <config>`. Nothing else in the
# installation is written: no task, no run, no policy, no budget, and the launch
# in part (a) is refused by the gate before anything of it exists — which is why
# part (a) proves the gate is the judge BEFORE it launches (below).
#
# A NOTE ON THE LAUNCH IN PART (a). `fugaro run` has no dry-run: a launch that
# is not refused starts a real run on the sandbox. So the check refuses to launch
# until it has read, from the tier's own decline, that the judge of that
# repository's job image is the LOCAL CONFIG (the sentence the checkout: clone
# gate writes) rather than the build record in the runs bucket. Only then can the
# roll-back be what refuses the launch — and the row says which judge the decline
# named. A repository whose workflow is judged from its build record is left
# alone entirely: this check does not doctor the runs bucket, and part (a) then
# reads as NOT MEASURED rather than as a pass.
#
# A NOTE ON WHAT IS NEVER PRINTED: no credential, no token and no secret value
# of any kind — the check reads none. What it prints of the diagnosis is the
# answer's first line, the tier, the cost and the run's own reference. The
# launch's task text is this check's own and holds nothing of yours.
#
# Usage:      bash scripts/live-checks/check680.sh
#             bash scripts/live-checks/check680.sh --dry-run
#             bash scripts/live-checks/check680.sh --help
#             Run it from the SANDBOX CHECKOUT (the checkout: clone repository
#             the diagnosis is scoped to), with `fugaro` and `gcloud` on PATH and
#             gcloud authenticated to that installation's own project. It prompts
#             for nothing and needs no flag on a machine with one installation;
#             every flag below overrides a value the installation supplies —
#             --name the installation (project config) name, --config its config
#             file, --checkout a directory inside the checkout (default: this
#             directory), --repo the repository as owner/name, --project the GCP
#             project, --region its region, --bucket its runs bucket, --subject
#             the failed log to diagnose, --bad-release the release the base
#             image is rolled back to (default 0.1.0: older than every gate this
#             release carries), --absent-project the project part (b) points the
#             tier at (it must not exist, and the check asks gcloud to confirm
#             that), --task the text of the deliberately refused launch,
#             --executions-limit how many of the NEWEST executions each gcloud
#             read asks for (default 200: the reads are windows on creation time,
#             so how many executions the project holds never enters them),
#             --cloud-seconds the bound on every call of this check that reaches
#             the cloud — the deliberately refused launch of (a), the diagnosis
#             of (c) and the two troubleshoot reads of (a) and (b), each of which
#             launches a job execution whenever its tier answers rather than
#             declining (default 1500: the tier's own fifteen-minute
#             timeout and slack; 0 for no bound). The bound is `timeout`'s own,
#             so it holds where the machine has one (GNU coreutils; macOS ships
#             none, which is Check 310's own caveat) and without one every such
#             call is unbounded and yours to interrupt.
# Needs:      bash, python3 (every document and every rewrite below is read and
#             written with it), git, gcloud (authenticated: it lists executions
#             and reads the runs bucket) and `fugaro` — the binary of the
#             release under test, since the gates, the tier and the recipe are
#             its own. `timeout` is what the bound above is made of and is not
#             needed to run the check. It reads no secret and prompts for
#             nothing.
# Where:      the sandbox installation, from the sandbox checkout only, and
#             NEVER belong: a value this check was given that names belong is
#             refused before the first call, and an installation this check
#             resolved for itself that names belong is refused at the same point.
#             An installation whose GCP project is fugaro-prod is refused as
#             well: this check rewrites its config for the length of two parts,
#             and that is not a thing to do to production.
# Costs:      the diagnosis in part (c) launches ONE job on the project's own
#             account, bounded by the recipe's own fifteen minutes and USD 0.25
#             cap (D2407), plus one container start's storage. Parts (a) and (b)
#             launch nothing: (a)'s launch is refused by the gate before the
#             backend is reached, and (b) never gets past the tier's own
#             pre-flight. No gcloud call here is billable.
# Runs:       by YOU. No agent runs any step of it, and its own test
#             (check680_test.sh) drives it against fake `fugaro`, `gcloud` and
#             `git` only.
# Cleanup:    the project config is put back from `<config>.check680-backup`
#             (and the sidecar removed) by the exit trap, and the scratch
#             directory is removed with it — on a failure and on a Ctrl-C too,
#             unless CHECK680_KEEP_SCRATCH is set. What this check leaves behind
#             is what a diagnosis leaves behind anywhere: the run record the
#             cloud tier wrote, with the answer's own artifacts, which `fugaro
#             diagnose <ref>` explains and the runs bucket's lifecycle expires.
# Output:     progress on stderr, ONE block on stdout headed CHECK 680 RESULT,
#             then CHECK 680 RESULT END (exit N) — to copy back into the
#             register's Check 680 section, and nothing else. Exit 0: every
#             criterion that was measured held. 1: a measured criterion did not
#             hold — the tier launched a job over a broken launch path, the
#             decline carried a sentence the gate did not write, an execution
#             appeared where none may, the answer was not a cloud one, the record
#             held no cost, the class marker was missing, the bundle survived the
#             answer — or a guard stopped the check, such as a run of the
#             project's own jobs going on in the window this check opened. 2: a
#             row could NOT be measured, for a reason that is this check's
#             arrangement or the installation: no failed command is logged, the
#             workflow's image is judged from the build record rather than the
#             local config, the config file could not be found or held to the
#             CLI's reading, the executions could not be listed, the record names
#             no execution or one the list it is read against contradicts. A key
#             the run never reached reads `FAIL — not reached`, which is a row
#             that was NOT measured.
#
# Knobs, all read with a default so an unset shell runs it as written:
# CHECK680_NAME, CHECK680_CONFIG, CHECK680_CHECKOUT, CHECK680_REPO,
# CHECK680_PROJECT, CHECK680_REGION, CHECK680_BUCKET, CHECK680_SUBJECT,
# CHECK680_BAD_RELEASE (0.1.0), CHECK680_ABSENT_PROJECT, CHECK680_TASK,
# CHECK680_EXECUTIONS_LIMIT (200), CHECK680_CLOUD_SECONDS (1500),
# CHECK680_LOGS_DIR, CHECK680_DRY_RUN, CHECK680_KEEP_SCRATCH.
set -Eeuo pipefail

# No xtrace, whatever the shell it was started from had: a traced run would print
# every value this check reads out of the installation, and the check prints its
# own RESULT block, so a trace is never needed to report a failure.
XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
case "${SHELLOPTS:-}" in *xtrace*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null
unset BASH_XTRACEFD 2>/dev/null || true

# The owner's own stdout and stderr, kept on 3 and 4: a trap runs with whatever
# redirection is in effect when the signal arrives, so the block is written to
# the saved descriptors.
exec 3>&1 4>&2

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
owner() { printf '%s: %s\n' "$PROG" "$*" >&4; }

FACTS_FILE=""
LAST_ROW=""

# stop REASON: records a FAILed fact and ends the run. The EXIT trap prints the
# block, puts the project config back and removes the scratch directory. A stop
# right after an `unmeasured` row ends the run as one that could not measure
# (exit 2): the difference matters to whoever reads the block, because 1 is a
# finding about the release and 2 is a run that never got to measure it.
stop() {
  local was=$LAST_ROW
  fact aborted FAIL "$1"
  owner "stopped: $1"
  if [ "$was" = unmeasured ]; then exit 2; fi
  exit 1
}

fact() { # fact KEY PASS|FAIL|INFO "detail"
  [ -n "$FACTS_FILE" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS_FILE"
  case "$2" in
  FAIL) LAST_ROW=fail ;;
  *) LAST_ROW="" ;;
  esac
}

# unmeasured: a row the check could NOT measure, for a reason that is not a
# finding about the release. The row reads FAIL in the block — it is never a pass
# — and the check ends as one that could not measure (exit 2), whether it stopped
# there or ran to the end.
UNMEASURED_KEYS=""
unmeasured() { # unmeasured KEY "detail"
  fact "$1" FAIL "$2"
  UNMEASURED_KEYS="${UNMEASURED_KEYS:+$UNMEASURED_KEYS }$1"
  LAST_ROW=unmeasured
}

on_error() {
  trap - ERR
  local line=${BASH_LINENO[0]:-?}
  say "an internal error ended this check at line $line"
  exit 2
}

need_tools() {
  local t
  # `timeout` is NOT one of them: it is what the --cloud-seconds bound is made
  # of, and a machine without one (macOS ships none) runs the check with the two
  # calls that may launch something unbounded rather than not at all (below).
  for t in bash mktemp python3 git gcloud fugaro; do
    command -v "$t" >/dev/null 2>&1 || {
      say "Check 680 needs '$t' and it is not on PATH"
      exit 2
    }
  done
}

one_line() { printf '%s' "$1" | tr '\n' ' ' | sed -e 's/  */ /g' -e 's/^ //' -e 's/ $//'; }
last_lines() { tail -n 4 "$1" 2>/dev/null | tr '\n' ' ' | cut -c1-400 || true; }
file_hash() { python3 -c 'import hashlib,sys;print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest()[:16])' "$1" 2>/dev/null || printf '?'; }

# ------------------------------------------------------------ scratch + trap
SCRATCH=""
SCRATCH_KEPT=no
CLEANUP_RUNNING=no
PRINTED=""

NAME=""
CONFIG=""
CONFIG_BACKUP=""
CONFIG_ORIGINAL_HASH=""
CHECKOUT=""
REPO=""
PROJECT=""
REAL_PROJECT=""
REGION=""
BUCKET=""
BASE_OLD=""
BASE_NEW=""
HAS_GCP_LINE=no
HAS_RTDB_LINE=no
HAS_FP_LINE=no
HAS_BUDGET=no
BUDGET_MODE=""
SUBJECT=""
SUBJECT_COMMAND=""
JUDGE=""
GATE_SENTENCE=""
GATE_RELEASE=""
GATE_WORKFLOW=""
A_REASON=""
UNREACHABLE=""
NOCLAUDE_PATH=""
CLAUDE_DROPPED=""
SINCE_CHECK=""
SINCE_CHECK_STAMP=""
SINCE_A=""
SINCE_A_STAMP=""
RUN_EXEC=""
RUN_JOB=""
RUN_EXEC_NAME=""
COST_PRINTED=""
DECLINE_STATUS=""
LEVER=""
RUN_REF=""
RUN_SLUG=""
RUN_ID=""

scratch_up() {
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/check680.XXXXXX") || {
    say "Check 680 could not make a scratch directory"
    exit 2
  }
  chmod 700 "$SCRATCH"
  say "scratch directory: $SCRATCH (mode 0700, removed at the end unless CHECK680_KEEP_SCRATCH is set)"
  FACTS_FILE="$SCRATCH/facts"
  : >"$FACTS_FILE"
  build_noclaude_path
}

# config_backup: the project config's own copy, beside it rather than inside the
# scratch directory, which a killed run leaves nothing of: the register's
# Restore has to be possible after the worst end, and it is one cp.
config_backup() {
  cp -p "$CONFIG" "$CONFIG_BACKUP" || {
    say "Check 680 could not copy $CONFIG to $CONFIG_BACKUP"
    exit 2
  }
  chmod 600 "$CONFIG_BACKUP"
  say "the project config is at $CONFIG; its copy, put back at the end, is at $CONFIG_BACKUP"
}

# config_undo_writes puts the file back to the byte the check found and KEEPS the
# sidecar, which is what part (b) needs: the roll-back of part (a) has to be gone
# before (b) can arrange anything, because the image gate is the first thing the
# pre-flight asks and it would answer (a)'s sentence again (below).
config_undo_writes() {
  [ -n "$CONFIG_BACKUP" ] || return 0
  [ -f "$CONFIG_BACKUP" ] || return 0
  cp -p "$CONFIG_BACKUP" "$CONFIG" || {
    say "Check 680 could not put $CONFIG back from $CONFIG_BACKUP before part (b)"
    exit 2
  }
}

config_restore() {
  config_undo_writes
  [ -n "$CONFIG_BACKUP" ] || return 0
  rm -f "$CONFIG_BACKUP"
}

scratch_down() {
  [ -n "$SCRATCH" ] || return 0
  if [ "${KEEP_SCRATCH:-}" = 1 ]; then
    SCRATCH_KEPT=yes
    return 0
  fi
  rm -rf "$SCRATCH"
}

finish() {
  local status=$?
  CLEANUP_RUNNING=yes
  trap - ERR
  config_restore
  local facts=""
  if [ -n "$FACTS_FILE" ] && [ -f "$FACTS_FILE" ]; then facts=$(cat "$FACTS_FILE"); fi
  scratch_down
  if [ -n "$SCRATCH" ]; then
    if [ "$SCRATCH_KEPT" = yes ]; then
      facts="$facts"$'\n'"scratch|INFO|the scratch directory was kept at $SCRATCH (CHECK680_KEEP_SCRATCH is set)"
    else
      facts="$facts"$'\n'"scratch|PASS|the scratch directory was removed from the exit trap"
    fi
  fi
  if [ -n "$UNMEASURED_KEYS" ]; then
    say "this run could NOT measure ${UNMEASURED_KEYS// /, }: read the row(s) above, settle it, and run the check again"
    if [ "$status" -eq 0 ]; then status=2; fi
  fi
  # A run that reached the end with a FAILed row is a run with a finding: exit 1
  # says so without anyone having to read the block for it. An unmeasured row is
  # the exception — it is a FAIL in the block and a 2 above.
  if [ "$status" -eq 0 ] && [ -n "$facts" ]; then
    local failed=no key verdict
    while IFS='|' read -r key verdict _; do
      [ "$verdict" = FAIL ] || continue
      case " ${UNMEASURED_KEYS:-} " in *" $key "*) continue ;; esac
      failed=yes
    done <<<"$facts"
    if [ "$failed" = yes ]; then status=1; fi
  fi
  if [ -z "$PRINTED" ]; then
    PRINTED=1
    print_block "$facts" "$status"
  fi
  exit "$status"
}

# The block's shape does not depend on how far the run got: the keys below are
# printed in this order whatever happened, one that was never reached reads
# `FAIL — not reached`, and any other recorded key follows in the order the run
# recorded it.
BLOCK_KEYS=(xtrace agent-session project belong checkout config login subject
  baseline a-config a-decline a-launch a-executions b-config b-tier b-executions
  c-answer c-run c-cost c-marker c-artifacts c-bundle c-executions restore scratch)

print_block() { # print_block FACTS STATUS — always on the owner's own stdout
  local facts=$1 status=$2
  local key printed=$'\n'
  printf '\nCHECK 680 RESULT\n' >&3
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
    case "$printed" in *$'\n'"$key"$'\n'*) continue ;; esac
    printf 'FACT %s: %s — %s\n' "$key" "$verdict" "$detail" >&3
  done
  printf 'CHECK 680 RESULT END (exit %s)\n' "$status" >&3
}

on_signal() {
  if [ "$CLEANUP_RUNNING" = yes ]; then
    owner "cleanup is running, do not press Ctrl-C again"
    return 0
  fi
  owner "interrupted"
  exit 130
}

# ---------------------------------------------------------------- the readers
# Every document below is read with python3 rather than by pattern, so a field
# this check does not know about cannot be mistaken for one it does. Each reader
# prints `key=value` lines and exits 0 even over a document it cannot read: the
# caller reads a missing key as "the document did not answer", which is a row
# about the installation rather than this check's crash.

field() { # field FILE KEY — the value of one `key=value` line, "" when absent
  sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1 || true
}

# yaml_fields FILE PREFIX: the scalar keys of a config file's own text, as
# `PREFIX<dotted key>=<value>` lines. It walks the file's indentation and builds
# one dotted path per scalar line (`budget:` then `rtdb_url:` is
# budget.rtdb_url), so a key this check wants is read by name and a block it does
# not know about is ignored rather than misread. It is not a YAML parser and does
# not pretend to be one: it reads the subset a project config is written in, and
# a file it cannot walk answers nothing, which is a row rather than a value.
yaml_fields() { # yaml_fields FILE PREFIX
  python3 - "$1" "$2" <<'PY'
import re, sys
path, prefix = sys.argv[1], sys.argv[2]
try:
    text = open(path).read()
except Exception as exc:
    print("%serror=%s" % (prefix, exc))
    raise SystemExit(0)
print("%serror=" % prefix)
stack = []
for raw in text.splitlines():
    if not raw.strip() or raw.lstrip().startswith("#"):
        continue
    m = re.match(r"^(\s*)([A-Za-z0-9_.-]+):(?:[ \t]+(.*))?$", raw)
    if not m:
        continue
    indent, key, value = len(m.group(1)), m.group(2), (m.group(3) or "")
    while stack and stack[-1][0] >= indent:
        stack.pop()
    value = value.split(" #")[0].strip()
    if value in ("", "|", ">"):
        stack.append((indent, key))
        continue
    path_key = ".".join([k for _, k in stack] + [key])
    print("%s%s=%s" % (prefix, path_key, value.strip("\"'")))
PY
}

# yaml_set FILE DOTTED_KEY OLD NEW: one scalar of the file rewritten, by literal
# substitution on the line that holds it and nowhere else. The line is found the
# way yaml_fields walks (indentation, then the key), the old value has to be on
# it exactly once, and a file where that is not true is left untouched and the
# rewrite fails — this check never writes a config it could not read.
yaml_set() { # yaml_set FILE DOTTED_KEY OLD NEW
  python3 - "$1" "$2" "$3" "$4" <<'PY'
import re, sys
path, dotted, old, new = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
want = dotted.split(".")
text = open(path).read()
lines = text.splitlines(True)
stack = []
hits = []
for i, raw in enumerate(lines):
    if not raw.strip() or raw.lstrip().startswith("#"):
        continue
    m = re.match(r"^(\s*)([A-Za-z0-9_.-]+):(?:[ \t]+(.*))?$", raw)
    if not m:
        continue
    indent, key, value = len(m.group(1)), m.group(2), (m.group(3) or "")
    while stack and stack[-1][0] >= indent:
        stack.pop()
    value = value.split(" #")[0].strip()
    if value in ("", "|", ">"):
        stack.append((indent, key))
        continue
    if [k for _, k in stack] + [key] == want:
        hits.append(i)
if len(hits) != 1:
    raise SystemExit("%s holds %d line(s) for %s" % (path, len(hits), dotted))
i = hits[0]
if lines[i].count(old) != 1:
    raise SystemExit("the %s line of %s does not carry %r exactly once" % (dotted, path, old))
lines[i] = lines[i].replace(old, new, 1)
open(path, "w").write("".join(lines))
PY
}

# config_list_fields FILE: one line per installation this machine has, from
# `fugaro config list --json` — its name, its GCP project, its region and the
# checkout paths it remembers.
config_list_fields() { # config_list_fields FILE
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception as exc:
    print("error=%s" % exc)
    raise SystemExit(0)
inst = doc.get("installations")
if not isinstance(inst, list):
    print("error=the document carries no installations list")
    raise SystemExit(0)
print("error=")
print("count=%d" % len(inst))
for row in inst:
    if not isinstance(row, dict):
        continue
    paths = ",".join(sorted(
        (c.get("path") or "") for c in (row.get("checkouts") or [])
        if isinstance(c, dict)))
    print("installation|%s|%s|%s|%s" % (
        row.get("name") or "", row.get("gcp_project") or "",
        row.get("region") or "", paths))
PY
}

json_fields() { # json_fields FILE — the fields of one `fugaro troubleshoot --json` document
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception as exc:
    print("error=%s" % exc)
    raise SystemExit(0)
if not isinstance(doc, dict):
    print("error=the document is not an object")
    raise SystemExit(0)
print("error=")
for key in ("run_id", "command", "tier", "offline", "reason", "cloud_run",
            "cloud_cost_usd", "cost_usd", "outcome", "signature", "source_note"):
    value = doc.get(key)
    if isinstance(value, bool):
        value = "true" if value else "false"
    if value is None:
        value = ""
    print("%s=%s" % (key, str(value).replace("\n", " ")))
answer = doc.get("answer") or ""
first = ""
for line in answer.splitlines():
    if line.strip():
        first = " ".join(line.split())
        break
print("answer_first=%s" % first[:200])
PY
}

record_fields() { # record_fields FILE — the run record's own figures
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception as exc:
    print("error=%s" % exc)
    raise SystemExit(0)
if not isinstance(doc, dict):
    print("error=the record is not an object")
    raise SystemExit(0)
print("error=")
print("outcome=%s" % (doc.get("outcome") or ""))
# cost_usd is the record's own figure and the one the cloud tier reads back
# (troubleshootReadAnswer takes rec.CostUSD); the breakdown below it is the same
# money split by model and compute, and is reported beside it rather than read.
usd = doc.get("cost_usd")
print("cost_usd=%s" % ("" if usd is None else usd))
cost = doc.get("cost") or {}
if not isinstance(cost, dict):
    cost = {}
print("cost_total=%s" % ("" if cost.get("total_usd") is None else cost["total_usd"]))
# execution is the canonical name of the execution this run ran as
# (projects/<p>/locations/<r>/jobs/<job>/executions/<name>): part (c) narrows its
# read to that job and pins the one execution it finds to that name.
execution = doc.get("execution")
print("execution=%s" % (execution if isinstance(execution, str) else ""))
print("artifacts=%s" % ",".join(sorted(
    (row.get("name") or "") for row in (doc.get("artifacts") or [])
    if isinstance(row, dict))))
PY
}

task_fields() { # task_fields FILE — the class marker, wherever the spec holds it
  python3 - "$1" <<'PY'
import json, sys

def walk(node, key):
    if isinstance(node, dict):
        for k, v in node.items():
            if k == key:
                return v
            found = walk(v, key)
            if found is not None:
                return found
    elif isinstance(node, list):
        for item in node:
            found = walk(item, key)
            if found is not None:
                return found
    return None

try:
    doc = json.load(open(sys.argv[1]))
except Exception as exc:
    print("error=%s" % exc)
    raise SystemExit(0)
print("error=")
marker = walk(doc, "review_class")
print("review_class=%s" % ("true" if marker is True else ("false" if marker is False else "")))
for key in ("repo", "workflow", "run_id"):
    value = walk(doc, key)
    print("%s=%s" % (key, value if isinstance(value, str) else ""))
PY
}

answer_fields() { # answer_fields FILE — the answer document the tier reads back
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception as exc:
    print("error=%s" % exc)
    raise SystemExit(0)
if not isinstance(doc, dict):
    print("error=the artifact is not an object")
    raise SystemExit(0)
print("error=")
print("outcome=%s" % (doc.get("outcome") or ""))
print("text=%s" % " ".join((doc.get("text") or "").split())[:200])
PY
}

object_names() { # object_names FILE PREFIX — the objects under one prefix, from a listing
  python3 - "$1" "$2" <<'PY'
import sys
prefix = sys.argv[2]
names = []
try:
    text = open(sys.argv[1]).read()
except Exception:
    raise SystemExit(0)
for line in text.splitlines():
    line = line.strip().rstrip("/")
    if not line:
        continue
    if line.startswith("gs://"):
        line = line[len("gs://"):]
        line = line.split("/", 1)[1] if "/" in line else ""
    if line.startswith(prefix):
        names.append(line)
print("count=%d" % len(names))
print("names=%s" % ",".join(sorted(names)))
PY
}

# ------------------------------------------------------------- the fugaro side
# fg: one fugaro call, with the environment the cloud tier needs and nothing
# this check has no business inheriting. No config selector is inherited (a
# $FUGARO_PROJECT or $FUGARO_CONFIG naming another project would be selected
# over the checkout's own, and the check would doctor a file the CLI never
# reads); the two credential variables are unset and `claude` is off PATH, which
# is the only state in which the cloud tier is the tier that answers (D2400).
# The check passes no credential of any kind and reads none.
FG_ENV=(env -u FUGARO_PROJECT -u FUGARO_CONFIG -u CLAUDE_CODE_OAUTH_TOKEN -u ANTHROPIC_API_KEY)

fg() {
  "${FG_ENV[@]}" PATH="$NOCLAUDE_PATH" \
    fugaro "$@"
}

# fg_bounded: the same call with the --cloud-seconds bound in front of the binary.
# Every call of this check that reaches the cloud goes through this — the
# deliberately refused launch of part (a), the diagnosis of part (c), and the two
# `troubleshoot` reads of (a) and (b), each of which launches a job execution
# whenever its tier answers rather than declining — so a call that hangs is the
# bound firing rather than a check that never ends. The bound has to be
# put in front of `fugaro` itself and not around `fg`, which is a shell function
# and not something timeout(1) can exec. It is `timeout`'s own bound, so it holds
# where the machine has one (GNU coreutils; macOS ships none, which is the caveat
# Check 310's own bound carries) and without one the call is unbounded and yours
# to interrupt.
fg_bounded() {
  local t=""
  if [ "$CLOUD_SECONDS" -gt 0 ]; then t=$(command -v timeout 2>/dev/null || true); fi
  if [ -n "$t" ]; then
    "${FG_ENV[@]}" PATH="$NOCLAUDE_PATH" \
      "$t" "$CLOUD_SECONDS" fugaro "$@"
  else
    fg "$@"
  fi
}

# build_noclaude_path: PATH with every directory holding an executable `claude`
# dropped — the whole of what "this machine has no local login" means to the tier
# (the two variables unset and no claude to run). The tools this check calls are
# linked into the scratch directory first, so dropping a directory that also held
# `fugaro` or `gcloud` does not take them with it.
build_noclaude_path() {
  local path=$PATH dir tool abs dirs=() keep="" dropped=""
  local -a tools=(fugaro gcloud git python3 timeout sed grep awk)
  mkdir -p "$SCRATCH/bin"
  for tool in "${tools[@]}"; do
    abs=$(command -v "$tool" || true)
    [ -n "$abs" ] || continue
    case "$abs" in /*) ;; *) abs=$PWD/$abs ;; esac
    ln -sf "$abs" "$SCRATCH/bin/$tool"
  done
  IFS=: read -r -a dirs <<<"$path"
  for dir in "${dirs[@]}"; do
    [ -n "$dir" ] || continue
    if [ -x "$dir/claude" ]; then
      dropped="${dropped:+$dropped }$dir"
      continue
    fi
    keep="${keep:+$keep:}$dir"
  done
  NOCLAUDE_PATH="$SCRATCH/bin:$keep"
  CLAUDE_DROPPED=$dropped
}

claude_on_path() {
  env -u CLAUDE_CODE_OAUTH_TOKEN -u ANTHROPIC_API_KEY PATH="$NOCLAUDE_PATH" \
    bash -c 'command -v claude 2>/dev/null || true' 2>/dev/null || true
}

# --------------------------------------------------------------- the gcloud side
# executions_since FILE SINCE_EPOCH [JOB] — what was created at or after
# SINCE_EPOCH, read as a window on creation time and never as a total.
#
# What the rows below rest on is always "what did this part create": (a) is "the
# refused launch created none", (c) is "the diagnosis created exactly one". A
# total answers that only while the project is small — a count read through a
# limit saturates, so on a project holding as many executions as the limit, a
# refused launch that created one and a refused launch that created none read the
# same number, and (a)'s safety row would pass over the very leak it exists to
# find. A project that has run for weeks holds thousands of them, which is how
# the read of a total stopped this check at its first row.
#
# So the read is a window: the newest EXECUTIONS_LIMIT executions of one job (or
# of the project's jobs, where no job is named), asked for newest first, of which
# this counts the ones whose creationTimestamp is at or after SINCE_EPOCH. How
# many executions the project holds elsewhere never enters it, and no limit can
# saturate it: a page that is full AND sits entirely inside the window is
# reported as `exact=no` — a floor rather than a guess.
#
# The comparison is made here, in python3, and not by a gcloud --filter: a filter
# whose key gcloud does not know answers an empty list, which would read as
# "nothing was created" — a PASS out of a read that measured nothing at all. A
# page that is not newest-first (a --sort-by this gcloud ignored) is a failure
# for the same reason: what sits behind the window's edge would be invisible.
#
# Prints `read=`, `count=` (the rows at or after SINCE_EPOCH), `exact=` and
# `names=` (those rows' names, newest first) on stdout, and answers non-zero with
# the reason on FILE.err where the list itself could not be read.
executions_since() { # executions_since FILE SINCE_EPOCH [JOB]
  local file=$1 since=$2 job=${3:-}
  local common=(--project "$PROJECT" --region "$REGION"
    --sort-by='~metadata.creationTimestamp' --limit="$EXECUTIONS_LIMIT" --format=json)
  if [ -n "$job" ]; then
    gcloud run jobs executions list --job "$job" "${common[@]}" >"$file" 2>"$file.err" || return 1
  else
    gcloud run jobs executions list "${common[@]}" >"$file" 2>"$file.err" || return 1
  fi
  python3 - "$file" "$since" "$EXECUTIONS_LIMIT" <<'PY' 2>>"$file.err" || return 1
import datetime, json, re, sys

STAMP = re.compile(r"^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d+))?(Z|[+-]\d{2}:?\d{2})?$")


def epoch(stamp):
    m = STAMP.match(stamp.strip())
    if not m:
        raise ValueError(stamp)
    base, frac, off = m.group(1), (m.group(2) or "")[:6].ljust(6, "0"), m.group(3)
    iso = "%s.%s%s" % (base, frac, "+00:00" if off in (None, "Z", "+00:00") else off)
    return datetime.datetime.fromisoformat(iso).timestamp()


path, since, limit = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
try:
    rows = json.load(open(path))
except Exception as exc:
    print("gcloud's answer is not a document this check can read (%s)" % exc, file=sys.stderr)
    raise SystemExit(1)
if not isinstance(rows, list):
    print("gcloud's answer is not the list of executions this check reads", file=sys.stderr)
    raise SystemExit(1)
seen = []
for row in rows:
    meta = row.get("metadata") if isinstance(row, dict) else None
    meta = meta if isinstance(meta, dict) else {}
    name, stamp = meta.get("name"), meta.get("creationTimestamp")
    if not isinstance(name, str) or not name or not isinstance(stamp, str) or not stamp:
        print("an execution of this answer names no name or no creation time", file=sys.stderr)
        raise SystemExit(1)
    try:
        seen.append((epoch(stamp), name))
    except Exception:
        print("an execution of this answer carries a creation time this check cannot read (%s)" % stamp, file=sys.stderr)
        raise SystemExit(1)
for older, newer in zip(seen, seen[1:]):
    if newer[0] > older[0]:
        print("gcloud answered these executions oldest last and not newest first, so the window's edge is not on this page", file=sys.stderr)
        raise SystemExit(1)
inside = [name for when, name in seen if when >= since]
print("read=%d" % len(seen))
print("count=%d" % len(inside))
print("exact=%s" % ("no" if len(seen) >= limit and len(inside) == len(seen) else "yes"))
print("names=%s" % ",".join(inside))
PY
}

# window_start: the moment a window opens, printed as `EPOCH STAMP`. One instant,
# read once, so the epoch the comparison uses and the timestamp the row prints
# cannot be a second apart.
window_start() {
  python3 -c 'import datetime; n = datetime.datetime.now(datetime.timezone.utc); print(int(n.timestamp()), n.strftime("%Y-%m-%dT%H:%M:%SZ"))'
}

# -------------------------------------------------------------------- the steps
step_setup() {
  fg config list --json >"$SCRATCH/configlist.json" 2>"$SCRATCH/configlist.err" || true
  config_list_fields "$SCRATCH/configlist.json" >"$SCRATCH/configlist.txt"
  local error count
  error=$(field "$SCRATCH/configlist.txt" error)
  if [ -n "$error" ]; then
    unmeasured project "fugaro config list --json answered a document this check cannot read ($error): $(last_lines "$SCRATCH/configlist.err"). That document is the read-only, credential-free list of this machine's installations, and it is the only way this check reads the installation it doctors"
    stop "the installations of this machine could not be listed"
  fi
  count=$(field "$SCRATCH/configlist.txt" count)

  CHECKOUT=${CHECKOUT:-$PWD}
  local root
  root=$(git -C "$CHECKOUT" rev-parse --show-toplevel 2>/dev/null || true)
  if [ -z "$root" ]; then
    unmeasured checkout "$CHECKOUT is not inside a git checkout (git rev-parse --show-toplevel answered nothing): the cloud tier is scoped to the checkout's repository (D2409), so this check is run from the sandbox checkout"
    stop "no checkout, so nothing to scope a launch or a diagnosis to"
  fi
  CHECKOUT=$root
  if [ -z "$REPO" ]; then
    REPO=$(git -C "$CHECKOUT" remote get-url origin 2>/dev/null |
      sed -e 's#^git@[^:]*:##' -e 's#^https\?://[^/]*/##' -e 's#\.git$##' -e 's#/$##' || true)
  fi
  case "$REPO" in
  */*/*) stop "the checkout's origin does not read as owner/name ('$REPO'): pass --repo <owner/name>" ;;
  "") stop "the checkout's origin could not be read, so the repository is unknown: pass --repo <owner/name>" ;;
  esac
  fact checkout PASS "the checkout is $CHECKOUT (git rev-parse --show-toplevel) and its origin is $REPO, which is the repository both the deliberately refused launch and the three diagnoses are scoped to (D2409)"

  # The installation: named, or the one whose own record remembers this checkout,
  # or the only one there is. More than one match, or several installations and
  # no match, is a row rather than a guess: doctoring the wrong installation's
  # config is the one thing this check must never do.
  local pick="" line lname lgcp lregion lpaths matched=0
  while IFS='|' read -r _ lname lgcp lregion lpaths; do
    [ -n "$lname" ] || continue
    if [ -n "$NAME" ]; then
      [ "$lname" = "$NAME" ] || continue
      pick="$lname|$lgcp|$lregion"
      matched=$((matched + 1))
      continue
    fi
    case ",$lpaths," in
    *",$CHECKOUT,"*)
      pick="$lname|$lgcp|$lregion"
      matched=$((matched + 1))
      ;;
    esac
  done < <(grep '^installation|' "$SCRATCH/configlist.txt" || true)
  if [ -n "$NAME" ] && [ "$matched" -eq 0 ]; then
    unmeasured project "--name $NAME is not one of the $count installation(s) this machine has ($(sed -n 's/^installation|\([^|]*\).*/\1/p' "$SCRATCH/configlist.txt" | tr '\n' ' ')): nothing was read and nothing was written"
    stop "the installation --name names is not on this machine"
  fi
  if [ "$matched" -gt 1 ]; then
    unmeasured project "$matched installations on this machine match ($(sed -n 's/^installation|\([^|]*\).*/\1/p' "$SCRATCH/configlist.txt" | tr '\n' ' ')): this check writes the installation's own project config, so it will not guess between them. Pass --name <the project config's name>"
    stop "more than one installation matches, and this check does not guess between them"
  fi
  if [ "$matched" -eq 0 ] && [ "$count" = 1 ]; then
    while IFS='|' read -r _ lname lgcp lregion _; do
      [ -n "$lname" ] || continue
      pick="$lname|$lgcp|$lregion"
    done < <(grep '^installation|' "$SCRATCH/configlist.txt" || true)
  fi
  if [ -z "$pick" ]; then
    unmeasured project "this machine has $count installation(s) and none of them remembers a checkout at $CHECKOUT: this check reads the installation a checkout belongs to, so pass --name <the project config's name>, or run it from a checkout one of them remembers. Nothing was read and nothing was written"
    stop "which installation this checkout belongs to could not be told"
  fi
  NAME=${pick%%|*}
  local rest=${pick#*|}
  PROJECT=${PROJECT:-${rest%%|*}}
  REGION=${REGION:-${rest##*|}}
  case "$NAME$CHECKOUT$PROJECT" in
  *belong*)
    stop "the installation this checkout resolves to names belong (name '$NAME', project '$PROJECT'): this check is never run there, and nothing was read"
    ;;
  esac
  if [ "$PROJECT" = fugaro-prod ]; then
    stop "the installation is fugaro-prod: this check rolls the installation's own base image back and rewrites its project config for the length of two parts, and that is not done to production. Run it on the sandbox installation"
  fi
  REAL_PROJECT=$PROJECT
  fact project PASS "the installation is '$NAME' — GCP project $PROJECT, region $REGION — read through fugaro config list --json (read-only, offline, no credential) and picked as the one that remembers the checkout $CHECKOUT. This is the only read this check makes of the installation before it starts doctoring"

  if [ -z "$CONFIG" ]; then
    CONFIG="${XDG_CONFIG_HOME:-$HOME/.config}/fugaro/projects/$NAME.yaml"
  fi
  case "$CONFIG" in /*) ;; *) CONFIG=$PWD/$CONFIG ;; esac
  CONFIG_BACKUP="$CONFIG.check680-backup"
  if [ ! -f "$CONFIG" ]; then
    unmeasured config "the project config this check doctors is not there: $CONFIG (the name's own file under \$XDG_CONFIG_HOME/fugaro/projects, or a --config path). Nothing was read and nothing was written"
    stop "the project config file could not be found, so no part of this check could arrange anything"
  fi
  yaml_fields "$CONFIG" "" >"$SCRATCH/config.txt"
  BASE_OLD=$(field "$SCRATCH/config.txt" base_images.base)
  local cfg_gcp cfg_bucket cfg_error
  cfg_error=$(field "$SCRATCH/config.txt" error)
  cfg_gcp=$(field "$SCRATCH/config.txt" gcp_project)
  cfg_bucket=$(field "$SCRATCH/config.txt" runs_bucket)
  BUCKET=${BUCKET:-${cfg_bucket:-fugaro-runs-$PROJECT}}
  if [ -z "$BASE_OLD" ]; then
    unmeasured config "$CONFIG records no base_images.base${cfg_error:+ ($cfg_error)}: that ref is the image a checkout: clone workflow runs and the one part (a) rolls back, and a config without one has nothing to arrange"
    stop "the installation records no base image, so part (a) could not be arranged"
  fi
  if [ -n "$cfg_gcp" ]; then HAS_GCP_LINE=yes; fi
  if grep -qE '^[[:space:]]*rtdb_url[[:space:]]*:' "$CONFIG"; then HAS_RTDB_LINE=yes; fi
  if grep -qE '^[[:space:]]*firebase_project[[:space:]]*:' "$CONFIG"; then HAS_FP_LINE=yes; fi
  if grep -qE '^budget[[:space:]]*:' "$CONFIG"; then HAS_BUDGET=yes; fi
  BUDGET_MODE=$(field "$SCRATCH/config.txt" budget.mode)
  CONFIG_ORIGINAL_HASH=$(file_hash "$CONFIG")
  fact config PASS "$CONFIG is the installation's own project config and the ONE file this check writes: its base image is $BASE_OLD (the ref part (a) rolls back), its runs bucket is $BUCKET${HAS_GCP_LINE:+, its gcp_project line reads $cfg_gcp}${HAS_BUDGET:+ and it carries a budget block (mode '${BUDGET_MODE:-none}'${HAS_FP_LINE:+, firebase_project}${HAS_RTDB_LINE:+, rtdb_url})}. Every write below replaces one literal value on one line of this file, a copy of it is kept at $CONFIG_BACKUP from before the first write, and what proves the CLI reads THIS file is the tier's own behaviour in (a) and (b) — not a second read this check takes"

  # The PATH every `fugaro` call runs with is built before the first call:
  # `fg` is used from step_setup on, and an empty PATH there would make the
  # call fail with the tool not found rather than with the template's refusal.
  build_noclaude_path
  local claude
  claude=$(claude_on_path)
  fact login PASS "the three diagnoses run with no local login, which is the only state the cloud tier answers in: CLAUDE_CODE_OAUTH_TOKEN and ANTHROPIC_API_KEY are unset and \`claude\` is off PATH${CLAUDE_DROPPED:+ (dropped from PATH: $CLAUDE_DROPPED)} (it resolves to '${claude:-nothing}'). D2400: with a local login the local tier answers instead, and this check will not take yours away"

  step_subject

  # The window every part below is read against, opened before anything of this
  # check is arranged, and the read that proves the list can be read at all — so
  # a project whose executions cannot be listed stops the check here, before it
  # writes a byte of the installation's config.
  read -r SINCE_CHECK SINCE_CHECK_STAMP <<<"$(window_start)"
  if ! executions_since "$SCRATCH/executions.before" "$SINCE_CHECK" >"$SCRATCH/before.txt"; then
    unmeasured baseline "gcloud run jobs executions list --project $PROJECT --region $REGION --sort-by '~metadata.creationTimestamp' --limit $EXECUTIONS_LIMIT answered nothing this check can count ($(last_lines "$SCRATCH/executions.before.err")): every row below is a count of what was created at or after $SINCE_CHECK_STAMP, so nothing could be measured without it"
    stop "the project's executions could not be listed, so no part of this check could read what it created"
  fi
  local opened names
  opened=$(field "$SCRATCH/before.txt" count)
  names=$(field "$SCRATCH/before.txt" names)
  if [ "$opened" != 0 ]; then
    fact baseline FAIL "$opened execution(s) of $PROJECT were created in the second the window opened ($SINCE_CHECK_STAMP — $names): a real run of this project's jobs was going on as this check started, and every row below counts what was created since, so it would read that run as this check's own work. Wait for the runs of $PROJECT to finish and run the check again"
    stop "a run of the project was going on when the window opened"
  fi
  fact baseline PASS "the window every part below is read against opened at $SINCE_CHECK_STAMP, with no execution of $PROJECT created in it: gcloud run jobs executions list --project $PROJECT --region $REGION --sort-by '~metadata.creationTimestamp' --limit $EXECUTIONS_LIMIT (the newest $EXECUTIONS_LIMIT executions, newest first) answers the rows at or after that moment, so parts (a) and (b) read 'no execution created since $SINCE_CHECK_STAMP' and part (c) reads exactly one — and how many older executions the project holds never enters any of them"
}

# step_subject: the failed command the three parts diagnose. The check writes no
# log and invents none: it takes the newest log the CLI itself would take
# (cmdlog.Latest's rule — the newest `<command>-<14 digits>-<pid>.log` whose
# closing line is the reporter's `Failed in …` or `Left for you in …`) and passes
# its ID explicitly. A machine with no failed command logged has nothing for this
# check to diagnose, and says so rather than measuring a log of its own.
step_subject() {
  local dir=${LOGS_DIR:-${XDG_STATE_HOME:-$HOME/.local/state}/fugaro/logs}
  if [ -n "$SUBJECT" ]; then
    if [ ! -f "$dir/$SUBJECT.log" ]; then
      unmeasured subject "--subject names $SUBJECT and there is no log at $dir/$SUBJECT.log: the tier diagnoses a log this machine wrote, and the ID is the log's own file name"
      stop "the log --subject names is not there, so there is nothing to diagnose"
    fi
  else
    if [ ! -d "$dir" ]; then
      unmeasured subject "no failed fugaro command is logged on this machine: $dir is not there. Run a command that fails — a sandbox \`fugaro run\` whose launch the installation's gates refuse is the one this check is written around — then run this check again. The tier explains a failure this machine logged, and this check measures nothing before there is one"
      stop "nothing failed is logged, so there is no failure for the cloud tier to explain"
    fi
    local file base last newest=""
    while IFS= read -r file; do
      base=${file##*/}
      case "${base%.log}" in
      *-[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-[0-9]*) ;;
      *) continue ;;
      esac
      last=$(grep -v '^[[:space:]]*$' "$file" 2>/dev/null | tail -n 1 || true)
      case "$last" in
      "Failed in "* | "Left for you in "*) newest=${base%.log} ;;
      *) continue ;;
      esac
      [ -n "$newest" ] && break
    done < <(ls -t "$dir"/*.log 2>/dev/null || true)
    if [ -z "$newest" ]; then
      unmeasured subject "no FAILED fugaro command is logged in $dir (a log whose closing line is 'Failed in …' or 'Left for you in …'): run a command that fails — a sandbox \`fugaro run\` whose launch the installation's gates refuse is the one this check is written around — then run this check again"
      stop "nothing failed is logged, so there is no failure for the cloud tier to explain"
    fi
    SUBJECT=$newest
  fi
  SUBJECT_COMMAND=${SUBJECT%%-*}
  if grep -qF '[fugaro-cause:' "$dir/$SUBJECT.log"; then
    unmeasured subject "the failed log is $SUBJECT and it names a cause this CLI recognises in its own text (a [fugaro-cause: …] marker): the RULES tier answers that one from the log alone, with no model call, and none of D2408's three parts is about it. Clear that failure from the machine's log, or pass --subject with a log that names no cause, then run the check again"
    stop "the log this check would diagnose is one the rules tier answers, so the cloud tier is never reached over it"
  fi
  fact subject PASS "the failed command this check diagnoses is $SUBJECT (command '$SUBJECT_COMMAND'), taken by the CLI's own rule when --subject was not given — the newest log under $dir whose closing line is 'Failed in …' or 'Left for you in …' — and its own text names no cause the CLI recognises, so the rules tier does not take it. Every diagnosis below passes $SUBJECT explicitly and holds the document's own run_id to it"
}

# arrange_base: the local config's base image rolled back to an older release.
# Only the base_images.base line is touched, and only through the literal value
# yaml_fields read, so nothing else in the file can move.
arrange_base() {
  local release new
  release=$(printf '%s' "$BASE_OLD" | grep -oE ':[0-9]+\.[0-9]+\.[0-9]+' | head -n 1 | tr -d ':' || true)
  if [ -z "$release" ]; then
    unmeasured a-config "$CONFIG's base image ($BASE_OLD) carries no release tag, and a base with no release is never judged by the image gates — only warned about (releaseOf). This check cannot roll it back to an older release, so part (a) cannot be arranged here"
    stop "the installation's base image has no release to roll back"
  fi
  new=$(printf '%s' "$BASE_OLD" | sed -e "s#:$release#:$BAD_RELEASE#" | head -n 1)
  if [ "$new" = "$BASE_OLD" ]; then
    unmeasured a-config "the check could not build an older ref from $BASE_OLD: it would have replaced the release $release with $BAD_RELEASE"
    stop "the base image ref could not be rolled back"
  fi
  if ! yaml_set "$CONFIG" base_images.base "$release" "$BAD_RELEASE" 2>"$SCRATCH/yaml_set.err"; then
    unmeasured a-config "the base image line of $CONFIG could not be rewritten ($(last_lines "$SCRATCH/yaml_set.err")): this check writes a config only where it read one value, and nothing was written"
    stop "the base image could not be rolled back in the config"
  fi
  BASE_NEW=$new
  fact a-config PASS "the installation's base image is rolled back to a release older than this release's policy, in the installation's own config and nowhere else: $BASE_OLD -> $BASE_NEW in $CONFIG (the line's release tag replaced, nothing else in the file moved). What that costs a checkout: clone workflow is that its launch — and the diagnosis's pre-flight, which calls the launch path's own gates (F8) — refuse it; whether the CLI really reads this file is what the decline below reads"
}

# judge_of: which judge a refusal sentence names. `clone` is the local config's
# base image (checkCloneImageSince's sentence, the gate a rolled-back base image
# refuses). Anything else that is still the image gate's sentence is the build
# record in the runs bucket (checkImageSinceDev): this check does not doctor that
# bucket, and part (a) cannot be arranged against it.
judge_of() { # judge_of "sentence text"
  case "$1" in
  *"the checkout: clone job image of"*) printf 'clone' ;;
  *"needs a job image whose runner knows"*) printf 'other' ;;
  *) printf 'unknown' ;;
  esac
}

sentence_release() { printf '%s' "$1" | grep -oE '\(fugaro [0-9]+\.[0-9]+\.[0-9]+ or later\)' | head -n 1 || true; }
sentence_workflow() { printf '%s' "$1" | sed -n 's/.*workflow \([^ ]*\) .*/\1/p' | head -n 1 || true; }

step_decline() {
  # (a)'s own window, opened before the tier's read: the tier's read must upload
  # nothing and launch nothing, and the refused launch below must not either.
  read -r SINCE_A SINCE_A_STAMP <<<"$(window_start)"
  local status=0
  fg_bounded troubleshoot "$SUBJECT" --json >"$SCRATCH/decline.json" 2>"$SCRATCH/decline.err" || status=$?
  if [ "$status" = 124 ]; then
    unmeasured a-decline "fugaro troubleshoot $SUBJECT was still running when the ${CLOUD_SECONDS}s bound (--cloud-seconds) ended it: the tier's own timeout is fifteen minutes and this bound is that timeout and slack, so a read the bound had to end is one whose job was still going — the run it launched may be there still, and its record is what to read before running this check again"
    stop "the tier's own read did not come back inside the bound, so (a) was not measured"
  fi
  DECLINE_STATUS=$status
  json_fields "$SCRATCH/decline.json" >"$SCRATCH/decline.txt"
  local error tier offline reason run_id
  error=$(field "$SCRATCH/decline.txt" error)
  if [ -n "$error" ]; then
    unmeasured a-decline "fugaro troubleshoot $SUBJECT --json answered no document this check can read ($error) over the rolled-back base image: $(last_lines "$SCRATCH/decline.err")"
    stop "the tier's own document could not be read, so nothing about (a) was measured"
  fi
  tier=$(field "$SCRATCH/decline.txt" tier)
  offline=$(field "$SCRATCH/decline.txt" offline)
  reason=$(field "$SCRATCH/decline.txt" reason)
  run_id=$(field "$SCRATCH/decline.txt" run_id)
  if [ "$run_id" != "$SUBJECT" ]; then
    unmeasured a-decline "the document diagnoses run_id '$run_id' and this check asked for '$SUBJECT': the tier answered about another log, and no row below would be about the failure this check read"
    stop "the tier diagnosed another log than the one this check passed"
  fi
  case "$tier" in
  cloud | local | rules)
    fact a-decline FAIL "the cloud tier was NOT the tier that declined: with the installation's job image rolled back to $BAD_RELEASE — a release the image gates refuse — fugaro troubleshoot answered from the '$tier' tier instead (offline='$offline', reason='$(one_line "$reason")'). D2400's order puts the cloud job first for a machine with no local login, and D2401 puts the launch path's gates in front of it, so a tier above the cloud one answering this failure is either a gate this release does not have or a log whose cause this CLI should not be answering"
    stop "the tier above the cloud one answered a failure the launch path's gates refuse, which is D2401's own case"
    ;;
  evidence) ;;
  *)
    unmeasured a-decline "the document names tier '${tier:-nothing}' with reason '$(one_line "$reason")': this check reads D2400's four tiers by name (rules, local, cloud, evidence) and cannot hold an answer it cannot name"
    stop "the tier in the document is not one of D2400's four"
    ;;
  esac
  if [ "$offline" != true ]; then
    unmeasured a-decline "the tier is 'evidence' and the document's offline field is '${offline:-empty}': the evidence path is the one that answers when no diagnosis ran (D1608), and a document that says otherwise is not the shape this check reads"
    stop "the evidence tier's document is not the shape this check reads"
  fi
  if [ "$DECLINE_STATUS" = 0 ]; then
    fact a-decline FAIL "the declined cloud tier exited 0: D1608 exits non-zero because the evidence path explains nothing by itself, and a caller that read 0 as an answer would take the evidence for a diagnosis"
    stop "the declined tier exited 0"
  fi
  case "$reason" in
  "the cloud diagnosis is not available: "*)
    case "$reason" in
    *". The evidence follows."*) ;;
    *)
      unmeasured a-decline "the reason begins with D2401's decline and does not carry its closing sentence: it reads '$(one_line "$reason")'. This check reads the sentence as the CLI writes it"
      stop "the decline is not D2401's sentence as this CLI writes it"
      ;;
    esac
    ;;
  *)
    unmeasured a-decline "the reason is not D2401's decline ('the cloud diagnosis is not available: <the gate's sentence>. The evidence follows.'): it reads '$(one_line "$reason")'. That sentence is the pre-flight's own (troubleshootDeclineCloud), so a reason without it means the tier declined somewhere else — the plan's own decline (D2409), the source tree, or the credential — and this check arranged none of those"
    stop "the decline is not D2401's sentence, so the tier declined before the launch path's gates"
    ;;
  esac
  case "$reason" in
  *"needs a job image whose runner knows"*"fugaro update"*) ;;
  *)
    unmeasured a-decline "the decline is D2401's sentence and the gate it quotes is not the image gate's ('$(one_line "$reason")'): the tier declined, and for a reason this check did not arrange — another gate of the launch path refuses this installation's checkout, which is a fact about the sandbox rather than a reading of D2401. It names its own fix: settle what that sentence names and run the check again"
    stop "the tier declined, but not over the image this check rolled back"
    ;;
  esac
  A_REASON=$reason
  GATE_SENTENCE=$reason
  GATE_RELEASE=$(sentence_release "$reason")
  GATE_WORKFLOW=$(sentence_workflow "$reason")
  JUDGE=$(judge_of "$reason")
  fact a-decline PASS "fugaro troubleshoot $SUBJECT with no local login, over the rolled-back base image, declines the cloud TIER rather than launching: tier 'evidence', offline true, exit $DECLINE_STATUS, in D2401's own sentence — '$(one_line "$reason")'. It quotes the gate's sentence as the gate wrote it, naming the release the job image needs (${GATE_RELEASE:-none}), the workflow (${GATE_WORKFLOW:-none}) and the command that fixes it (fugaro update), and no execution was created over it (below). This is also the proof that the file this check rewrote is the file the CLI read: the pre-flight judged the base image line of $CONFIG"
}

step_launch() {
  if [ "$JUDGE" != clone ]; then
    unmeasured a-launch "the tier's decline named the BUILD RECORD as the judge of $REPO's job image ('$(one_line "$GATE_SENTENCE")'): this workflow is not checkout: clone, so its image comes from the runs bucket's build record — the roll-back this check made in the local config is not what its launch meets, and a launch here would not be refused. This check does not doctor the runs bucket and will not run a launch it cannot show will be refused. Measure (a) on a checkout: clone workflow (the sandbox repository's own, whose base image is the installation's), then run the check again"
    stop "the workflow's image is judged from the build record, so no refused launch could be arranged here"
  fi
  local status=0
  fg_bounded run --repo "$REPO" "$TASK" >"$SCRATCH/launch.out" 2>"$SCRATCH/launch.err" || status=$?
  local out sentence lrelease lworkflow
  out=$(cat "$SCRATCH/launch.out" "$SCRATCH/launch.err" 2>/dev/null || true)
  if [ "$status" = 124 ]; then
    fact a-launch FAIL "the deliberate launch was still running when the ${CLOUD_SECONDS}s bound (--cloud-seconds) ended it: the gate this check arranged refuses it in a sentence, before anything is launched, so a launch that had to be ended is one the launch path did not refuse — and a run admitted there is a run that is running now. Read this project's own \`fugaro ls\` and the executions below for what it left"
    stop "the launch did not come back inside the bound, so it was not refused"
  fi
  if [ "$status" = 0 ]; then
    fact a-launch FAIL "the deliberate launch was NOT refused: \`fugaro run --repo $REPO\` exited 0 while the installation's base image was rolled back to $BASE_NEW, which the tier's own pre-flight had just refused. The launch path and the diagnosis's pre-flight are one block (F8) and they disagree about the same image — and on a live installation this launch may have started a run"
    stop "the launch was admitted over an image the pre-flight refuses"
  fi
  sentence=$(printf '%s\n' "$out" | grep -m1 'needs a job image whose runner knows' || true)
  if [ -z "$sentence" ]; then
    unmeasured a-launch "the launch was refused, and not over the job image: '$(last_lines "$SCRATCH/launch.err")'. The tier's decline named the image gate, so a launch meeting another gate first means the arrangement this check made is not the one the launch judges — a fact about the sandbox rather than a reading of D2401"
    stop "the launch was refused over something other than the image"
  fi
  lrelease=$(sentence_release "$sentence")
  lworkflow=$(sentence_workflow "$sentence")
  if [ -n "$GATE_RELEASE" ] && [ "$lrelease" != "$GATE_RELEASE" ]; then
    fact a-launch FAIL "the launch's refusal names the required release $lrelease and the decline quoted $GATE_RELEASE: the tier is quoting a different gate than the one the launch meets, so the sentence a person reads about their next launch is not the sentence their launch prints"
    stop "the launch's gate and the tier's quoted gate require different releases"
  fi
  fact a-launch PASS "the deliberately refused launch is refused by the gate the tier quoted: \`fugaro run --repo $REPO\` exited $status with '$(one_line "$sentence")', naming the same required release (${lrelease:-none}) the decline carried${lworkflow:+, the workflow $lworkflow} and the same fix (fugaro update). The launch was run at all only because the decline above had already read this gate as the judge — the checkout: clone base image, this check's own roll-back of $CONFIG — so nothing behind it could have started"
}

step_no_execution() { # the whole of (a), read once at the end of it
  if ! executions_since "$SCRATCH/executions.after.a" "$SINCE_A" >"$SCRATCH/after.a.txt"; then
    unmeasured a-executions "gcloud run jobs executions list answered nothing after the refused launch ($(last_lines "$SCRATCH/executions.after.a.err")): this check will not report 'no execution created' from a list it could not read"
    stop "the executions could not be listed after the refused launch"
  fi
  local after names rows
  after=$(field "$SCRATCH/after.a.txt" count)
  names=$(field "$SCRATCH/after.a.txt" names)
  rows=$(field "$SCRATCH/after.a.txt" read)
  if [ "$after" != 0 ]; then
    fact a-executions FAIL "the refused launch created $after execution(s) — $names — in $PROJECT at or after $SINCE_A_STAMP (of the $rows newest execution(s) gcloud answered, which is what this row counts). D2401's decline exists so that a broken launch path costs a person a sentence and not a job that fails the same way a second time. A real run of this project's jobs going on during the check looks exactly the same: read the names before taking this for a leak, because nothing this check runs may create an execution at all"
    stop "the refused launch created an execution"
  fi
  fact a-executions PASS "the refused launch created no execution: gcloud answers no execution of $PROJECT created at or after $SINCE_A_STAMP (gcloud run jobs executions list --project $REAL_PROJECT --region $REGION --sort-by '~metadata.creationTimestamp' --limit $EXECUTIONS_LIMIT, read newest first and counted in the window), so what the gate refused never reached the backend — and the tier's own refusal to launch over it (a-decline, above) uploaded nothing either. No total of the project's executions was read, so how many older ones it holds is not part of this row"
}

step_unavailable() {
  local absent=$ABSENT_PROJECT old=$REAL_PROJECT
  case "$absent" in
  "" | "$old")
    unmeasured b-config "--absent-project has to name a project that is not $old, and it reads '$absent'"
    stop "the absent project was not named"
    ;;
  esac
  if gcloud projects describe "$absent" >"$SCRATCH/absent.out" 2>"$SCRATCH/absent.err"; then
    unmeasured b-config "gcloud projects describe $absent answered with a project: part (b) needs a project that is NOT there — the tier's own reads are made in its project, and one that cannot be read is what makes the tier unavailable — and $absent exists"
    stop "the project part (b) would point the tier at exists"
  fi
  if [ "$HAS_GCP_LINE" != yes ]; then
    unmeasured b-config "$CONFIG carries no gcp_project line this check could point at $absent (it is read by its dotted key 'gcp_project:'), so the tier cannot be made unavailable this way. Nothing was written"
    stop "the config has no gcp_project line to point elsewhere"
  fi
  if ! yaml_set "$CONFIG" gcp_project "$old" "$absent" 2>"$SCRATCH/yaml_set.err"; then
    unmeasured b-config "the gcp_project line of $CONFIG could not be rewritten ($(last_lines "$SCRATCH/yaml_set.err")): nothing was written"
    stop "the config's project could not be pointed at an absent project"
  fi
  local rewrote="gcp_project $old -> $absent"
  # The budget block's own project, when the config records one: the tier's
  # budget read is the first thing it cannot make in a project that is not there,
  # and these are the two keys localcfg validates as a project id and a Firebase
  # database URL. A config without them is still arranged by the project line
  # alone, which is what the row reports.
  if [ "$HAS_FP_LINE" = yes ]; then
    local fp
    fp=$(field "$SCRATCH/config.txt" budget.firebase_project)
    if [ -n "$fp" ] && [ "$fp" != "$absent" ] &&
      yaml_set "$CONFIG" budget.firebase_project "$fp" "$absent" 2>/dev/null; then
      rewrote="$rewrote, budget.firebase_project $fp -> $absent"
    fi
  fi
  if [ "$HAS_RTDB_LINE" = yes ]; then
    local url host newurl
    url=$(field "$SCRATCH/config.txt" budget.rtdb_url)
    if [ -n "$url" ]; then
      host=$(printf '%s' "$url" | sed -n 's#^https\?://\([^/]*\).*#\1#p')
      newurl=$(printf '%s' "$url" |
        sed -e "s#//$host#//$(printf '%s' "$absent" | tr 'A-Z' 'a-z')-default-rtdb.firebaseio.com#")
      if [ -n "$host" ] && [ "$newurl" != "$url" ] &&
        yaml_set "$CONFIG" budget.rtdb_url "$url" "$newurl" 2>/dev/null; then
        rewrote="$rewrote, budget.rtdb_url -> $newurl"
      fi
    fi
  fi
  PROJECT=$absent
  UNREACHABLE=$absent
  fact b-config PASS "the installation's own config is pointed at a project that is not there — $rewrote in $CONFIG — and gcloud itself confirms $absent does not exist. The tier makes its own reads in its project (the budget's database and the account its job would run as) and none of them can be made here. The roll-back of (a) is put back FIRST, before these writes (config_undo_writes): the image gate is the first thing the pre-flight asks, and leaving it broken would make the row below read (a)'s sentence a second time instead of anything about this project. The real project ($old) is untouched and the exit trap puts this file back"
}

step_unavailable_tier() {
  local status=0
  fg_bounded troubleshoot "$SUBJECT" --json >"$SCRATCH/unavailable.json" 2>"$SCRATCH/unavailable.err" || status=$?
  if [ "$status" = 124 ]; then
    unmeasured b-tier "fugaro troubleshoot $SUBJECT was still running when the ${CLOUD_SECONDS}s bound (--cloud-seconds) ended it, with the tier's own project pointed at $UNREACHABLE: a read the bound had to end is one whose job was still going — the run it launched may be there still, and its record is what to read before running this check again"
    stop "the tier's own read did not come back inside the bound, so (b) was not measured"
  fi
  json_fields "$SCRATCH/unavailable.json" >"$SCRATCH/unavailable.txt"
  local error tier offline reason
  error=$(field "$SCRATCH/unavailable.txt" error)
  if [ -n "$error" ]; then
    unmeasured b-tier "fugaro troubleshoot $SUBJECT --json answered no document this check can read ($error) with the tier pointed at $UNREACHABLE: $(last_lines "$SCRATCH/unavailable.err"). If that is a hard error rather than a fall back to the evidence, it is its own finding and not one this arrangement measures"
    stop "the tier's document could not be read with the tier made unavailable"
  fi
  tier=$(field "$SCRATCH/unavailable.txt" tier)
  offline=$(field "$SCRATCH/unavailable.txt" offline)
  reason=$(field "$SCRATCH/unavailable.txt" reason)
  case "$tier" in
  cloud)
    fact b-tier FAIL "the cloud tier ANSWERED with its project pointed at $UNREACHABLE, which does not exist: offline='$offline', reason='$(one_line "$reason")'. The tier reads the budget's database and the account its job runs as out of the project it would launch in, and an answer from a project that is not there is either a read that did not happen or a launch somewhere else"
    stop "the tier answered with its project unreachable"
    ;;
  local | rules)
    fact b-tier FAIL "the tier that answered with the cloud tier unavailable is '$tier': the subject log names no cause the rules tier takes and this machine has no local login for the local tier, so neither of them can answer over this failure — a command that answered from one of them answered from somewhere this check did not arrange"
    stop "a tier above the cloud one answered while the cloud tier was unavailable and no local login exists"
    ;;
  evidence) ;;
  *)
    unmeasured b-tier "the document names tier '${tier:-nothing}' with reason '$(one_line "$reason")': this check reads D2400's four tiers by name and cannot hold an answer it cannot name"
    stop "the tier in the document is not one of D2400's four"
    ;;
  esac
  if [ "$offline" != true ]; then
    unmeasured b-tier "the tier is 'evidence' and offline is '${offline:-empty}': the evidence path is the one that answers when no diagnosis ran (D1608)"
    stop "the unavailable tier's document is not the shape this check reads"
  fi
  if [ "$status" = 0 ]; then
    fact b-tier FAIL "the run whose project does not exist exited 0: D1608 exits non-zero because the evidence explains nothing by itself"
    stop "the unavailable tier exited 0"
  fi
  if [ -z "$reason" ]; then
    fact b-tier FAIL "the tier fell back to the evidence with an EMPTY reason: a person is told the cloud diagnosis is not available and nothing about why, and that is the row that must never read as a pass"
    stop "the unavailable tier named no reason"
  fi
  if [ "$reason" = "$A_REASON" ]; then
    unmeasured b-tier "the reason with the tier pointed at $UNREACHABLE is the SAME sentence part (a) read over the rolled-back base image ('$(one_line "$reason")'): the tier is still refusing over the image, so pointing it at a project that is not there changed nothing this check can read. Nothing about (b) was measured"
    stop "the unavailable tier's reason is the same sentence part (a) read"
  fi
  case "$reason" in
  *"needs a job image whose runner knows"*)
    unmeasured b-tier "the reason with the tier pointed at $UNREACHABLE is the IMAGE GATE's own sentence ('$(one_line "$reason")'), which is part (a)'s reading and not this one: the config file's roll-back of (a) is still in force, as it should be, and the tier met the image gate before it could meet the project that is not there"
    stop "the image gate still answers before the tier's own project read, so (b) measured nothing new"
    ;;
  esac
  case "$reason" in
  "the cloud diagnosis is not available: "*". The evidence follows."*)
    case "$reason" in
    *"credentials service"* | *"credentials_service"*) LEVER=b ;;
    *budget* | *database* | *"$UNREACHABLE"*) LEVER=a ;;
    *) LEVER="" ;;
    esac
    ;;
  *)
    unmeasured b-tier "the tier fell back to the evidence with a reason that is not the pre-flight's own decline ('$(one_line "$reason")'): the tier declined, and not at the launch path's gates, so what this row measured is unclear. Read the reason above, settle it, and run the check again"
    stop "the unavailable tier's reason is not the pre-flight's decline"
    ;;
  esac
  case "$LEVER" in
  a)
    fact b-tier PASS "with the tier's own project pointed at $UNREACHABLE, a project gcloud itself confirms does not exist, and its budget's database moved with it — so the reads the tier makes in its own project cannot be made — fugaro troubleshoot $SUBJECT answers from tier 4: tier 'evidence', offline true, exit $status, reason '$(one_line "$reason")'. The reason is the PRE-FLIGHT's own sentence (troubleshootDeclineCloud) and names the budget/database read the project change broke; the pre-flight runs before the bundle is uploaded and before any execution exists, so nothing was created, and what a person is left with is the evidence where it already is (D2400's fourth tier). It is not part (a)'s sentence either, which the row above had to see first: the image gate answered (a), this one answers nothing until the image is put back"
    ;;
  b)
    fact b-tier PASS "with the installation's project config pointed at $UNREACHABLE, fugaro troubleshoot $SUBJECT answers from tier 4 with a reason about the CREDENTIALS SERVICE: tier 'evidence', offline true, exit $status, reason '$(one_line "$reason")'. That is T-TC10(b)'s other lever — the service's own switch, checkReviewService, which refuses a review-only job's account when the installation publishes no credentials service — and not the project this check moved: the pre-flight reached it after the image gate passed, which is what the rows above establish. Nothing was created, and what a person is left with is the evidence where it already is"
    ;;
  *)
    unmeasured b-tier "the tier answered tier 4 with a pre-flight sentence this check cannot attribute: '$(one_line "$reason")'. It is not (a)'s image sentence, so the image gate passed and the refusal is about something later in the launch path — but it names neither the projects this check moved nor the credentials service's own switch, so whether (b) measured what it arranged is not something this row can say. Read the reason, settle what it names, and run the check again"
    stop "the unavailable tier's reason is a pre-flight sentence this check cannot attribute to its own arrangement"
    ;;
  esac
}

step_unavailable_executions() { # the whole of (b), read once at the end of it
  if ! executions_since "$SCRATCH/executions.after.b" "$SINCE_A" >"$SCRATCH/after.b.txt"; then
    unmeasured b-executions "gcloud run jobs executions list answered nothing after the tier was pointed at $UNREACHABLE ($(last_lines "$SCRATCH/executions.after.b.err")): this check will not report 'no execution created' from a list it could not read"
    stop "the executions could not be listed after the unavailable tier"
  fi
  local after names rows
  after=$(field "$SCRATCH/after.b.txt" count)
  names=$(field "$SCRATCH/after.b.txt" names)
  rows=$(field "$SCRATCH/after.b.txt" read)
  if [ "$after" != 0 ]; then
    fact b-executions FAIL "the tier pointed at $UNREACHABLE created $after execution(s) — $names — in $PROJECT at or after $SINCE_A_STAMP (of the $rows newest execution(s) gcloud answered, which is what this row counts). (b) makes the tier's own reads unavailable and is answered by the pre-flight, which runs before the bundle is uploaded and before any execution exists, so nothing it runs may reach the backend. A real run of this project's jobs going on during the check looks exactly the same: read the names before taking this for a leak, because this part launches nothing either"
    stop "the unavailable tier created an execution"
  fi
  fact b-executions PASS "the tier with its own project unavailable created no execution: gcloud answers no execution of $PROJECT created at or after $SINCE_A_STAMP (gcloud run jobs executions list --project $REAL_PROJECT --region $REGION --sort-by '~metadata.creationTimestamp' --limit $EXECUTIONS_LIMIT, read newest first and counted in the window), so what (b) broke is the tier's read in its own project and not the backend: no job of $PROJECT was launched by it. No total of the project's executions was read, so how many older ones it holds is not part of this row"
}

step_answer() {
  local status=0
  fg_bounded troubleshoot "$SUBJECT" --json >"$SCRATCH/answer.json" 2>"$SCRATCH/answer.err" || status=$?
  if [ "$status" = 124 ]; then
    unmeasured c-answer "fugaro troubleshoot $SUBJECT was still running when the ${CLOUD_SECONDS}s bound (--cloud-seconds) ended it: the tier's own timeout is fifteen minutes and this bound is that timeout and slack, so a diagnosis the bound had to end is one whose job was still going — the run it launched may be there still, and its record is what to read before running this check again"
    stop "the diagnosis did not come back inside the bound, so (c) was not measured"
  fi
  json_fields "$SCRATCH/answer.json" >"$SCRATCH/answer.txt"
  local error tier offline reason run_ref outcome answer_first
  error=$(field "$SCRATCH/answer.txt" error)
  if [ -n "$error" ]; then
    unmeasured c-answer "fugaro troubleshoot $SUBJECT --json answered no document this check can read ($error) on a sandbox whose config had just been put back: $(last_lines "$SCRATCH/answer.err")"
    stop "the cloud answer's document could not be read"
  fi
  tier=$(field "$SCRATCH/answer.txt" tier)
  offline=$(field "$SCRATCH/answer.txt" offline)
  reason=$(field "$SCRATCH/answer.txt" reason)
  run_ref=$(field "$SCRATCH/answer.txt" cloud_run)
  COST_PRINTED=$(field "$SCRATCH/answer.txt" cloud_cost_usd)
  outcome=$(field "$SCRATCH/answer.txt" outcome)
  answer_first=$(field "$SCRATCH/answer.txt" answer_first)
  case "$tier" in
  cloud) ;;
  local)
    unmeasured c-answer "the LOCAL tier answered a machine this check had left with no local login: something still resolved a claude of its own (none is on the crafted PATH, but the tier found one), and D2400's order gives the local tier the answer whenever a login exists — this check will not take your login away to reach the cloud tier. Nothing about (c) was measured. $(last_lines "$SCRATCH/answer.err")"
    stop "a local credential still answered, so the cloud tier was never reached"
    ;;
  evidence)
    unmeasured c-answer "the healthy sandbox answered from the evidence with reason '$(one_line "$reason")': the cloud tier is the tier a machine with no local login gets when the launch path is healthy, and this installation's is not — the gate the reason names is the one to settle before running the check again. $(last_lines "$SCRATCH/answer.err")"
    stop "the cloud tier declined on a sandbox this check had just put back"
    ;;
  *)
    fact c-answer FAIL "the tier that answered is '$tier' and not the cloud one: this machine has no local login (no claude on PATH, neither credential variable set) and the launch path is healthy, so D2400's order leaves the cloud job as the tier — a '$tier' answer is a tier order that runs something else first (offline='$offline', reason='$(one_line "$reason")')"
    stop "a tier other than the cloud one answered a machine with no local login"
    ;;
  esac
  if [ -z "$answer_first" ]; then
    fact c-answer FAIL "the cloud tier answered (tier 'cloud', run '${run_ref:-none}') and the answer is empty: a run that cost money and says nothing is the one thing this tier may not print"
    stop "the cloud answer carries no text"
  fi
  if [ "$status" != 0 ]; then
    fact c-answer FAIL "the cloud tier answered and exited $status: exit 0 is this command's one word for 'it explained something' (D1608), and a client that read this as a failure would retry a diagnosis it already has"
    stop "the cloud answer exited non-zero"
  fi
  RUN_REF=$run_ref
  fact c-answer PASS "fugaro troubleshoot $SUBJECT with no local login answered from the CLOUD: tier 'cloud', offline '${offline:-empty}', exit 0, outcome '${outcome:-none}', the run '${run_ref:-none}', and the answer's first line — '${answer_first}' — which is a model's reading of this machine's own evidence rather than the local tier's. That the answer exists at all is also T-TC12's marker being read: a run without it is halted at bootstrap (c-marker, below)"
}

step_run_record() {
  case "$RUN_REF" in
  */*) RUN_SLUG=${RUN_REF%%/*}; RUN_ID=${RUN_REF##*/} ;;
  *)
    unmeasured c-run "the answer names run '${RUN_REF:-nothing}', which is not the <repo-slug>/<run-id> reference the other commands take (fugaro diagnose <ref>): the run's own record cannot be read without it"
    stop "the answer names no readable run reference"
    ;;
  esac
  local prefix="gs://$BUCKET/runs/$RUN_SLUG/$RUN_ID/"
  gcloud storage ls -r "${prefix}**" >"$SCRATCH/objects" 2>"$SCRATCH/objects.err" || true
  if [ ! -s "$SCRATCH/objects" ]; then
    unmeasured c-run "gcloud storage ls -r '${prefix}**' listed no object of the diagnosis's own run ($(last_lines "$SCRATCH/objects.err")): the record, the stored task and the answer's artifacts are what the rows below read, and none of them could be listed. Check that gcloud is authenticated to the installation's project and that $BUCKET is its runs bucket, then run the check again"
    stop "the diagnosis run's own objects could not be listed"
  fi
  gcloud storage cat "${prefix}result.json" >"$SCRATCH/result.json" 2>"$SCRATCH/result.err" || true
  gcloud storage cat "${prefix}task.json" >"$SCRATCH/task.json" 2>"$SCRATCH/task.err" || true
  record_fields "$SCRATCH/result.json" >"$SCRATCH/record.txt"
  task_fields "$SCRATCH/task.json" >"$SCRATCH/task.txt"
  object_names "$SCRATCH/objects" "runs/$RUN_SLUG/$RUN_ID/" >"$SCRATCH/objects.txt"
  local rerror outcome cost total marker names count artifacts
  names=$(field "$SCRATCH/objects.txt" names)
  count=$(field "$SCRATCH/objects.txt" count)
  rerror=$(field "$SCRATCH/record.txt" error)
  if [ -n "$rerror" ]; then
    unmeasured c-run "the diagnosis run's own result.json could not be read ($rerror): $(last_lines "$SCRATCH/result.err")"
    stop "the diagnosis run's record could not be read"
  fi
  outcome=$(field "$SCRATCH/record.txt" outcome)
  cost=$(field "$SCRATCH/record.txt" cost_usd)
  total=$(field "$SCRATCH/record.txt" cost_total)
  artifacts=$(field "$SCRATCH/record.txt" artifacts)
  RUN_EXEC=$(field "$SCRATCH/record.txt" execution)
  case "$RUN_EXEC" in
  */jobs/*/executions/*)
    RUN_JOB=${RUN_EXEC#*/jobs/}
    RUN_JOB=${RUN_JOB%%/*}
    RUN_EXEC_NAME=${RUN_EXEC##*/}
    ;;
  esac
  if [ "$outcome" != artifacts ]; then
    fact c-run FAIL "the diagnosis run's record reads outcome '$outcome' and not 'artifacts': a diagnosis is a mode: artifact run whose deliverable is the answer in the bucket — it opens no pull request and is never ready for review — so a record that says otherwise is a record of something else"
    stop "the diagnosis run's record is not an artifact run"
  fi
  if [ -n "$artifacts" ] && [ "$artifacts" != "diagnosis.json,diagnosis.md" ]; then
    fact c-run FAIL "the record's artifact rows name '$artifacts' and not diagnosis.json and diagnosis.md: the recipe's two outputs are what the tier reads back (D2404), and a record naming others is a diagnosis whose deliverable moved"
    stop "the record names artifacts that are not the diagnosis's two"
  fi
  fact c-run PASS "the diagnosis's run is $RUN_SLUG/$RUN_ID, the reference the answer printed, and its own record is on the run's prefix: outcome '$outcome' (an artifact run)${artifacts:+, the artifact rows $artifacts}. It is a run this project really made, over the failure this check read"

  if [ -z "$cost" ]; then
    fact c-cost FAIL "the record holds no cost and the answer printed '${COST_PRINTED:-nothing}': a cloud diagnosis says what it cost (D2407), and a person deciding whether to run another one is owed the figure — the tier reads it off this record"
    stop "the cloud answer carries no cost"
  fi
  if [ "$COST_PRINTED" != "$cost" ]; then
    fact c-cost FAIL "the answer printed cloud_cost_usd '${COST_PRINTED:-nothing}' and the run's record holds '$cost': D2407 has the tier read the figure off the record and not off the job's own account of itself — two figures for one run is the reading that made the rule"
    stop "the printed cost is not the record's own"
  fi
  local total_note=""
  if [ -n "$total" ]; then total_note=", against the record's own billed total of $total USD"; fi
  fact c-cost PASS "the cost the answer printed — $COST_PRINTED USD — is the run record's own figure (cost_usd in runs/$RUN_SLUG/$RUN_ID/result.json, read out of the bucket by this check rather than taken from the answer)$total_note: D2407's rule, and the one a person checking a bill depends on"

  marker=$(field "$SCRATCH/task.txt" review_class)
  if [ "$marker" != true ]; then
    fact c-marker FAIL "the diagnosis run's stored task does not carry review_class: true (it reads '${marker:-nothing}'): that field is T-TC12's marker, which admits a run to the review-only job variant — the container mounts no model credential of the person's and its calls are served as the review class's. A task without it is halted at bootstrap, so an answer at all is the marker having been read; a stored task that does not carry it was admitted by something this release does not have"
    stop "the stored task carries no class marker"
  fi
  fact c-marker PASS "the stored task runs/$RUN_SLUG/$RUN_ID/task.json carries review_class: true: T-TC12's marker admitted this run to the review-only variant, and because the answer exists at all it was admitted rather than halted at bootstrap — a marker that did not would have stopped the run before its first stage. Its model call went the review class's way and no credential of the person's was ever in the container"

  case "$names" in
  *"artifacts/diagnosis.json"*) ;;
  *)
    fact c-artifacts FAIL "the run holds no artifacts/diagnosis.json ($count object(s): $names): the tier reads its answer back out of the run's own artifacts (D2404), and an answer with no artifact behind it is a text somebody printed"
    stop "the answer's artifact is not on the run's prefix"
    ;;
  esac
  case "$names" in
  *"artifacts/diagnosis.md"*) ;;
  *)
    fact c-artifacts FAIL "the run holds artifacts/diagnosis.json and no artifacts/diagnosis.md ($count object(s): $names): the recipe's two outputs are the JSON the tier reads and the Markdown a person reads, and one without the other is a deliverable a reader cannot open"
    stop "the answer's Markdown artifact is not on the run's prefix"
    ;;
  esac
  gcloud storage cat "${prefix}artifacts/diagnosis.json" >"$SCRATCH/diagnosis.json" 2>/dev/null || true
  answer_fields "$SCRATCH/diagnosis.json" >"$SCRATCH/diagnosis.txt"
  local doc_outcome doc_text aerror
  aerror=$(field "$SCRATCH/diagnosis.txt" error)
  doc_outcome=$(field "$SCRATCH/diagnosis.txt" outcome)
  doc_text=$(field "$SCRATCH/diagnosis.txt" text)
  if [ -n "$aerror" ] || [ -z "$doc_text" ]; then
    fact c-artifacts FAIL "the artifact artifacts/diagnosis.json does not read as the answer document the tier reads (${aerror:-it carries no text}): the read-back is what the tier answers with, and a file that is not that document is a run whose answer nobody can reproduce from the bucket"
    stop "the answer artifact is not the answer document"
  fi
  fact c-artifacts PASS "the answer was read back out of the run's own artifacts, which are still there: artifacts/diagnosis.json (outcome '${doc_outcome:-none}', text '${doc_text}') and artifacts/diagnosis.md, on the run's prefix, with $count object(s) in all. D2404: the deliverable is the bucket's, and the tier answers with what it read there rather than with something the job printed on its way out"

  case "$names" in
  *"inputs/diagnose/"*)
    fact c-bundle FAIL "the bundle is still on the run's own prefix ($count object(s): $names): the evidence this machine uploaded — the diagnosis's inputs, under inputs/diagnose/ — is deleted once the answer is in hand (D2405), and what is left is a copy of this machine's own text sitting in the bucket after the diagnosis it was sent for"
    stop "the diagnosis bundle was not deleted"
    ;;
  esac
  fact c-bundle PASS "the bundle is gone: the run's prefix holds $count object(s) and none of them is under inputs/diagnose/, so the evidence this machine uploaded was deleted once the answer was read (D2405) — what is left under that run is the answer and the record of it, nothing of this machine's own text"
}

step_executions_after() {
  if [ -z "$RUN_EXEC_NAME" ] || [ -z "$RUN_JOB" ]; then
    unmeasured c-executions "the diagnosis run's record names no execution this check can read ('${RUN_EXEC:-nothing}', where projects/<p>/locations/<r>/jobs/<job>/executions/<name> is the shape the record writes): D2407's 'one execution and no retry' is read against the job that record names and pinned to the execution it names, so there is nothing here to hold a count to"
    stop "the record names no execution for the diagnosis to be held to"
  fi
  if ! executions_since "$SCRATCH/executions.final" "$SINCE_CHECK" "$RUN_JOB" >"$SCRATCH/after.c.txt"; then
    unmeasured c-executions "gcloud run jobs executions list --job $RUN_JOB answered nothing after the diagnosis ($(last_lines "$SCRATCH/executions.final.err")): D2407's one execution and no retry is read from this list, and this check will not report it from a list it could not read"
    stop "the executions could not be listed after the diagnosis"
  fi
  local after names rows exact
  after=$(field "$SCRATCH/after.c.txt" count)
  names=$(field "$SCRATCH/after.c.txt" names)
  rows=$(field "$SCRATCH/after.c.txt" read)
  exact=$(field "$SCRATCH/after.c.txt" exact)
  case "$after" in
  0)
    unmeasured c-executions "the run's own record names the execution $RUN_EXEC_NAME of job $RUN_JOB, and gcloud answers no execution of that job created at or after $SINCE_CHECK_STAMP (of the $rows newest execution(s) it answered): the record and the list cannot both be right, and this check will not report D2407's 'exactly one' from a read that contradicts the record it is reading"
    stop "the list says the diagnosis created no execution and the record names one"
    ;;
  1)
    if [ "$names" != "$RUN_EXEC_NAME" ]; then
      fact c-executions FAIL "the one execution of job $RUN_JOB created at or after $SINCE_CHECK_STAMP is '$names' and the diagnosis run's own record names '$RUN_EXEC_NAME': the execution this check's window holds is not the one the run says it ran as, so the run whose record, cost and artifacts were read above is not the run this project made"
      stop "the execution in the window is not the one the record names"
    fi
    ;;
  *)
    fact c-executions FAIL "the diagnosis created $after executions of job $RUN_JOB at or after $SINCE_CHECK_STAMP (of the $rows newest execution(s) gcloud answered, exact=$exact: $names). D2407 — one execution and no second launch — so a diagnosis that launched twice paid twice for one answer, and a reader of the record sees one run. A real run of $RUN_JOB going on during this check looks exactly the same: read the names before taking this for a retry, because nothing this check runs may launch a job at all"
    stop "the diagnosis created more than one execution"
    ;;
  esac
  fact c-executions PASS "the diagnosis created exactly one execution: $RUN_EXEC_NAME, the execution the run's own record names (in $RUN_JOB, the job the record's execution belongs to), is the only execution of that job gcloud answers at or after $SINCE_CHECK_STAMP — the moment the window opened, before any part of this check ran. That is D2407's one execution and no retry, read by time over the newest $EXECUTIONS_LIMIT executions of that job rather than against a total of the project's, and since a-executions and b-executions both read no execution of $PROJECT created at or after $SINCE_A_STAMP, it is the only execution any part of this check created"
}

step_restore() {
  local hash
  hash=$(file_hash "$CONFIG")
  if [ "$hash" != "$CONFIG_ORIGINAL_HASH" ]; then
    fact restore FAIL "$CONFIG did not come back: it read $CONFIG_ORIGINAL_HASH before this check doctored it and $hash now. The register's Restore is one command: cp $CONFIG_BACKUP $CONFIG"
    stop "the project config was not put back"
  fi
  if [ -f "$CONFIG_BACKUP" ]; then
    fact restore FAIL "the project config is byte-identical to what it was ($hash) and the copy this check made at $CONFIG_BACKUP is still there: that copy holds the installation's own config, with nothing this check wrote, and it is not part of the installation. The register's Restore removes it: rm $CONFIG_BACKUP"
    stop "the sidecar copy was left behind"
  fi
  fact restore PASS "the installation's project config is byte-identical to what this check found ($hash): the base image roll-back of (a) and the absent project of (b) are both gone, the sidecar $CONFIG_BACKUP is removed, and the CLI reads the installation it read before. Nothing else in the installation was written at any point"
  fact left-behind INFO "what this run leaves in the real project ($REAL_PROJECT): the diagnosis's own run $RUN_REF — its record and its two answer artifacts in $BUCKET, which \`fugaro diagnose $RUN_REF\` explains and the bucket's lifecycle expires. Parts (a) and (b) left nothing behind: no execution, no run, and the file they arranged is put back"
}

plan() {
  say "CHECK 680 plan (dry run: nothing below runs, no file is written and no row of the block was measured)"
  say "  [fugaro, read-only, offline, no credential] config list --json — the installations of this machine, and the one that remembers this checkout (name, GCP project, region)"
  say "  [git, read-only] rev-parse --show-toplevel and remote get-url origin — the checkout and the repository (owner/name) everything is scoped to"
  say "  [local, read-only] \$XDG_CONFIG_HOME/fugaro/projects/<name>.yaml, read by python3 for base_images.base, gcp_project, runs_bucket and the budget block: the ONE file this check writes"
  say "  [local, read-only] the newest failed log under <state>/fugaro/logs (the CLI's own rule: a closing line 'Failed in …' or 'Left for you in …') — the failure the three parts diagnose"
  say "  [gcloud, read-only] run jobs executions list --project <p> --region <r> --sort-by '~metadata.creationTimestamp' --limit <n> — the moment the window every part below is read against opened: nothing was created since it, so (a) and (b) read 'no execution created since' and (c) reads exactly one"
  say "  [local, WOULD WRITE, PUT BACK] base_images.base rolled back to $BAD_RELEASE in the config file (a copy beside it first; the exit trap puts it back whatever happens)"
  say "  [fugaro, no launch, no cost] troubleshoot <id> --json with NO local login (claude off PATH, neither credential variable set) — (a): the cloud tier must DECLINE in D2401's sentence, from the pre-flight, so nothing is uploaded and no execution exists"
  say "  [fugaro, THE ONE REFUSED LAUNCH, no cost, ${CLOUD_SECONDS}s bound here] run --repo <owner/name> '<this check's own task text>' — refused by the same gate, and run at all only once the decline above has shown that gate is the judge: a real run starts if that is wrong, which is why the order is this way"
  say "  [gcloud, read-only] run jobs executions list once more, the newest executions counted in the window (a) opened — no execution was created in it, so what the gate refused never reached the backend"
  say "  [local, PUT BACK] the base image put back from the sidecar (the image gate answers first, so (b) could not reach the tier's own reads over it)"
  say "  [local, WOULD WRITE, PUT BACK] gcp_project (and the budget block's own project and database, where the config records them) pointed at $ABSENT_PROJECT, a project gcloud is asked to confirm does not exist"
  say "  [fugaro, no launch, no cost] troubleshoot <id> --json — (b): tier 4 with the reason, a sentence that is NOT (a)'s, from the pre-flight, which is before the upload and before any execution"
  say "  [local, PUT BACK] the config file restored to the byte the check found, before anything else runs"
  say "  [gcloud, read-only] run jobs executions list once more, the newest executions counted in the window (a) opened — neither the refused launch nor the unavailable tier created one"
  say "  [fugaro, THE ONE JOB — the recipe's own fifteen-minute timeout and USD 0.25 cap, ${CLOUD_SECONDS}s bound here] troubleshoot <id> --json — (c): a cloud answer with its cost, and the run it names is then read out of the bucket: the record, the stored task's review_class marker, the two answer artifacts, the deleted bundle"
  say "  [gcloud, read-only] run jobs executions list a last time, narrowed with --job to the job the run's own record names — (c): exactly one execution created since the window opened, and it is the execution the record names, no retry"
  say "  [local] the config compared byte for byte with what it was, the sidecar removed, the scratch directory removed"
  fact dry-run PASS "the plan was printed and nothing was run: no fugaro command, no gcloud call and no file written — the config this check would doctor was not touched, and no row of the block below was measured"
}

main() {
  need_tools
  scratch_up
  trap on_error ERR
  guard_xtrace
  guard_agent_session
  guard_inputs
  if [ -n "$DRY_RUN" ]; then
    plan
    return 0
  fi
  step_setup
  config_backup
  arrange_base
  step_decline
  step_launch
  step_no_execution
  config_undo_writes
  step_unavailable
  step_unavailable_tier
  config_restore
  PROJECT=$REAL_PROJECT
  step_unavailable_executions
  step_answer
  step_run_record
  step_executions_after
  step_restore
}

# ---------------------------------------------------------------------- inputs
guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x). Run it without -x: bash scripts/live-checks/check680.sh — the check prints its own RESULT block, so a trace is never needed to report a failure, and a traced run prints every value it reads out of the installation"
  fi
  fact xtrace PASS "this shell is not tracing, so no trace of this run can print the installation's own values to the terminal"
}

guard_agent_session() {
  local marker="" name
  for name in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "${!name:-}" ]; then marker=$name; break; fi
  done
  if [ -n "$marker" ]; then
    stop "$marker is set, so this is a coding agent's session: Check 680 rolls an installation's base image back, rewrites its project config and launches a job on the project's account, and no agent runs any step of it"
  fi
  fact agent-session PASS "no coding agent's marker is set (CLAUDECODE and the like): this is your own session, which is the one this check is written for"
}

guard_inputs() {
  local all="${NAME:-}${CONFIG:-}${CHECKOUT:-}${REPO:-}${PROJECT:-}${REGION:-}${BUCKET:-}${SUBJECT:-}${ABSENT_PROJECT:-}${LOGS_DIR:-}"
  case "$all" in
  *belong*) stop "a value this check was given names belong: this check is never run there, and nothing was read" ;;
  esac
  fact belong PASS "nothing this check was given names belong; the installation it picks for itself is held to the same rule once it is picked, before anything is written"
  if [ "$DRY_RUN" != 1 ] && [ "${PROJECT:-}" = fugaro-prod ]; then
    stop "--project fugaro-prod: this check rolls the installation's own base image back and rewrites its project config, and that is not done to production. Run it on the sandbox installation"
  fi
}

DRY_RUN_CLI=""
NAME_CLI=""
CONFIG_CLI=""
CHECKOUT_CLI=""
REPO_CLI=""
PROJECT_CLI=""
REGION_CLI=""
BUCKET_CLI=""
SUBJECT_CLI=""
BAD_RELEASE_CLI=""
ABSENT_CLI=""
TASK_CLI=""
USAGE="check680.sh [--name <installation>] [--config <file>] [--checkout <dir>] [--repo <owner/name>] [--project <gcp project>] [--region <region>] [--bucket <bucket>] [--subject <run id>] [--bad-release <X.Y.Z>] [--absent-project <project id>] [--task <text>] [--executions-limit <n>] [--cloud-seconds <n>] [--dry-run]"
usage() {
  sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&3
}
while [ $# -gt 0 ]; do
  case "$1" in
  --name)
    [ $# -ge 2 ] || { say "--name needs the installation's name. usage: $USAGE"; exit 2; }
    NAME_CLI=$2
    shift 2
    ;;
  --config)
    [ $# -ge 2 ] || { say "--config needs a path. usage: $USAGE"; exit 2; }
    CONFIG_CLI=$2
    shift 2
    ;;
  --checkout)
    [ $# -ge 2 ] || { say "--checkout needs a directory. usage: $USAGE"; exit 2; }
    CHECKOUT_CLI=$2
    shift 2
    ;;
  --repo)
    [ $# -ge 2 ] || { say "--repo needs owner/name. usage: $USAGE"; exit 2; }
    REPO_CLI=$2
    shift 2
    ;;
  --project)
    [ $# -ge 2 ] || { say "--project needs a GCP project id. usage: $USAGE"; exit 2; }
    PROJECT_CLI=$2
    shift 2
    ;;
  --region)
    [ $# -ge 2 ] || { say "--region needs a region. usage: $USAGE"; exit 2; }
    REGION_CLI=$2
    shift 2
    ;;
  --bucket)
    [ $# -ge 2 ] || { say "--bucket needs a bucket name. usage: $USAGE"; exit 2; }
    BUCKET_CLI=$2
    shift 2
    ;;
  --subject)
    [ $# -ge 2 ] || { say "--subject needs a run ID. usage: $USAGE"; exit 2; }
    SUBJECT_CLI=$2
    shift 2
    ;;
  --bad-release)
    [ $# -ge 2 ] || { say "--bad-release needs a release version. usage: $USAGE"; exit 2; }
    BAD_RELEASE_CLI=$2
    shift 2
    ;;
  --absent-project)
    [ $# -ge 2 ] || { say "--absent-project needs a GCP project id. usage: $USAGE"; exit 2; }
    ABSENT_CLI=$2
    shift 2
    ;;
  --task)
    [ $# -ge 2 ] || { say "--task needs the task text. usage: $USAGE"; exit 2; }
    TASK_CLI=$2
    shift 2
    ;;
  --executions-limit)
    [ $# -ge 2 ] || { say "--executions-limit needs a whole number. usage: $USAGE"; exit 2; }
    EXECUTIONS_LIMIT_CLI=$2
    shift 2
    ;;
  --cloud-seconds)
    [ $# -ge 2 ] || { say "--cloud-seconds needs a whole number. usage: $USAGE"; exit 2; }
    CLOUD_SECONDS_CLI=$2
    shift 2
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
    say "unknown argument '$1'. usage: $USAGE"
    exit 2
    ;;
  esac
done

NAME=${CHECK680_NAME:-${NAME_CLI:-}}
CONFIG=${CHECK680_CONFIG:-${CONFIG_CLI:-}}
CHECKOUT=${CHECK680_CHECKOUT:-${CHECKOUT_CLI:-}}
REPO=${CHECK680_REPO:-${REPO_CLI:-}}
PROJECT=${CHECK680_PROJECT:-${PROJECT_CLI:-}}
REGION=${CHECK680_REGION:-${REGION_CLI:-}}
BUCKET=${CHECK680_BUCKET:-${BUCKET_CLI:-}}
SUBJECT=${CHECK680_SUBJECT:-${SUBJECT_CLI:-}}
BAD_RELEASE=${CHECK680_BAD_RELEASE:-${BAD_RELEASE_CLI:-0.1.0}}
ABSENT_PROJECT=${CHECK680_ABSENT_PROJECT:-${ABSENT_CLI:-check680-absent-$$}}
TASK_DEFAULT="Check 680: this launch exists only to be refused by the installation's own job-image gate, and nothing is meant to run from it."
TASK=${CHECK680_TASK:-${TASK_CLI:-$TASK_DEFAULT}}
DRY_RUN=${CHECK680_DRY_RUN:-${DRY_RUN_CLI:-}}
KEEP_SCRATCH=${CHECK680_KEEP_SCRATCH:-}
# How many of the NEWEST executions each read asks for (see executions_since):
# how deep a window reaches into the newest few, never a total the project has to
# fit in. A project holding thousands of executions reads the same as one holding
# none, and a page that comes back full and entirely inside the window is
# reported as a floor (`exact=no`) rather than widened.
EXECUTIONS_LIMIT=${CHECK680_EXECUTIONS_LIMIT:-${EXECUTIONS_LIMIT_CLI:-200}}
CLOUD_SECONDS=${CHECK680_CLOUD_SECONDS:-${CLOUD_SECONDS_CLI:-1500}}
LOGS_DIR=${CHECK680_LOGS_DIR:-}

case "$BAD_RELEASE" in
"" | *[!0-9.]*) say "--bad-release has to be a release version like 0.1.0. usage: $USAGE"; exit 2 ;;
esac
case "$EXECUTIONS_LIMIT" in
"" | 0 | *[!0-9]*) say "--executions-limit has to be a whole number of at least 1. usage: $USAGE"; exit 2 ;;
esac
case "$CLOUD_SECONDS" in
"" | *[!0-9]*) say "--cloud-seconds has to be a whole number. usage: $USAGE"; exit 2 ;;
esac

trap finish EXIT
trap on_signal INT TERM HUP
main "$@"
