#!/usr/bin/env bash
# Check 721 — the drift row's audit-log cross-check, on YOUR OWN workspace and
# one of its members. Run by YOU. It settles U5 of
# docs/workspace-projects-users/reconciliation.md: Cloud Audit Logs' Admin
# Activity entries for SetIamPolicy are readable with roles/logging.viewer on the
# member, which a workspace admin's roles/owner includes.
#
# What it settles, and how it reads it. U5 is one question — can the workspace
# admin who runs `status` read the member's Admin Activity at all — and the
# check answers it the way the row itself does, in the row's own words:
#
#   - the check makes one real IAM change on the member: the write a Cloud
#     Console edit makes (an IAM policy binding added for one principal). This
#     check cannot drive your browser, so it makes the call the console makes and
#     prints Google's own record of it, so you can see both;
#   - it runs `fugaro workspace project status <member>` with your own
#     credentials and reads the `drift` row;
#   - a row that could not read the log carries `the member's live IAM was not
#     compared against the record, so drift was not measured: Cloud Audit Logs of
#     <project>: <Google's own words>` — that IS U5's answer, and the check
#     reports it as measured-and-no rather than guessing;
#   - a row that DID read the log is asserted on the two halves the plan's
#     acceptance names: it names the change this check made, with the audit log
#     entry Google wrote for it (drift's own sentence,
#     `changed in the console: <address> as <role> on <project> (<entry>)`), and
#     it names none of the changes the member's OWN `fugaro update` made in the
#     same window — what an update applies is the record, so nothing it wrote is
#     drift (internal/workspace/drift.go; D2811, D2818, R13).
#
# How the two changes are told apart, and what that is worth. Both are made by
# the same account (yours), so the check reads Google's log itself, with the same
# filter internal/infra's AuditLogs uses, and keeps what it read: the entry for
# this check's own change (by principal and role) is the console change, and the
# entries the member's own update made are the rest, by that same account. The
# second half cannot be shown at all when the window holds no such entry — a
# member whose grants are all applied has nothing to apply — and then the row
# reads UNVERIFIED naming the fix, rather than passing on an empty comparison. A
# change somebody ELSE made while the check ran is named for what it is: the
# cross-check working, not drift's own noise.
#
# What it writes on YOUR installation. No config of yours is created or changed:
# the check only reads your workspace's own config and your member's. It runs two
# things: `fugaro update` for the member (the member's own installation on this
# machine, when there is one and unless --no-update is given — that is what
# applies the record's grants to the member's IAM, which is the change the drift
# row must NOT name), and one IAM binding on the member, which it removes again
# (the `ungrant` row; --keep does not). Nothing else is touched, and the update
# it runs is the one documented command, idempotent by design.
#
# What it needs from you: your own credentials, and both of them. `gcloud auth
# login` is what the IAM write uses; `gcloud auth application-default login` is
# what the CLI's own reads use (the drift comparison reads the member's live IAM
# and its audit log through Application Default Credentials — a second login). A
# coding agent's session is refused outright: no agent runs any step of this.
#
# Usage:      bash scripts/live-checks/check-721.sh --workspace <name> --project <member>
#               --workspace the workspace's OWN project name on this machine: the
#                           config that holds the member table (the project the
#                           workspace is named after)
#               --project   the member to look at, as the member table names it
#                           (its project name, which is its GCP project id in an
#                           ordinary installation)
#               --member-project  the member's GCP project id, when it is not the
#                           domain of its job service accounts (default: read
#                           from a job account, as the member table does)
#               --grantee   the address the hand-made console change is for
#                           (default: a fresh chk721-<stamp>@example.com — nobody
#                           holds it, so the record cannot state the binding and
#                           the cross-check cannot skip it; pass an address of
#                           your own to grant a real person, and the check
#                           removes it again)
#               --role      the role that change adds (default roles/viewer)
#               --since     how many minutes of Admin Activity to read (default
#                           90, and never a window starting after the member's own
#                           update this check ran)
#               --timeout   how long the member's own update may take, in minutes
#                           (default 45)
#               --wait      how long to wait for the change to appear in Admin
#                           Activity, in seconds (default 180)
#               --no-update do not run the member's own update
#               --dir       the directory this check works in, kept as it is and
#                           never removed; by default a scratch directory under
#                           TMPDIR that the exit trap removes
#               --keep      leave the IAM binding this check added (and say so)
#               --dry-run   print the plan and call nothing
#               --help
# Needs:      bash, mktemp, python3, gcloud, fugaro, and your own installations:
#             the workspace's config, and the member's for the update.
# Runs:       by YOU. It refuses to start in a coding agent's session and in a
#             traced shell. Every child call runs with stdin closed and every
#             gcloud call with --quiet. Its test (check-721_test.sh) drives it
#             against fakes on PATH and touches no cloud and no installation.
# Output:     progress on stderr, ONE block on stdout headed CHECK 721 RESULT.
#             Its last line is the verdict, and the verdict is the exit status:
#               PASS (exit 0)        the drift row read the log, named the change
#                                    this check made, and named nothing the
#                                    member's own update applied
#               FAIL (exit 1)        the row named the update's own change, did
#                                    not name the one Google logged, or one of
#                                    this check's own cloud calls failed
#               INCOMPLETE (exit 2)  the run stopped before it could measure
#               INCOMPLETE (exit 3)  it measured, and a row could not be read
#                                    (UNVERIFIED): the log was not readable (U5's
#                                    answer is no), or the window held nothing
#                                    to compare
# Expected duration: a few minutes when the member's own update is skipped, up to
# an hour with it (Terraform's own applies).
set -euo pipefail

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }

XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null

FACTS=""
SCRATCH=""
NEXT=""
SCRATCH_REMOVED=no
CALLER_DIR=""
REACHED_END=no
ABORT=""
KEEP=no
DRY_RUN=no
RUN_UPDATE=yes
GRANTED=no

stop() {
  printf '%s: stopped: %s\n' "$PROG" "$1" >&2
  ABORT=$1
  exit 2
}

fact() { # fact KEY STATE "detail"
  [ -n "$FACTS" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS"
}

usage() {
  cat <<'USAGE'
Check 721 — the drift row's audit-log cross-check, on your own workspace and one
of its members. It makes one real IAM change on the member (the write a Cloud
Console edit makes), runs `fugaro workspace project status <member>` with your
own credentials, and reads its `drift` row: the row must name that change with
the Admin Activity entry Google logged for it, and must not name what the
member's own `fugaro update` applied. It settles U5 of the workspace-projects-users
reconciliation — whether an admin's roles/owner really reads the member's Admin
Activity. Run by YOU; no agent runs any step of it.

Usage:
  bash scripts/live-checks/check-721.sh --workspace <name> --project <member> [flags]
  bash scripts/live-checks/check-721.sh --help

  --workspace <name>    the workspace's own project name on this machine
                        (required: the config that holds the member table)
  --project <member>    the member to look at, as the member table names it
                        (required)
  --member-project <id> the member's GCP project id (default: the domain of its
                        job service accounts)
  --grantee <address>   who the console change is for (default: a fresh
                        chk721-<stamp>@example.com nobody holds)
  --role <role>         the role that change adds (default roles/viewer)
  --since <minutes>     how much Admin Activity to read (default 90)
  --timeout <minutes>   how long the member's own update may take (default 45)
  --wait <seconds>      how long to wait for the change to be logged (default 180)
  --no-update           do not run the member's own update
  --dir <path>          work in this directory and keep it (default: a scratch
                        directory under TMPDIR, removed by the exit trap)
  --keep                leave the IAM binding this check added
  --dry-run             print the plan and call nothing

It removes the IAM binding it added again unless --keep is given and changes
nothing else of yours: no config of yours is written, and the only command it
runs that writes is the member's own `fugaro update` (skippable with --no-update).

  PASS (exit 0)  FAIL (exit 1)  INCOMPLETE (exit 2 stopped, exit 3 unverified)
USAGE
}

guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs Check 721: it writes an IAM binding on one of your projects and runs your own update. Run it yourself, at your own terminal: bash scripts/live-checks/check-721.sh --workspace <name> --project <member>"
}

guard_xtrace() {
  [ "$XTRACE_AT_START" = no ] ||
    stop "this shell is tracing (bash -x), and its trace would print your accounts, your workspace and every project and log entry id this check reads. Run it without -x"
}

need() {
  command -v "$1" >/dev/null 2>&1 || stop "$1 is not on PATH, and this check is built on it"
}

WORKSPACE=""
MEMBER=""
MEMBER_PROJECT=""
GRANTEE=""
ROLE=roles/viewer
SINCE=90
TIMEOUT_MIN=45
WAIT=180

while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  --workspace)
    [ $# -ge 2 ] || stop "--workspace needs a name"
    WORKSPACE=$2
    shift
    ;;
  --workspace=*)
    WORKSPACE=${1#--workspace=}
    ;;
  --project)
    [ $# -ge 2 ] || stop "--project needs a member name"
    MEMBER=$2
    shift
    ;;
  --project=*)
    MEMBER=${1#--project=}
    ;;
  --member-project)
    [ $# -ge 2 ] || stop "--member-project needs a project id"
    MEMBER_PROJECT=$2
    shift
    ;;
  --member-project=*)
    MEMBER_PROJECT=${1#--member-project=}
    ;;
  --grantee)
    [ $# -ge 2 ] || stop "--grantee needs an address"
    GRANTEE=$2
    shift
    ;;
  --grantee=*)
    GRANTEE=${1#--grantee=}
    ;;
  --role)
    [ $# -ge 2 ] || stop "--role needs a role name"
    ROLE=$2
    shift
    ;;
  --role=*)
    ROLE=${1#--role=}
    ;;
  --since)
    [ $# -ge 2 ] || stop "--since needs a number of minutes"
    SINCE=$2
    shift
    ;;
  --since=*)
    SINCE=${1#--since=}
    ;;
  --timeout)
    [ $# -ge 2 ] || stop "--timeout needs a number of minutes"
    TIMEOUT_MIN=$2
    shift
    ;;
  --timeout=*)
    TIMEOUT_MIN=${1#--timeout=}
    ;;
  --wait)
    [ $# -ge 2 ] || stop "--wait needs a number of seconds"
    WAIT=$2
    shift
    ;;
  --wait=*)
    WAIT=${1#--wait=}
    ;;
  --no-update)
    RUN_UPDATE=no
    ;;
  --dir)
    [ $# -ge 2 ] || stop "--dir needs a path"
    CALLER_DIR=$2
    shift
    ;;
  --dir=*)
    CALLER_DIR=${1#--dir=}
    ;;
  --keep)
    KEEP=yes
    ;;
  --dry-run)
    DRY_RUN=yes
    ;;
  *)
    stop "unknown argument $1 (usage: check-721.sh --workspace <name> --project <member>, or --help)"
    ;;
  esac
  shift
done

guard_xtrace
guard_agent

STAMP=$(date -u +%s)
# A fresh address per run, so the binding is new every time and Google logs the
# change: a binding the member already holds is an idempotent no-op, writes no
# Admin Activity entry, and would leave this check with nothing to look for.
[ -n "$GRANTEE" ] || GRANTEE="chk721-${STAMP}@example.com"

[ -n "$WORKSPACE" ] || stop "no --workspace was given: pass the name of the workspace's own project on this machine, which is the config that holds the member table"
[ -n "$MEMBER" ] || stop "no --project was given: pass the member to look at, as the member table names it"
for n in "$SINCE" "$TIMEOUT_MIN" "$WAIT"; do
  printf '%s' "$n" | grep -Eq '^[0-9]+$' || stop "$n is not a number (--since and --timeout are minutes, --wait is seconds)"
done
[ "$SINCE" -gt 0 ] || stop "--since 0 is not a window: pass at least one minute of Admin Activity"
[ "$WAIT" -gt 0 ] || stop "--wait 0 would not wait for the change to be logged: pass at least one second"
printf '%s' "$ROLE" | grep -Eq '^roles/[A-Za-z0-9_.]+$' ||
  stop "--role $ROLE is not an IAM role name (it looks like roles/viewer)"
case "$GRANTEE" in
*@*.*) ;;
*) stop "--grantee $GRANTEE is not an address (it looks like person@example.com)" ;;
esac

need mktemp
need python3
need gcloud
need fugaro

if [ -n "$CALLER_DIR" ]; then
  [ -d "$CALLER_DIR" ] || stop "--dir $CALLER_DIR is not a directory"
  SCRATCH=$CALLER_DIR
else
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk721.XXXXXXXX") || stop "cannot make a scratch directory"
  chmod 700 "$SCRATCH"
fi
FACTS=$SCRATCH/facts
: >"$FACTS"

cleanup() {
  [ -n "$SCRATCH" ] || return 0
  if [ -n "$CALLER_DIR" ]; then
    SCRATCH_REMOVED=lefte
    return 0
  fi
  rm -rf "$SCRATCH" 2>/dev/null || true
  [ -e "$SCRATCH" ] || SCRATCH_REMOVED=yes
}

plan() {
  local plan_project=${MEMBER_PROJECT:-"the domain of $MEMBER's job service accounts"}
  cat >&2 <<PLAN
Check 721 plan (--dry-run: nothing below is called)
  workspace          $WORKSPACE  (your own installation: the config with the member table)
  member             $MEMBER
  member project     $plan_project
  console change     add $ROLE for user:$GRANTEE on the member's project — the write
                     a console edit makes, removed again at the end
  member's update    run=$RUN_UPDATE (the member's own \`fugaro update\`; --no-update skips it)
  audit log window   the last $SINCE minutes, and never starting after that update
  the row read       fugaro workspace project status $MEMBER --project $WORKSPACE --json
  Google's record    the SetIamPolicy entries of the member's project, with the same
                     filter internal/infra's AuditLogs reads them with
  restore            the binding is removed again (--keep leaves it); no config of
                     yours is written
  working directory  $SCRATCH
PLAN
}

# g_at is one gcloud call: stdin closed, --quiet always, stdout and stderr both
# in $1, so Google's own words are what a row reports. Its exit status is the
# call's.
g_at() { # g_at <log> <args...>
  local log=$1
  shift
  local status=0
  gcloud --quiet "$@" </dev/null >"$log" 2>&1 || status=$?
  return "$status"
}

# g_out is g_at for a call whose ANSWER the check itself reads: stdout lands in
# $1, stderr in $2, stdin stays closed, and the exit status is the call's.
g_out() { # g_out <out> <err-log> <args...>
  local out=$1 errlog=$2
  shift 2
  local status=0
  gcloud --quiet "$@" </dev/null >"$out" 2>"$errlog" || status=$?
  return "$status"
}

# run_bounded runs CMD with stdin closed and kills it at the deadline, which is
# reported as 124. A command of Fugaro's that asked a question would have nobody
# to ask it (nothing here ever types an answer), so the deadline is what keeps a
# step that waits on one from holding the check forever.
run_bounded() { # run_bounded <seconds> <log> <cmd...>
  python3 - "$1" "$2" "${@:3}" <<'PY'
import os, signal, subprocess, sys

secs, log, argv = float(sys.argv[1]), sys.argv[2], sys.argv[3:]
with open(log, "wb") as fh:
    child = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=fh,
                             stderr=subprocess.STDOUT, start_new_session=True)
    try:
        code = child.wait(timeout=secs)
    except subprocess.TimeoutExpired:
        try:
            os.killpg(os.getpgid(child.pid), signal.SIGKILL)
        except OSError:
            pass
        child.wait()
        code = 124
sys.exit(code)
PY
}

# cli runs `fugaro` with one of YOUR installations selected (FUGARO_PROJECT) and
# prints its exit status; stdout and stderr are left in $2. Every call has stdin
# closed: no fugaro command here asks anything, and one that did would be a bug
# this check should see rather than a prompt it should answer.
cli() { # cli <fugaro-project> <log> <args...>
  local project=$1 log=$2
  shift 2
  local status=0
  if [ -n "$project" ]; then
    env FUGARO_PROJECT="$project" fugaro "$@" </dev/null >"$log" 2>&1 || status=$?
  else
    fugaro "$@" </dev/null >"$log" 2>&1 || status=$?
  fi
  printf '%s' "$status"
}

log_tail() { # log_tail <file> [lines]: the last lines, on one line
  tail -n "${2:-3}" "$1" 2>/dev/null | tr '\n' ' ' | tr -s ' ' | cut -c1-600
}

# member_facts reads `workspace show --json` for the member this check was given:
# whether this machine holds the workspace's own config, whether the table lists
# the member, the member's job service accounts and Firebase project, and whether
# a config on this machine names the workspace as that member's.
member_facts() { # member_facts <json-file>
  python3 - "$1" "$WORKSPACE" "$MEMBER" <<'PY' 2>/dev/null || true
import json, sys

path, name, member = sys.argv[1], sys.argv[2], sys.argv[3]
try:
    doc = json.load(open(path, encoding="utf-8"))
except Exception as why:
    print("error\t%s" % why)
    raise SystemExit(0)
row = None
for w in doc.get("workspaces") or []:
    if w.get("name") == name:
        row = w
if row is None:
    print("missing\t1")
    raise SystemExit(0)
print("owner\t%s" % ("yes" if row.get("owner") else "no"))
found = False
for m in row.get("members") or []:
    if m.get("project") != member:
        continue
    found = True
    for ja in m.get("job_service_accounts") or []:
        account = ja.get("account") if isinstance(ja, dict) else ja
        if account:
            print("job\t%s" % account)
    print("firebase\t%s" % (m.get("firebase_project") or ""))
print("member\t%s" % ("yes" if found else "no"))
projects = [x.get("project") for x in row.get("memberships") or []]
print("membership\t%s" % ("yes" if member in projects else "no"))
PY
}

facts_of() { # facts_of <file> <key>: the values of a key, one per line
  awk -F'\t' -v k="$2" '$1 == k { print $2 }' "$1" 2>/dev/null || true
}

# status_rows turns the status document into one line per row, tab-separated:
# scope, id, status, detail. The document is written on stdout even when a row
# fails (the command's exit status is nonzero then), so the caller reads the file.
status_rows() { # status_rows <json-file>
  python3 - "$1" <<'PY' 2>/dev/null || true
import json, sys

try:
    doc = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception as why:
    print("error\t\t\t%s" % why)
    raise SystemExit(0)

def one(scope, rows):
    for r in rows or []:
        detail = (r.get("detail") or "").replace("\t", " ").replace("\n", " ")
        print("%s\t%s\t%s\t%s" % (scope, r.get("id", ""), r.get("status", ""), detail))

for m in doc.get("members") or []:
    one("member:" + str(m.get("project", "")), m.get("rows"))
one("workspace", doc.get("rows"))
PY
}

row_line() { # row_line <rows-file> <scope> <id>: the row's line, or empty
  awk -F'\t' -v s="$2" -v i="$3" '$1 == s && $2 == i { print; exit }' "$1" 2>/dev/null || true
}

row_detail() { # row_detail <rows-file> <scope> <id>
  row_line "$1" "$2" "$3" | cut -f4
}

# rows_error is the refusal status_rows records for a document it could not read:
# its line is an error scope with the reader's own words in the detail field, the
# same place a row's detail sits.
rows_error() { # rows_error <rows-file>
  awk -F'\t' '$1 == "error" { print $4; exit }' "$1" 2>/dev/null || true
}

# log_deltas turns Google's own Admin Activity entries into one line per binding a
# SetIamPolicy call ADDED: entry, timestamp, actor, role, member. An entry the
# cross-check has no rule for is skipped exactly as internal/infra skips one
# (policyChanges), and a document that cannot be parsed is one error line.
log_deltas() { # log_deltas <json-file>
  python3 - "$1" <<'PY' 2>/dev/null || true
import json, sys

try:
    entries = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception as why:
    print("error\t\t\t\t%s" % why)
    raise SystemExit(0)
if isinstance(entries, dict):
    entries = [entries]
for e in entries or []:
    p = e.get("protoPayload") or {}
    if p.get("methodName") != "SetIamPolicy":
        continue
    actor = (p.get("authenticationInfo") or {}).get("principalEmail") or ""
    delta = ((p.get("serviceData") or {}).get("policyDelta") or {}).get("bindingDeltas") or []
    for d in delta:
        if d.get("action") != "ADD":
            continue
        print("%s\t%s\t%s\t%s\t%s" % (e.get("insertId") or "", e.get("timestamp") or "",
                                      actor, d.get("role") or "", d.get("member") or ""))
PY
}

read_log() { # read_log <json-file> <window-start>: Google's own entries, or a refusal
  local filter
  filter="logName=\"projects/$MEMBER_PROJECT/logs/cloudaudit.googleapis.com%2Factivity\" AND protoPayload.methodName=\"SetIamPolicy\" AND timestamp>=\"$2\""
  # --order=desc asks for the NEWEST entries of the window. Reading the window
  # oldest-first truncated the newest 200 in a busy one — this check's own change
  # among them, so the entry row failed on a log that had logged it — and the two
  # things asserted here are both at the newest end: the change this check just
  # made, and the entries the member's own update wrote immediately before it.
  g_out "$1" "$SCRATCH/log.err" logging read "$filter" --project="$MEMBER_PROJECT" --format=json --limit=200 --order=desc
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
  printf '\nCHECK 721 RESULT\n'
  if [ -n "$NEXT" ]; then printf '%s\n' "$NEXT"; fi
  printf 'ROWS\n'
  printf '%s\n' "$1" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-14s %-10s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 721 RESULT: %s (exit %s)\n' "$(verdict "$1")" "$2"
}

# ungrant removes the binding this check added and prints its row. It runs from
# finish, so EVERY way out of the check after the write — a failure step, a
# document that could not be read, an interrupted run — still takes the binding
# away; only --keep leaves it, and only on purpose.
ungrant() {
  [ "$GRANTED" = yes ] || return 0
  if [ "$KEEP" = yes ]; then
    printf 'ungrant|UNVERIFIED|the binding this check added is STILL THERE (%s for user:%s on %s), because --keep was given: remove it with `gcloud projects remove-iam-policy-binding %s --member=user:%s --role=%s`\n' \
      "$ROLE" "$GRANTEE" "$MEMBER_PROJECT" "$MEMBER_PROJECT" "$GRANTEE" "$ROLE"
    return 0
  fi
  local status=0
  g_at "$SCRATCH/ungrant.log" projects remove-iam-policy-binding "$MEMBER_PROJECT" \
    --member="user:$GRANTEE" --role="$ROLE" --condition=None --format=none || status=$?
  if [ "$status" = 0 ]; then
    printf 'ungrant|PASS|the binding this check added is removed again: %s for user:%s on %s\n' \
      "$ROLE" "$GRANTEE" "$MEMBER_PROJECT"
  else
    printf 'ungrant|FAIL|the binding this check added is STILL THERE (%s for user:%s on %s): removing it failed (exit %s): %s — remove it yourself with `gcloud projects remove-iam-policy-binding %s --member=user:%s --role=%s`\n' \
      "$ROLE" "$GRANTEE" "$MEMBER_PROJECT" "$status" "$(log_tail "$SCRATCH/ungrant.log" 3)" "$MEMBER_PROJECT" "$GRANTEE" "$ROLE"
  fi
}

finish() {
  local status=$?
  trap - EXIT
  local facts
  facts=$(cat "$FACTS" 2>/dev/null || true)
  local ungranted
  ungranted=$(ungrant)
  cleanup
  if [ -n "$ungranted" ]; then
    facts="${facts}"$'\n'"$ungranted"
  fi
  if [ -n "$ABORT" ]; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT"
  elif [ "$REACHED_END" = no ]; then
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): what was not reached was NOT measured"
  fi
  case "$SCRATCH_REMOVED" in
  yes) facts="${facts}"$'\n'"restore|PASS|this check's own working directory is gone (your own installations were read and never moved)" ;;
  lefte) facts="${facts}"$'\n'"restore|PASS|the directory you named with --dir was left as it is, at ${SCRATCH}" ;;
  *) facts="${facts}"$'\n'"restore|FAIL|the working directory is STILL THERE, at ${SCRATCH}" ;;
  esac
  local v
  v=$(verdict "$facts")
  if [ -n "$ABORT" ] || [ "$REACHED_END" = no ]; then
    status=2
  elif [ "$v" = FAIL ]; then
    status=1
  elif [ "$v" = INCOMPLETE ]; then
    status=3
  else
    status=0
  fi
  print_block "$facts" "$status"
  exit "$status"
}
trap finish EXIT

# ------------------------------------------------------------------ the check

if [ "$DRY_RUN" = yes ]; then
  plan
  trap - EXIT
  cleanup
  exit 0
fi

say "workspace $WORKSPACE, member $MEMBER ($PROG --help for the flags)"

# ---- premise: the tools, your two credentials, the workspace and the member

AUTH_STATUS=0
g_at "$SCRATCH/auth.log" auth list --filter=status:ACTIVE --format='value(account)' || AUTH_STATUS=$?
ACCOUNT=""
[ "$AUTH_STATUS" = 0 ] && ACCOUNT=$(head -n 1 "$SCRATCH/auth.log" | tr -d '[:space:]')
if [ -z "$ACCOUNT" ]; then
  stop "gcloud is not authenticated: this check writes one IAM binding on the member with your own credentials. Run \`gcloud auth login\` first — and \`gcloud auth application-default login\`, the second login the CLI's own reads need$(if [ -n "$(log_tail "$SCRATCH/auth.log" 1)" ]; then printf '; gcloud said: %s' "$(log_tail "$SCRATCH/auth.log" 1)"; fi)"
fi

if ! g_at "$SCRATCH/adc.log" auth application-default print-access-token >/dev/null 2>&1; then
  stop "your Application Default Credentials do not answer ($(log_tail "$SCRATCH/adc.log" 2)), and they are what \`fugaro workspace project status\` reads the member's live IAM and its audit log with: run \`gcloud auth application-default login\`, and if a read then refuses on a quota project, \`gcloud auth application-default set-quota-project <a project you may use>\`"
fi

SHOW_STATUS=$(cli "" "$SCRATCH/show.log" workspace show "$WORKSPACE" --json)
if [ "$SHOW_STATUS" != 0 ]; then
  stop "\`fugaro workspace show $WORKSPACE --json\` refused (exit $SHOW_STATUS): $(log_tail "$SCRATCH/show.log")"
fi
member_facts "$SCRATCH/show.log" >"$SCRATCH/member.tsv"
if [ "$(facts_of "$SCRATCH/member.tsv" error | head -n 1)" != "" ]; then
  stop "\`fugaro workspace show $WORKSPACE --json\` did not print the document: $(log_tail "$SCRATCH/show.log")"
fi
if [ "$(facts_of "$SCRATCH/member.tsv" missing | head -n 1)" != "" ]; then
  stop "no workspace $WORKSPACE is known to this machine: its name is the project the workspace is named after, and \`fugaro workspace ls\` lists the workspaces this machine knows"
fi
if [ "$(facts_of "$SCRATCH/member.tsv" owner | head -n 1)" != yes ]; then
  stop "this machine holds no config for the workspace $WORKSPACE's own project, so it holds no member table and nothing here could be read: run this check on the workspace owner's machine, where \`fugaro workspace project add\` wrote the table"
fi
if [ "$(facts_of "$SCRATCH/member.tsv" member | head -n 1)" != yes ]; then
  stop "the workspace $WORKSPACE's member table does not list $MEMBER, so \`status $MEMBER\` would refuse it: run this with a member the table declares, or add it with \`fugaro workspace project add $MEMBER --workspace $WORKSPACE\`"
fi

JOBS=$(facts_of "$SCRATCH/member.tsv" job | tr '\n' ' ' | tr -s ' ')
FIREBASE=$(facts_of "$SCRATCH/member.tsv" firebase | head -n 1)
MEMBERSHIP=$(facts_of "$SCRATCH/member.tsv" membership | head -n 1)
if [ -z "$MEMBER_PROJECT" ]; then
  # A job service account's own domain IS the project it lives in, which is how
  # the member table derives the member's GCP project too (wsMemberTarget).
  MEMBER_PROJECT=$(facts_of "$SCRATCH/member.tsv" job | head -n 1 | sed -n 's/.*@\([^.]*\)\.iam\.gserviceaccount\.com$/\1/p')
  if [ -z "$MEMBER_PROJECT" ]; then
    stop "the member $MEMBER names no job service account this check can read its GCP project from, and none was given: pass --member-project <the member's GCP project id>"
  fi
fi

fact premise PASS "gcloud is authenticated as $ACCOUNT, your Application Default Credentials answer, and this machine's config for $WORKSPACE declares $MEMBER (job accounts: ${JOBS:-none}; firebase ${FIREBASE:-none}); a config on this machine names it as the member: $MEMBERSHIP"

if ! g_at "$SCRATCH/describe.log" projects describe "$MEMBER_PROJECT" --format='value(projectId)' >/dev/null 2>&1; then
  fact member FAIL "Google did not describe the member's project $MEMBER_PROJECT with your credentials: $(log_tail "$SCRATCH/describe.log" 2) — pass --member-project <id> if the check read the wrong project out of the job accounts"
  REACHED_END=yes
  exit 1
fi
fact member PASS "the member's GCP project is $MEMBER_PROJECT: where the console change lands and whose Admin Activity this check (and the drift row) reads"

NEXT="WHAT TO OPEN, BY YOU (U5)
  the member's IAM page     https://console.cloud.google.com/iam-admin/iam?project=$MEMBER_PROJECT
  the member's Activity     https://console.cloud.google.com/home/activity?project=$MEMBER_PROJECT
  (the check's own change, $ROLE for user:$GRANTEE, is logged there like any console edit)
"

# ---- status before: what the member is waiting for, read the same way twice

BEFORE_STATUS=$(cli "$WORKSPACE" "$SCRATCH/status-before.json" workspace project status "$MEMBER" --json)
status_rows "$SCRATCH/status-before.json" >"$SCRATCH/rows-before.tsv"
if [ -n "$(rows_error "$SCRATCH/rows-before.tsv")" ]; then
  fact status FAIL "\`fugaro workspace project status $MEMBER --json\` did not print the document (exit $BEFORE_STATUS): $(log_tail "$SCRATCH/status-before.json")"
  REACHED_END=yes
  exit 1
fi
PENDING=$(row_detail "$SCRATCH/rows-before.tsv" "member:$MEMBER" pending-apply)
DRIFT_BEFORE=$(row_detail "$SCRATCH/rows-before.tsv" "member:$MEMBER" drift)
if [ -z "$DRIFT_BEFORE" ]; then
  fact status FAIL "the document of \`status $MEMBER\` carries no drift row, so nothing here would compare the member's live IAM against the record (exit $BEFORE_STATUS): $(log_tail "$SCRATCH/status-before.json")"
  REACHED_END=yes
  exit 1
fi
fact status PASS "before the change — pending-apply: $(printf '%s' "${PENDING:-<no row>}" | cut -c1-300); drift: $(printf '%s' "$DRIFT_BEFORE" | cut -c1-400)"

# ---- the member's own update: what applies the record to the member's IAM

UPDATE_START=$(date -u +%s)
if [ "$RUN_UPDATE" = no ]; then
  fact update UNVERIFIED "the member's own update was skipped (--no-update), so nothing here shows the row leaving out what an update applies: rerun without it, or pass --since far enough back to hold the member's last one"
elif [ "$MEMBERSHIP" != yes ]; then
  fact update UNVERIFIED "this machine holds no config for the member $MEMBER, so its own \`fugaro update\` was not run here: run it on the member's machine inside the window, or pass --since far enough back to hold the member's last one"
else
  say "running the member's own update ($MEMBER), which is what applies the record's grants to its IAM; up to $TIMEOUT_MIN minutes"
  UPDATE_STATUS=0
  run_bounded "$((TIMEOUT_MIN * 60))" "$SCRATCH/update.log" \
    env FUGARO_PROJECT="$MEMBER" fugaro update || UPDATE_STATUS=$?
  if [ "$UPDATE_STATUS" = 124 ]; then
    fact update UNVERIFIED "the member's own \`fugaro update\` did not finish inside $TIMEOUT_MIN minutes and was killed; its last lines: $(log_tail "$SCRATCH/update.log")"
  elif [ "$UPDATE_STATUS" != 0 ]; then
    fact update UNVERIFIED "the member's own \`fugaro update\` exited $UPDATE_STATUS, so what it applied is not known: $(log_tail "$SCRATCH/update.log")"
  else
    fact update PASS "the member's own \`fugaro update\` exited 0 on this machine, which is what writes the record's grants into the member's IAM: $(log_tail "$SCRATCH/update.log" 2)"
  fi
fi

# ---- the console's own write, made by API (this check cannot drive a browser)

GRANT_START=$(date -u +%s)
say "adding $ROLE for user:$GRANTEE on $MEMBER_PROJECT — the write a Cloud Console edit makes"
GRANT_STATUS=0
g_at "$SCRATCH/grant.log" projects add-iam-policy-binding "$MEMBER_PROJECT" \
  --member="user:$GRANTEE" --role="$ROLE" --condition=None --format=none || GRANT_STATUS=$?
if [ "$GRANT_STATUS" != 0 ]; then
  fact grant FAIL "Google refused the IAM change this check makes with your credentials (exit $GRANT_STATUS): $(log_tail "$SCRATCH/grant.log" 3) — pass --grantee <an address of your own> if Google would not take $GRANTEE"
  REACHED_END=yes
  exit 1
fi
fact grant PASS "Google accepted the change this check makes by API, the same binding a console edit adds: $ROLE for user:$GRANTEE on $MEMBER_PROJECT"
GRANTED=yes

# ---- Google's own record of both changes, read as internal/infra reads it

WINDOW_EPOCH=$((GRANT_START - SINCE * 60))
if [ "$UPDATE_START" -lt "$WINDOW_EPOCH" ]; then
  WINDOW_EPOCH=$UPDATE_START
fi
WINDOW_START=$(python3 -c 'import datetime,sys; print(datetime.datetime.fromtimestamp(int(sys.argv[1]), datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"))' "$WINDOW_EPOCH")

LOG_STATUS=0
LOG_ERROR=""
HAND_ENTRY=""
HAND_AT=""
HAND_ACTOR=""
HAND_ROLE=""
HAND_MEMBER=""
deadline=$((GRANT_START + WAIT))
while :; do
  LOG_STATUS=0
  read_log "$SCRATCH/log.json" "$WINDOW_START" || LOG_STATUS=$?
  log_deltas "$SCRATCH/log.json" >"$SCRATCH/deltas.tsv"
  LOG_ERROR=$(awk -F'\t' '$1 == "error" { print $5; exit }' "$SCRATCH/deltas.tsv" 2>/dev/null || true)
  [ -z "$LOG_ERROR" ] || break
  [ "$LOG_STATUS" = 0 ] || break
  if awk -F'\t' -v g="user:$GRANTEE" '$5 == g { found = 1 } END { exit !found }' "$SCRATCH/deltas.tsv"; then
    break
  fi
  if [ "$(date -u +%s)" -ge "$deadline" ]; then
    break
  fi
  left=$((deadline - $(date -u +%s)))
  [ "$left" -gt 15 ] && left=15
  say "waiting for the change to appear in Admin Activity..."
  sleep "$left"
done

if [ "$LOG_STATUS" != 0 ]; then
  fact log UNVERIFIED "this check's own read of $MEMBER_PROJECT's Admin Activity failed (exit $LOG_STATUS): $(log_tail "$SCRATCH/log.err" 3). The drift row reads the same log with your Application Default Credentials, and its own words are in the row below; roles/logging.viewer on the member is what reads it, and a user credential also needs a quota project: gcloud auth application-default set-quota-project <a project you may use>"
elif [ -n "$LOG_ERROR" ]; then
  fact log UNVERIFIED "Google answered, and this check could not read the answer: $LOG_ERROR. The entries are in $SCRATCH/log.json, which --dir keeps; the drift row's own read is in the row below"
else
  changes=$(grep -c . "$SCRATCH/deltas.tsv" 2>/dev/null || true)
  fact log PASS "your credentials read $MEMBER_PROJECT's Admin Activity from $WINDOW_START, its newest entries (at most 200): ${changes:-0} SetIamPolicy binding(s) added, in Google's own record"
fi

HAND_LINE=$(awk -F'\t' -v g="user:$GRANTEE" '$5 == g { print; exit }' "$SCRATCH/deltas.tsv" 2>/dev/null || true)
if [ "$LOG_STATUS" = 0 ] && [ -z "$LOG_ERROR" ]; then
  if [ -z "$HAND_LINE" ]; then
    fact entry FAIL "Admin Activity holds no SetIamPolicy entry for this check's own change ($ROLE for user:$GRANTEE) in the window from $WINDOW_START, although Google accepted the write: what the drift row compares has no entry to name. A longer --wait, or a --since that reaches back further, is what tells a slow log apart from a change Google logged in a shape this reader has no rule for"
    REACHED_END=yes
    exit 1
  fi
  HAND_ENTRY=$(printf '%s' "$HAND_LINE" | cut -f1)
  HAND_AT=$(printf '%s' "$HAND_LINE" | cut -f2)
  HAND_ACTOR=$(printf '%s' "$HAND_LINE" | cut -f3)
  HAND_ROLE=$(printf '%s' "$HAND_LINE" | cut -f4)
  HAND_MEMBER=$(printf '%s' "$HAND_LINE" | cut -f5)
  fact entry PASS "Google logged the change as entry $HAND_ENTRY at $HAND_AT, made by $HAND_ACTOR: $HAND_ROLE for $HAND_MEMBER — the entry the drift row must name"
  NEXT="${NEXT}  the entry, in Google's console  https://console.cloud.google.com/logs/query;query=insertId%3D%22${HAND_ENTRY}%22?project=$MEMBER_PROJECT
"
fi

# ---- status after: the drift row, read again with the change in place

AFTER_STATUS=$(cli "$WORKSPACE" "$SCRATCH/status-after.json" workspace project status "$MEMBER" --json)
status_rows "$SCRATCH/status-after.json" >"$SCRATCH/rows-after.tsv"
if [ -n "$(rows_error "$SCRATCH/rows-after.tsv")" ]; then
  fact names FAIL "\`fugaro workspace project status $MEMBER --json\` did not print the document on the second read (exit $AFTER_STATUS): $(log_tail "$SCRATCH/status-after.json")"
  REACHED_END=yes
  exit 1
fi
DRIFT_AFTER=$(row_detail "$SCRATCH/rows-after.tsv" "member:$MEMBER" drift)
if [ -z "$DRIFT_AFTER" ]; then
  fact names FAIL "the document of the second \`status $MEMBER\` carries no drift row, so nothing compared the member's live IAM against the record (exit $AFTER_STATUS): $(log_tail "$SCRATCH/status-after.json")"
  REACHED_END=yes
  exit 1
fi
NEXT="${NEXT}  the drift row, in your terminal  fugaro workspace project status $MEMBER --project $WORKSPACE
"

case "$DRIFT_AFTER" in
*"Cloud Audit Logs"*)
  # The row says, in its own words, that the log was not read: that is U5
  # answered no, measured rather than guessed.
  fact names UNVERIFIED "the drift row reports the audit-log read as not measured, which is U5's answer here (no): $(printf '%s' "$DRIFT_AFTER" | cut -c1-600)"
  fact silent UNVERIFIED "the log was not compared, so neither half of this check's assertion could be shown: $(printf '%s' "$DRIFT_AFTER" | cut -c1-400)"
  ;;
*)
  if [ -z "$HAND_ENTRY" ]; then
    fact names UNVERIFIED "this check's own read of the log has no entry for its change (see the log and entry rows), so the row's naming cannot be tied to Google's record; the row reads: $(printf '%s' "$DRIFT_AFTER" | cut -c1-600)"
    fact silent UNVERIFIED "this check could not read the log itself, so it cannot say which entries the member's own update wrote; the row reads: $(printf '%s' "$DRIFT_AFTER" | cut -c1-400)"
  else
    ADDRESS=${HAND_MEMBER#user:}
    WANT="changed in the console: $ADDRESS as $HAND_ROLE on $MEMBER_PROJECT ($HAND_ENTRY)"
    case "$DRIFT_AFTER" in
    *"$WANT"*)
      fact names PASS "the drift row names this check's own console change, with Google's own entry for it: $WANT"
      ;;
    *)
      fact names FAIL "the drift row does not name this check's own change as \`$WANT\`; the row reads: $(printf '%s' "$DRIFT_AFTER" | cut -c1-600) — \`fugaro workspace project reconcile $MEMBER\` is the fix if the finding is a real one, and a row that keeps missing a change Google logged is the bug this check hunts"
      ;;
    esac

    # ... and it must name none of what the member's OWN update applied: what an
    # update writes is the record, so it is stated and never a console change.
    own=0
    own_ids=""
    own_named=""
    other_named=""
    while IFS=$'\t' read -r entry at actor role member; do
      [ -n "$entry" ] || continue
      [ "$entry" = "$HAND_ENTRY" ] && continue
      if [ -n "$at" ] && [ "$(printf '%s' "$at" | cut -c1-19)" \> "$(printf '%s' "$HAND_AT" | cut -c1-19)" ]; then
        continue
      fi
      if [ "$actor" != "$HAND_ACTOR" ]; then
        case "$DRIFT_AFTER" in
        *"($entry)"*) other_named="${other_named}${other_named:+, }$entry by $actor" ;;
        esac
        continue
      fi
      own=$((own + 1))
      own_ids="${own_ids}${own_ids:+, }$entry"
      case "$DRIFT_AFTER" in
      *"($entry)"*) own_named="${own_named}${own_named:+, }$entry ($role for $member)" ;;
      esac
    done <"$SCRATCH/deltas.tsv"
    other_clause=""
    if [ -n "$other_named" ]; then
      other_clause="; it does name $other_named, a change somebody else made while this check ran, which is the cross-check working"
    fi
    if [ "$own" = 0 ]; then
      fact silent UNVERIFIED "no SetIamPolicy change of the member's own update is in the part of the window this check read (from $WINDOW_START, the newest 200 entries), so there was nothing to show the row leaving out: run the member's own \`fugaro update\` inside the window — or a --since that reaches back to its last one — and run this check again"
    elif [ -n "$own_named" ]; then
      fact silent FAIL "the drift row names change(s) the member's own update made, which is what an update applying the record must never be: $own_named (entries $own_ids). The row reads: $(printf '%s' "$DRIFT_AFTER" | cut -c1-600)"
    else
      fact silent PASS "the drift row names none of the $own change(s) the member's own update made in the window ($own_ids): what an update applies is the record, so the cross-check leaves it alone$other_clause"
    fi
  fi
  ;;
esac

# ---- restore: the binding this check added goes away again

REACHED_END=yes
