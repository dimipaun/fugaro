#!/usr/bin/env bash
# Check 720 — the folder mechanism end to end, on throwaway projects, and the
# console page `workspace project rm` prints. Run by YOU. It settles U1, U3 and
# U8 of docs/workspace-projects-users/reconciliation.md and the RETURN half of
# U6, and it records rather than claims U6's own case (the `u6` row below).
#
# What it settles, and how it reads each one:
#
#   U1  the exact permissions to move an existing project into a folder, and
#       whether a project with NO organization can be moved by the CLI or only
#       through Google's migration flow. Read three ways: the parent of the
#       workspace's own project before and after `workspace init` (the CLI's own
#       move, R8 — init creates that folder under the first organization its own
#       search answers, D2803, so a credential that sees more than one stops the
#       check rather than reading that move as a failure), the parent of the member's project before and after
#       `workspace project link` (the member owner's own move, D2819/A6), and a
#       `gcloud projects move` of a project created with no parent at all —
#       Google's own answer about the CLI's ability to move such a project.
#   U3  that R3's URL is Google's shut-down page for a project. `workspace
#       project rm` prints it (workspaceProjectRmWarning, R3); the check asserts
#       the exact string and prints it in its block for YOU to open. It never
#       opens a browser and never waits for you to.
#   U6  the return itself: `workspace project unlink` on the member's own machine
#       moves the member back to the parent the link record named, and the parent
#       read after it says so (FolderAuthority.Unlink, D2822). It does NOT settle
#       U6's own case — a caller holding roles/owner on the FOLDER and no rights
#       on the member. Both throwaway installations answer to ONE gcloud account,
#       which holds the member project's owner rights and the folder's owner
#       binding at once, so no call this check can make is that caller: the `u6`
#       row records the skip rather than claiming it settled. The product holds
#       U6 open the same way: `project rm` on the folder mechanism prints the
#       member owner's own command, because a folder owner is not known to be
#       able to do it for them (workspaceProjectRmAuthority).
#   U8  whether Google lets a person be made a project's owner BY API outside an
#       organization, or only by an invitation they accept in the console. The
#       check creates a project with no organization (no --parent, no
#       --organization) and makes exactly the call SAAuthority.GrantOwner makes:
#       an IAM policy write adding roles/owner. Either answer settles U8 and the
#       row names it, with the console step the CLI prints when Google refuses
#       (ErrSAOwnerInvite, D2825) beside it.
#
# What it creates, what it costs, and what it deletes again. The check is a
# throwaway installation foundry: two `fugaro setup` runs create the workspace's
# own project and the member's, each with Firebase, its Realtime Database and the
# boring-cost baseline behind them (the credentials service, the images, the
# daily check), plus a test folder and a third project with no parent. That is
# real money until the projects are deleted, which is what the `teardown` row
# does at the end (and `--keep` does not). The two folders are NOT deleted: a
# project spends about 30 days in DELETE_REQUESTED, Resource Manager refuses a
# folder delete while one under it is in that state, and a folder costs nothing —
# the row names the commands for you to run once the projects are really gone.
# Nothing of YOUR machine's
# installations is read or written: the check's entire config root is its own
# scratch directory (XDG_CONFIG_HOME), so `fugaro` cannot see a config of yours,
# and the projects it moves are the ones it created itself.
#
# Two steps need a real terminal and get one: `fugaro setup --create-project`
# and `--link-billing` are confirmed by being at a terminal (nothing is typed),
# so each create runs at a pty (run_at_a_terminal) rather than on a pipe. A
# coding agent's session is refused outright: no agent runs any step of this.
#
# What it substitutes, and why. A fresh installation has no repository, so it has
# no job service account and no exchange URL (both are per repository), and the
# member's entry in the workspace's table is built from all four values the
# workspace's service resolves a caller by. This check's subject is the move and
# the console page, not the run-time identity a member's token carries, so the
# entry is declared with well-formed values of the check's own (`add`'s row names
# them) and the pin's assertion key is the check's own placeholder — no command
# publishes that key, deliberately (workspace.go's workspaceAddCommand). The
# member has no runs, no token and no repository in this check.
#
# Usage:      bash scripts/live-checks/check-720.sh --org <org-id> --billing <account>
#               --org       the organization the test folder is created under,
#                           and the one the check reads `workspace init`'s own
#                           folder against — init picks its organization itself,
#                           the first the API's search answers, so a credential
#                           that sees several has to see one for this check to
#                           read U1's move (numeric id, as
#                           `gcloud organizations list` prints it)
#               --billing   the billing account the two throwaway projects are
#                           created on (as `gcloud billing accounts list`
#                           prints it)
#               --region    where the throwaway installations live (default
#                           us-east5, the US region the wizard's place answer
#                           names)
#               --rigor     the throwaway projects' rigor mode (default poc)
#               --dir       the directory this check works in, kept as it is and
#                           never removed; by default a scratch directory under
#                           TMPDIR that the exit trap removes
#               --grantee   the address the U8 owner grant is attempted for
#                           (default: the account gcloud is authenticated as)
#               --member-repo  the repository the member's entry says it
#                           contributes to, as owner/name (default
#                           chk720/<member>): a placeholder, as above
#               --keep      leave every project and folder it made (and say so)
#               --dry-run   print the plan and call nothing
#               --help
# Needs:      bash, mktemp, python3 (the pty runner), gcloud, fugaro.
# Runs:       by YOU. It refuses to start in a coding agent's session and in a
#             traced shell. Every cloud call runs with stdin closed and every
#             gcloud call with --quiet. Its test (check-720_test.sh) drives it
#             against fakes on PATH and touches no cloud and no terminal.
# Output:     progress on stderr, ONE block on stdout headed CHECK 720 RESULT.
#             Its last line is the verdict, and the verdict is the exit status:
#               PASS (exit 0)        every row settled its question
#               FAIL (exit 1)        a move did not happen, the console page is
#                                    not R3's, or a read answered something
#                                    else
#               INCOMPLETE (exit 2)  the run stopped before it could measure
#               INCOMPLETE (exit 3)  it measured, and a row could not be read
#                                    (UNVERIFIED)
#             A row that reads INFO is a reading, not a verdict: it records what
#             this check does not measure, and it is neither of the two above.
# Expected duration: two installations, ~20 to 40 minutes, most of it the two
# `fugaro setup` runs and Terraform's own applies.
set -euo pipefail

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }

XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null

FACTS=""
SCRATCH=""
CONFIG_HOME=""
NEXT=""
SCRATCH_REMOVED=no
CALLER_DIR=""
REACHED_END=no
ABORT=""
KEEP=no
DRY_RUN=no

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
Check 720 — the folder mechanism end to end, on throwaway projects. It creates
a test folder, two throwaway installations (`fugaro setup`), the workspace's own
folder and a project with no organization, runs `workspace init`, `workspace
project add`, `workspace add`, `workspace project link`, `workspace project rm`
and `workspace project unlink` on them, reads every folder move and its return,
and prints R3's console page for you to open. Run by YOU; no agent runs any step
of it.

Usage:
  bash scripts/live-checks/check-720.sh --org <org-id> --billing <account> [flags]
  bash scripts/live-checks/check-720.sh --help

  --org <id>          the organization the test folder is created under, and the
                      one the check reads `workspace init`'s own folder against
                      (required; init picks its organization itself, so use a
                      credential that sees only this one)
  --billing <account> the billing account the throwaway projects are created on
                      (required)
  --region <region>   where the throwaway installations live (default us-east5)
  --rigor <mode>      the throwaway projects' rigor mode (default poc)
  --dir <path>        work in this directory and keep it (default: a scratch
                      directory under TMPDIR, removed by the exit trap)
  --grantee <address> who the U8 owner grant is attempted for (default: the
                      account gcloud is authenticated as)
  --member-repo <o/n> the placeholder repository the member's entry contributes
                      to (default chk720/<member>)
  --keep              leave every project and folder this check made
  --dry-run           print the plan and call nothing

It writes nothing of yours: its config root is its own directory, so no
installation of this machine is read or changed. It creates real GCP projects
(they cost money until the teardown row deletes them) and deletes every project
it created unless --keep is given. It leaves the two folders it made: a folder
cannot be deleted while a project under it is still in DELETE_REQUESTED, so the
teardown row names the commands to run about 30 days later.

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
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs Check 720: it creates real projects and moves them between folders with the owner's own credentials. Run it yourself, at your own terminal: bash scripts/live-checks/check-720.sh --org <org-id> --billing <account>"
}

guard_xtrace() {
  [ "$XTRACE_AT_START" = no ] ||
    stop "this shell is tracing (bash -x), and its trace would print the account, the billing id and every project id this check makes. Run it without -x"
}

need() {
  command -v "$1" >/dev/null 2>&1 || stop "$1 is not on PATH, and this check is built on it"
}

ORG=""
BILLING=""
REGION=us-east5
RIGOR=poc
GRANTEE=""
MEMBER_REPO=""

while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  --org)
    [ $# -ge 2 ] || stop "--org needs an organization id"
    ORG=$2
    shift
    ;;
  --org=*)
    ORG=${1#--org=}
    ;;
  --billing)
    [ $# -ge 2 ] || stop "--billing needs a billing account id"
    BILLING=$2
    shift
    ;;
  --billing=*)
    BILLING=${1#--billing=}
    ;;
  --region)
    [ $# -ge 2 ] || stop "--region needs a region"
    REGION=$2
    shift
    ;;
  --region=*)
    REGION=${1#--region=}
    ;;
  --rigor)
    [ $# -ge 2 ] || stop "--rigor needs a mode"
    RIGOR=$2
    shift
    ;;
  --rigor=*)
    RIGOR=${1#--rigor=}
    ;;
  --dir)
    [ $# -ge 2 ] || stop "--dir needs a path"
    CALLER_DIR=$2
    shift
    ;;
  --dir=*)
    CALLER_DIR=${1#--dir=}
    ;;
  --grantee)
    [ $# -ge 2 ] || stop "--grantee needs an address"
    GRANTEE=$2
    shift
    ;;
  --grantee=*)
    GRANTEE=${1#--grantee=}
    ;;
  --member-repo)
    [ $# -ge 2 ] || stop "--member-repo needs owner/name"
    MEMBER_REPO=$2
    shift
    ;;
  --member-repo=*)
    MEMBER_REPO=${1#--member-repo=}
    ;;
  --keep)
    KEEP=yes
    ;;
  --dry-run)
    DRY_RUN=yes
    ;;
  *)
    stop "unknown argument $1 (usage: check-720.sh --org <org-id> --billing <account>, or --help)"
    ;;
  esac
  shift
done

guard_xtrace
guard_agent

[ -n "$ORG" ] || stop "no --org was given, and this check creates a folder under an organization: pass the numeric id \`gcloud organizations list\` prints"
printf '%s' "$ORG" | grep -Eq '^[0-9]+$' ||
  stop "--org $ORG is not an organization id (it is the numeric id, not the display name)"
[ -n "$BILLING" ] || stop "no --billing was given, and this check creates two GCP projects: pass the billing account id \`gcloud billing accounts list\` prints"
printf '%s' "$REGION" | grep -Eq '^[a-z]+(-[a-z]+)+[0-9]+$' ||
  stop "--region $REGION is not a GCP region"
case "$RIGOR" in poc | startup | pro | critical) ;; *) stop "--rigor $RIGOR is not one of poc, startup, pro or critical" ;; esac

need mktemp
need python3
need gcloud
need fugaro

STAMP=$(date -u +%s)
WS="chk720ws${STAMP}"
MEMBER="chk720mem${STAMP}"
NOORG="chk720no${STAMP}"
FOLDER_NAME="chk720-test-${STAMP}"
[ -n "$MEMBER_REPO" ] || MEMBER_REPO="chk720/${MEMBER}"
# The member's entry needs a job account and an exchange URL, and a throwaway
# installation with no repository has neither: both are the check's own, named in
# the add row and nowhere else.
MEMBER_JOB_SA="fugaro-job-chk720@${MEMBER}.iam.gserviceaccount.com"
ASSERTION_KEY="chk720-not-a-key"

if [ -n "$CALLER_DIR" ]; then
  [ -d "$CALLER_DIR" ] || stop "--dir $CALLER_DIR is not a directory"
  SCRATCH=$CALLER_DIR
else
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk720.XXXXXXXX") || stop "cannot make a scratch directory"
  chmod 700 "$SCRATCH"
fi
CONFIG_HOME=$SCRATCH/config
FACTS=$SCRATCH/facts
: >"$FACTS"
mkdir -p "$CONFIG_HOME"
# Every `fugaro` call this check makes — `setup` included — reads and writes
# THIS config root: XDG_CONFIG_HOME is exported for the whole run, so no
# installation of the owner's own is ever in reach of a command here, and a
# throwaway project's config cannot land in their ~/.config.
export XDG_CONFIG_HOME="$CONFIG_HOME"

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
  cat >&2 <<PLAN
Check 720 plan (--dry-run: nothing below is called)
  organization        organizations/$ORG
  billing account     $BILLING
  test folder         $FOLDER_NAME under organizations/$ORG
  workspace           $WS  (project $WS, an installation of its own)
  member              $MEMBER  (project $MEMBER, an installation of its own, created under the test folder)
  no-org project      $NOORG  (no parent: U1's no-org half and U8)
  workspace's folder  created by \`fugaro workspace init $WS\` (under the first
                      organization its own search answers)
  steps               workspace init, project add, workspace add, project link,
                      project add (again), project rm, project unlink
  U8                  an owner grant by API for ${GRANTEE:-<the account gcloud is authenticated as>} on $NOORG
  teardown            both projects and the no-org project are deleted; the two
                      folders are left (a folder cannot be deleted before the
                      projects under it leave Google's ~30-day delete queue)
  config root         $CONFIG_HOME (this check's own; no installation of this machine is read)
PLAN
}

# g_do is one gcloud call: stdin closed, --quiet always, stdout left to the
# caller and stderr in $SCRATCH/last.err.
g_do() {
  gcloud --quiet "$@" </dev/null 2>"$SCRATCH/last.err"
}
g_err() { cat "$SCRATCH/last.err" 2>/dev/null || true; }

# run_at_a_terminal runs CMD with a REAL terminal on its stdin, stdout and
# stderr, for the steps whose confirmation is being at one (fugaro setup
# --create-project and --link-billing type nothing: the terminal is the answer).
# It prints nothing and its own exit status IS the child's, which the caller
# reads with `|| status=$?`; the transcript it prints is written to $2. The child
# is killed at the deadline, which is reported as 124.
run_at_a_terminal() { # run_at_a_terminal <seconds> <transcript> <cmd...>
  python3 - "$1" "$2" "${@:3}" <<'PY'
import os, pty, select, signal, subprocess, sys, time

secs, transcript, argv = float(sys.argv[1]), sys.argv[2], sys.argv[3:]
master, slave = pty.openpty()
child = subprocess.Popen(argv, stdin=slave, stdout=slave, stderr=slave,
                         start_new_session=True, close_fds=True)
os.close(slave)
buf = b""
deadline = time.time() + secs
timed_out = False
while True:
    ready, _, _ = select.select([master], [], [], 0.2)
    if ready:
        try:
            chunk = os.read(master, 65536)
        except OSError:
            chunk = b""
        if chunk:
            buf += chunk
            continue
    if child.poll() is not None:
        break
    if time.time() > deadline:
        timed_out = True
        try:
            os.killpg(os.getpgid(child.pid), signal.SIGKILL)
        except OSError:
            pass
        child.wait()
        break
# The child is gone; drain what the pty still holds, briefly.
edge = time.time() + 1.0
while time.time() < edge:
    ready, _, _ = select.select([master], [], [], 0.1)
    if not ready:
        continue
    try:
        chunk = os.read(master, 65536)
    except OSError:
        break
    if not chunk:
        break
    buf += chunk
os.close(master)
with open(transcript, "wb") as fh:
    fh.write(buf)
sys.exit(124 if timed_out else child.returncode)
PY
}

# cli runs this check's own `fugaro` with this check's own config root, prints
# the exit status, and leaves stdout and stderr in $2. Every call has stdin
# closed: no fugaro command here asks anything, and one that did would be a bug
# this check should see rather than a prompt it should answer.
cli() { # cli <fugaro-project> <log> <args...>
  local project=$1 log=$2
  shift 2
  local status=0
  if [ -n "$project" ]; then
    (XDG_CONFIG_HOME="$CONFIG_HOME" FUGARO_PROJECT="$project" fugaro "$@" </dev/null >"$log" 2>&1) || status=$?
  else
    (XDG_CONFIG_HOME="$CONFIG_HOME" fugaro "$@" </dev/null >"$log" 2>&1) || status=$?
  fi
  printf '%s' "$status"
}

log_tail() { # log_tail <file> [lines]: the last lines, on one line
  tail -n "${2:-4}" "$1" 2>/dev/null | tr '\n' ' ' | tr -s ' ' | cut -c1-600
}

parent_of() { # parent_of <project-id>: the project's parent, or none, or unreadable
  local out status=0
  out=$(g_do projects describe "$1" --format='value(parent)') || status=$?
  if [ "$status" != 0 ]; then
    printf 'unreadable'
    return 0
  fi
  if [ -z "$out" ]; then
    printf 'none'
    return 0
  fi
  printf '%s' "$out"
}

config_url() { # config_url <config-file> <block> <key>: the url under a block
  python3 - "$1" "$2" "$3" <<'PY' 2>/dev/null || true
import re, sys

path, block, key = sys.argv[1], sys.argv[2], sys.argv[3]
value, inblock = "", False
try:
    lines = open(path, encoding="utf-8").read().splitlines()
except OSError:
    lines = []
for raw in lines:
    line = raw.rstrip()
    if not line.strip() or line.lstrip().startswith("#"):
        continue
    ind = len(line) - len(line.lstrip(" "))
    body = line.strip()
    if ind == 0:
        inblock = body.startswith(block + ":")
        continue
    if inblock:
        m = re.match(r"^%s:\s*(.*)$" % re.escape(key), body)
        if m:
            value = m.group(1).strip().strip("'\"")
            break
print(value)
PY
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
  printf '\nCHECK 720 RESULT\n'
  if [ -n "$NEXT" ]; then printf '%s\n' "$NEXT"; fi
  printf 'ROWS\n'
  printf '%s\n' "$1" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-14s %-10s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 720 RESULT: %s (exit %s)\n' "$(verdict "$1")" "$2"
}

finish() {
  local status=$?
  trap - EXIT
  local facts
  facts=$(cat "$FACTS" 2>/dev/null || true)
  cleanup
  if [ -n "$ABORT" ]; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT"
  elif [ "$REACHED_END" = no ]; then
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): what was not reached was NOT measured"
  fi
  case "$SCRATCH_REMOVED" in
  yes) facts="${facts}"$'\n'"restore|PASS|this check's own working directory (its whole XDG_CONFIG_HOME, so no installation of yours was ever in reach) is gone" ;;
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

if [ "$DRY_RUN" = yes ]; then
  # A dry run calls nothing and writes no block: it is the plan, and the exit
  # trap is taken off before it leaves so the block is not printed over it.
  plan
  trap - EXIT
  cleanup
  exit 0
fi

say "working in $SCRATCH (its own XDG_CONFIG_HOME at $CONFIG_HOME)"
say "projects this check makes: $WS, $MEMBER (under the test folder) and $NOORG (no parent)"

# ------------------------------------------------------------------ premise

ACCOUNT=""
st=0
ACCOUNT=$(g_do config get-value account --format='value(core.account)') || st=$?
if [ "$st" != 0 ] || [ -z "$ACCOUNT" ] || [ "$ACCOUNT" = "(unset)" ]; then
  fact premise FAIL "gcloud is not authenticated as a person: $PROG needs the owner's own credentials, and 'gcloud auth login' is the fix"
  stop "no authenticated gcloud account: $(g_err)"
fi
[ -n "$GRANTEE" ] || GRANTEE=$ACCOUNT
printf '%s' "$GRANTEE" | grep -Eq '^[!-~]+@[!-~]+$' ||
  stop "--grantee $GRANTEE is not an address"

ORGS=""
st=0
ORGS=$(g_do organizations list --format='value(ID)') || st=$?
if [ "$st" != 0 ]; then
  fact premise FAIL "the organizations this credential can see were not read ($(g_err)): this check creates a folder under one, so it cannot start"
  stop "the organization list was not read"
fi
if ! printf '%s\n' "$ORGS" | grep -qx "$ORG"; then
  fact premise FAIL "organization $ORG is not one this credential can see (it sees: $(printf '%s' "$ORGS" | tr '\n' ' ')): pass the id of an organization you are an owner of"
  stop "organization $ORG is not visible to this credential"
fi
ORG_COUNT=$(printf '%s\n' "$ORGS" | grep -c . || true)

for p in "$WS" "$MEMBER" "$NOORG"; do
  if g_do projects describe "$p" >/dev/null 2>&1; then
    fact premise FAIL "the project id $p already exists: this check makes its own throwaway projects, so run it again (the ids carry the second the run started)"
    stop "project $p already exists"
  fi
done

if ! g_do billing accounts describe "$BILLING" >/dev/null 2>&1; then
  fact premise FAIL "billing account $BILLING was not read ($(g_err)): the two throwaway projects are created on it, so it has to be one this credential can open"
  stop "billing account $BILLING is not readable"
fi

ORG_NOTE=""
# `init` picks its own organization — the first the API's search answers
# (workspace_init.go, D2803) — so on a credential that sees several the
# organization this check names with --org is not necessarily the one used, and
# the init row below reads which one that was.
[ "$ORG_COUNT" -gt 1 ] && ORG_NOTE=", and this credential sees more than one organization, so the init row below reads which one init itself used rather than assuming --org"

fact premise PASS "authenticated as $ACCOUNT, organization $ORG is visible (this credential sees $ORG_COUNT organization(s)), billing account $BILLING is readable, and the ids $WS, $MEMBER and $NOORG are free$ORG_NOTE"

# ------------------------------------------------------------- the test folder

st=0
FOLDER=$(g_do resource-manager folders create --organization="$ORG" --display-name="$FOLDER_NAME" --format='value(name)') || st=$?
if [ "$st" != 0 ] || ! printf '%s' "$FOLDER" | grep -Eq '^folders/[0-9]+$'; then
  fact folder FAIL "the test folder was not created ($(g_err)): this check moves a project OUT of a folder and back INTO it, so it needs a parent of its own to move back to"
  stop "the test folder was not created"
fi
FOLDER_NUM=${FOLDER#folders/}
# A folder is not a project: what parent it hangs under is read from the folder
# itself, never with `projects describe`.
st=0
FP=$(g_do resource-manager folders describe "$FOLDER_NUM" --format='value(parent)') || st=$?
if [ "$st" != 0 ]; then
  fact folder UNVERIFIED "the test folder $FOLDER was created, and its parent was not read ($(g_err))"
  stop "the test folder's parent was not read, and everything below is read against it: nothing was created under it"
else
  if [ "$FP" = "organizations/$ORG" ]; then
    fact folder PASS "the test folder $FOLDER exists under organizations/$ORG (display name $FOLDER_NAME): it is the parent the member's project is created in and moved back to"
  else
    fact folder FAIL "the test folder $FOLDER was created under $FP, not organizations/$ORG: nothing below can be read as a move to a known parent"
    stop "the test folder is not under organization $ORG"
  fi
fi

# ------------------------------------------------- the two throwaway installations

# setup_installation brings one installation up at a real terminal. The wizard's
# questions are all answered by flags; the confirmations that create the project
# and link its billing are the terminal itself (nothing is typed).
setup_installation() { # setup_installation <name> <parent> <label>: its status is setup's
  local name=$1 parent=$2 label=$3
  local log=$SCRATCH/setup-$label.log status=0
  say "fugaro setup $name (at a real terminal; this is the slow part)"
  run_at_a_terminal 3600 "$log" fugaro setup \
    --create-project --gcp-project "$name" --parent "$parent" \
    --link-billing "$BILLING" --name "$name" --region "$REGION" --rigor "$RIGOR" \
    --bake-repos=false --firebase-project "$name" --budget-mode observe || status=$?
  return "$status"
}

reason=""
for pair in "workspace:$WS:organizations/$ORG" "member:$MEMBER:folders/$FOLDER_NUM"; do
  label=${pair%%:*}
  rest=${pair#*:}
  name=${rest%%:*}
  parent=${rest#*:}
  status=0
  setup_installation "$name" "$parent" "$label" || status=$?
  if [ "$status" = 0 ]; then
    say "setup $name finished"
  fi
  if [ "$status" = 124 ]; then
    reason="${reason}${reason:+; }setup $name did not finish inside an hour (its transcript is $SCRATCH/setup-$label.log)"
  elif [ "$status" = 1 ]; then
    reason="${reason}${reason:+; }setup $name left a step for you: $(log_tail "$SCRATCH/setup-$label.log")"
  elif [ "$status" != 0 ]; then
    reason="${reason}${reason:+; }setup $name exited $status: $(log_tail "$SCRATCH/setup-$label.log")"
  fi
done

CONFIG_WS=$CONFIG_HOME/fugaro/projects/$WS.yaml
CONFIG_MEMBER=$CONFIG_HOME/fugaro/projects/$MEMBER.yaml
if [ -z "$reason" ] && [ -f "$CONFIG_WS" ] && [ -f "$CONFIG_MEMBER" ]; then
  WS_PARENT=$(parent_of "$WS")
  MEMBER_PARENT=$(parent_of "$MEMBER")
  fact installations PASS "both throwaway installations are up: $WS under $WS_PARENT and $MEMBER under $MEMBER_PARENT, each with its own config in this check's own config root (it created the projects, added Firebase, linked billing $BILLING and applied its Terraform, at a real terminal, with nothing typed)"
else
  if [ -z "$reason" ]; then
    reason="setup wrote no config for $( [ -f "$CONFIG_WS" ] || printf '%s ' "$WS" )$( [ -f "$CONFIG_MEMBER" ] || printf '%s' "$MEMBER" )"
  fi
  fact installations UNVERIFIED "$reason"
  stop "the two throwaway installations did not come up: $reason"
fi

# --------------------------------------------------------------- workspace init

# The workspace's own project is named after the workspace (D1290), and `init`
# creates the workspace's folder under an organization it picks ITSELF — the
# first one this credential's organization search answers (workspace_init.go,
# D2803) — and moves that project into the folder (R8). The move is U1's first
# reading, and the organization it chose is the search's answer, not this
# check's --org.
before=$(parent_of "$WS")
status=$(cli "$WS" "$SCRATCH/init.log" workspace init "$WS")
OUT=$(cat "$SCRATCH/init.log" 2>/dev/null || true)
if [ "$status" != 0 ]; then
  fact init FAIL "fugaro workspace init $WS exited $status: $(log_tail "$SCRATCH/init.log")"
  stop "workspace init failed"
fi
after=$(parent_of "$WS")
WS_FOLDER=""
if printf '%s' "$after" | grep -Eq '^folders/[0-9]+$'; then
  WS_FOLDER=$after
fi
WS_FOLDER_PARENT=""
if [ -n "$WS_FOLDER" ]; then
  WS_FOLDER_PARENT=$(g_do resource-manager folders describe "${WS_FOLDER#folders/}" --format='value(parent)' 2>/dev/null || printf 'unreadable')
fi
case "$OUT" in
*"initialized workspace $WS: the folder mechanism"*) MECH=yes ;;
*) MECH=no ;;
esac
if [ "$MECH" = no ]; then
  fact init FAIL "fugaro workspace init $WS did not report the folder mechanism: $(log_tail "$SCRATCH/init.log") — an SA-mechanism answer here means this credential's organization search answered no organization, and this check's whole subject is the folder"
  stop "workspace init did not take the folder mechanism"
fi
if [ -z "$WS_FOLDER" ]; then
  fact init FAIL "the workspace's own project $WS still hangs under $after (it was $before), not under any folder: the move init makes did not happen"
  stop "the workspace's own project did not move into the workspace's folder"
fi
if [ "$WS_FOLDER_PARENT" = "organizations/$ORG" ]; then
  fact init PASS "workspace init created the workspace's folder $WS_FOLDER under $WS_FOLDER_PARENT, bound $ACCOUNT to roles/owner on it, and placed the workspace's own project in it — the CLI's own move, from $before, with the caller's own credentials (R8, D2803) — and wrote the roster at workspaceRosters/$WS with the caller as its first admin"
elif [ "$WS_FOLDER_PARENT" = "unreadable" ]; then
  fact init UNVERIFIED "workspace init placed the workspace's own project in the folder $WS_FOLDER (it was $before), and read the folder's parent failed ($(g_err)): whether that parent is organizations/$ORG is the whole of this row, and this credential sees $ORG_COUNT organization(s), so the organization init chose cannot be assumed — init takes the first the API's search answers (workspace_init.go, D2803). Read it with: gcloud resource-manager folders describe ${WS_FOLDER#folders/} --format=value\\(parent\\). The projects this run already made ($WS, $MEMBER, under the test folder $FOLDER) are still there and still cost money: delete them with \`gcloud projects delete $WS $MEMBER\`"
  stop "the workspace's folder parent was not read"
elif [ "$ORG_COUNT" -gt 1 ]; then
  fact init UNVERIFIED "workspace init created the workspace's folder $WS_FOLDER under $WS_FOLDER_PARENT, which is not this check's --org organizations/$ORG: this credential sees $ORG_COUNT organization(s), and init creates its folder under the FIRST one the API's search answers (workspace_init.go, D2803), so which organization it chose is the search's answer and not this check's to name. U1's move is a project out of $FOLDER and back into it, both in organizations/$ORG, and Google refuses to move a project between organizations, so a run here would measure a different move than U1 states. Run this check with a credential that sees only organizations/$ORG; the projects this run already made ($WS, $MEMBER, under the test folder $FOLDER) are still there and still cost money, so delete them with \`gcloud projects delete $WS $MEMBER\` first"
  stop "workspace init chose another organization"
else
  fact init FAIL "workspace init created the workspace's folder $WS_FOLDER under $WS_FOLDER_PARENT, and this credential sees only organizations/$ORG: the folder is not under the organization this check names"
  stop "the workspace's folder is not under the organization this check names"
fi

# ------------------------------------------------------------------ project add

# The member's entry in the workspace's table, built from all four values the
# workspace's service resolves a caller by. The job account, its repository and
# the exchange URL are this check's own placeholders: a fresh installation has no
# repository, so it has no job account (they are per repository) and no exchange
# URL — see the header.
EXCHANGE=$(config_url "$CONFIG_WS" credentials_service url)
if [ -z "$EXCHANGE" ]; then
  EXCHANGE="https://exchange.chk720.invalid/$WS"
  EXCHANGE_NOTE="the workspace's own credentials service URL is not in its config yet (it lands on the apply after the one that created the service), so the entry names this check's placeholder"
else
  EXCHANGE_NOTE="the entry names the workspace's own credentials service URL"
fi
ADD_ARGS=(workspace project add "$MEMBER" --workspace "$WS"
  --job-service-account "$MEMBER_JOB_SA"
  --job-repos "$MEMBER_JOB_SA=$MEMBER_REPO"
  --repo "$MEMBER_REPO"
  --firebase-project "$MEMBER" --fp "$MEMBER" --exchange-url "$EXCHANGE"
  --no-apply)

status=$(cli "$WS" "$SCRATCH/add1.log" "${ADD_ARGS[@]}")
OUT1=$(cat "$SCRATCH/add1.log" 2>/dev/null || true)
if [ "$status" != 0 ]; then
  fact add FAIL "fugaro ${ADD_ARGS[*]} exited $status: $(log_tail "$SCRATCH/add1.log")"
  stop "project add failed"
fi
case "$OUT1" in
*"the member's own owner runs fugaro workspace project link"*)
  fact add PASS "project add declared $MEMBER in workspace $WS's table and handed the member's own owner the link step — the entry names the job account $MEMBER_JOB_SA (this check's placeholder), the repository $MEMBER_REPO (this check's placeholder) and $EXCHANGE_NOTE. The add's follow-up steps were left with --no-apply: this check settles the move and the console page, not the workspace's published record"
  ;;
*)
  fact add UNVERIFIED "project add exited 0 without the handoff sentence this check reads: $(log_tail "$SCRATCH/add1.log")"
  ;;
esac

# --------------------------------------------------------------------- the pin

# The member's own machine pins the workspace before it can link: `workspace add`
# writes the membership key and the four pin values, and `workspace project link`
# refuses a config that pins nothing. The assertion key is this check's own
# placeholder because no command publishes it (workspace.go's workspaceAddCommand
# spells every other value). The service account is the workspace's own
# (WorkspaceServiceAccountID, "the workspace service account its own fugaro update
# binds its read-only database grant to"), which is the one account whose name
# this check can compute from the workspace's project id.
PIN_SA="fugaro-workspace@$WS.iam.gserviceaccount.com"
status=$(cli "$MEMBER" "$SCRATCH/pin.log" workspace add "$WS" \
  --firebase-project "$WS" --issuer "https://securetoken.google.com/$WS" \
  --assertion-key "$ASSERTION_KEY" --service-account "$PIN_SA")
if [ "$status" != 0 ]; then
  fact pin FAIL "fugaro workspace add $WS on the member's machine exited $status: $(log_tail "$SCRATCH/pin.log")"
  stop "the member could not be pinned to the workspace"
fi
if grep -q '^workspace:' "$CONFIG_MEMBER" 2>/dev/null && grep -q '^workspace_pin:' "$CONFIG_MEMBER" 2>/dev/null; then
  fact pin PASS "the member's own config now carries the membership workspace: $WS and its pin (Firebase project $WS, issuer https://securetoken.google.com/$WS, the workspace's own service account $PIN_SA, and an assertion key that is this check's own placeholder because no command publishes that key). Nothing was applied: the pin takes effect at the member's own fugaro update, which this check does not run"
else
  fact pin FAIL "the member's config states no membership and no pin after workspace add: this check read $CONFIG_MEMBER, and the move that follows is refused without them"
  stop "the pin was not written"
fi

# ------------------------------------------------------------------ the move in

# `workspace project link` runs on the member's own machine with the member
# owner's own credentials: it moves the member's project into the workspace's
# folder --- U1's second reading --- files the member owner's audit entry, and
# writes the link record the move back is read from (A6, R14).
before=$MEMBER_PARENT
status=$(cli "$MEMBER" "$SCRATCH/link.log" workspace project link)
OUT=$(cat "$SCRATCH/link.log" 2>/dev/null || true)
if [ "$status" != 0 ]; then
  fact move FAIL "fugaro workspace project link exited $status: $(log_tail "$SCRATCH/link.log")"
  stop "the member's project was not linked"
fi
after=$(parent_of "$MEMBER")
if [ "$after" = "$WS_FOLDER" ] && [ "$before" = "folders/$FOLDER_NUM" ]; then
  MOVE_DETAIL="the member's project $MEMBER moved from $before to $WS_FOLDER (the workspace's own folder), which is the same move as the workspace project's, made by the member owner's own credentials on the member's own machine (D2819, A6). The permissions it took: the caller is an owner of $MEMBER (it created it) and can write the parent it now hangs under — the destination's owner binding is what the CLI reads before it moves (workspaceLinkPolicyReport)"
  if printf '%s' "$OUT" | grep -q "$WS now holds $MEMBER"; then
    fact move PASS "$MOVE_DETAIL, and the command reported that $WS now holds $MEMBER, with the member owner's own audit entry filed"
  else
    fact move UNVERIFIED "$MOVE_DETAIL, and the command's own sentence about the member holding the workspace was not read: $(log_tail "$SCRATCH/link.log")"
  fi
else
  fact move FAIL "the member's project $MEMBER hangs under $after after the link, and this check expected $WS_FOLDER: $(log_tail "$SCRATCH/link.log")"
fi

# ------------------------------------------------------------------ adopt again

# link hands the workspace's owner back the add, which is what adopts the member's
# own grantees and writes the workspace's people into the member's roster and
# mirror (D2805, D2813). It is the same command line as the first add.
status=$(cli "$WS" "$SCRATCH/add2.log" "${ADD_ARGS[@]}")
OUT2=$(cat "$SCRATCH/add2.log" 2>/dev/null || true)
if [ "$status" != 0 ]; then
  fact adopt FAIL "the second fugaro workspace project add exited $status: $(log_tail "$SCRATCH/add2.log")"
else
  # The sentence the command really prints: `adopted <N person(people)> into
  # workspace <ws>'s roster: <addresses>` (workspace_project.go's
  # workspacePeoplePhrase). The count depends on how many of the member's
  # grantees are new to the workspace's roster, so the pattern takes either
  # count and the roster tail, and the second branch is the add that adopted
  # nobody.
  case "$OUT2" in
  *"adopted "*" into workspace $WS's roster"* | *"no grantee of project $MEMBER is new to workspace $WS"*)
    fact adopt PASS "the add the link hands back ran: $(log_tail "$SCRATCH/add2.log" 3) — the workspace's people are in the member's own roster and mirror, or the check read that no grantee was new"
    ;;
  *)
    fact adopt UNVERIFIED "the second project add exited 0 without the adoption sentence this check reads: $(log_tail "$SCRATCH/add2.log")"
    ;;
  esac
fi

# ----------------------------------------------------------- the console page

# R3: `workspace project rm` detaches the member and prints the page a person
# shuts the project down from. The check asserts the exact string and prints it
# in the block for YOU to open --- it never opens a browser and never waits.
R3="https://console.cloud.google.com/iam-admin/settings?project=$MEMBER"
status=$(cli "$WS" "$SCRATCH/rm.log" workspace project rm "$MEMBER" --no-save-state)
OUT=$(cat "$SCRATCH/rm.log" 2>/dev/null || true)
if [ "$status" != 0 ]; then
  fact detach FAIL "fugaro workspace project rm $MEMBER exited $status: $(log_tail "$SCRATCH/rm.log")"
else
  case "$OUT" in
  *"shut $MEMBER down yourself at $R3"*)
    fact detach PASS "workspace project rm detached $MEMBER (--no-save-state: nothing of its settings was snapshotted), and printed R3's page: $R3"
    NEXT=$(printf 'WHAT TO OPEN, BY YOU (R3, U3)\n  %s\n  a browser is the one thing this check cannot use, so opening it is yours: it is\n  where Google shuts a project down. The row above holds the sentence the command\n  printed, and it is the whole of U3.' "$R3")
    ;;
  *"$R3"*)
    fact detach UNVERIFIED "workspace project rm printed R3's URL, and not in the sentence this check reads: $(log_tail "$SCRATCH/rm.log")"
    ;;
  *)
    fact detach FAIL "workspace project rm exited 0 and printed no R3 page: $(log_tail "$SCRATCH/rm.log") — R3's whole content is that this URL is printed and never opened"
    ;;
  esac
fi

# ---------------------------------------------------------------- the move back

# U6's return: the member owner takes its own project back out of the workspace's
# folder, to the parent the link record names. `u6` below records what that does
# NOT settle: U6's own case needs a caller with the folder's rights and none on
# the member, and this check's one gcloud account holds both.
status=$(cli "$MEMBER" "$SCRATCH/unlink.log" workspace project unlink)
OUT=$(cat "$SCRATCH/unlink.log" 2>/dev/null || true)
if [ "$status" != 0 ]; then
  fact return FAIL "fugaro workspace project unlink exited $status: $(log_tail "$SCRATCH/unlink.log")"
else
  back=$(parent_of "$MEMBER")
  if [ "$back" = "folders/$FOLDER_NUM" ]; then
    if printf '%s' "$OUT" | grep -q "$WS no longer holds $MEMBER"; then
      fact return PASS "$MEMBER moved back from $WS_FOLDER to its original parent $back, and the command reported the authority gone (D2822). The credentials this took are the member owner's own, on the member's own machine — the return the task's acceptance names, and the command the product hands the member owner when the workspace's mechanism is the folder (workspaceProjectRmAuthority, workspace_project_rm.go)"
    else
      fact return UNVERIFIED "$MEMBER is back at $back, and the command's own sentence about the authority was not read: $(log_tail "$SCRATCH/unlink.log")"
    fi
  else
    fact return FAIL "$MEMBER hangs under $back after the unlink, and this check expected folders/$FOLDER_NUM (its parent before the link): $(log_tail "$SCRATCH/unlink.log")"
  fi
fi
fact u6 INFO "U6's OWN case is NOT measured here, and this row is the skip: whether a caller holding roles/owner on the FOLDER and NO rights on the member's project can move it back needs a second credential, and both throwaway installations answer to the one gcloud account this check runs as — the account that created the member project and holds its owner rights, and that created the workspace's folder and holds roles/owner on it. No call this check can make is the caller U6 asks about, so U6 stays open in docs/workspace-projects-users/reconciliation.md and the product keeps printing the member owner's own command (workspaceProjectRmAuthority). To settle it: grant a second identity roles/owner on the test folder alone, give it no binding on the member's project, and run fugaro workspace project unlink as that identity on the member's own machine"

# ------------------------------------------------- the no-org project: U1 and U8

# Google's own answer, not the CLI's: a project created with no parent (no
# --parent, no --organization) is exactly the project U1 and U8 ask about.
st=0
created=$(g_do projects create "$NOORG" --format='value(projectId)') || st=$?
if [ "$st" != 0 ] || [ "$created" != "$NOORG" ]; then
  fact noorg FAIL "the project with no organization was not created ($(g_err)): U1's second half and U8 are what this project exists for"
  stop "the project with no organization was not created"
fi
NOORG_PARENT=$(parent_of "$NOORG")

st=0
g_do projects move "$NOORG" --folder="$FOLDER_NUM" >/dev/null || st=$?
move_err=$(g_err)
if [ "$st" != 0 ] && printf '%s' "$move_err" | grep -Eqi 'unknown command|not found|no such command|Invalid choice'; then
  st=0
  g_do beta projects move "$NOORG" --folder="$FOLDER_NUM" >/dev/null || st=$?
  move_err=$(g_err)
fi
if [ "$st" = 0 ]; then
  MOVED=$(parent_of "$NOORG")
  if [ "$MOVED" = "folders/$FOLDER_NUM" ]; then
    fact noorg PASS "a project created with NO organization (its parent read: $NOORG_PARENT) WAS moved by an API call (gcloud projects move) into the test folder $FOLDER: U1's no-org half reads that a parentless project can be moved by an API, and a person has a route that is not Google's migration flow. The CLI's own link refuses such a project before it tries (authority_folder.go, 'project %s is under no parent')"
  else
    fact noorg UNVERIFIED "gcloud projects move exited 0 and $NOORG hangs under $MOVED, which is not the test folder $FOLDER: the move's answer cannot be read"
  fi
elif printf '%s' "$move_err" | grep -Eqi 'unknown command|not found|no such command|Invalid choice'; then
  fact noorg UNVERIFIED "this gcloud has no projects move (neither 'gcloud projects move' nor 'gcloud beta projects move' answered), so whether a project with no organization can be moved by an API was not read: $move_err"
else
  fact noorg PASS "a project created with NO organization (its parent read: $NOORG_PARENT) was NOT moved by an API call: gcloud projects move answered: $(printf '%s' "$move_err" | tr '\n' ' ' | cut -c1-400). That is U1's no-org half, in Google's own words, and it is why the CLI refuses a member under no parent rather than moving it (authority_folder.go) and why such a project joins an organization by Google's own migration"
fi

# U8. The call is the one SAAuthority.GrantOwner makes: an IAM policy write
# adding roles/owner, made by API, on a project outside an organization.
st=0
g_do projects add-iam-policy-binding "$NOORG" --member "user:$GRANTEE" --role roles/owner --condition=None >/dev/null || st=$?
grant_err=$(g_err)
if [ "$st" = 0 ]; then
  u8="Google ACCEPTED an owner grant made by API on a project outside an organization ($GRANTEE as roles/owner on $NOORG)"
  u8state=PASS
elif printf '%s' "$grant_err" | grep -Eqi 'invit'; then
  u8="Google REFUSED the owner grant made by API and answered with an invitation: $(printf '%s' "$grant_err" | tr '\n' ' ' | cut -c1-400)"
  u8state=PASS
else
  u8="the owner grant by API answered something this check cannot read as either an acceptance or an invitation (exit $st): $(printf '%s' "$grant_err" | tr '\n' ' ' | cut -c1-400)"
  u8state=UNVERIFIED
fi
CONSOLE_IAM="https://console.cloud.google.com/iam-admin/iam?project=$NOORG"
U8STEP=$(printf 'Google refuses an owner grant on the project %s made by API, which is outside an organization: invite %s as an owner in the Cloud Console at %s and have them accept the invitation Google sends, then run this again to confirm the binding' \
  "$NOORG" "$GRANTEE" "$CONSOLE_IAM")
if [ "$st" = 0 ]; then
  U8BESIDE="The workspace side makes the same call from SAAuthority.GrantOwner (D2809), so an SA-mechanism workspace outside an organization would have made this binding by API; its console-invitation branch (ErrSAOwnerInvite, D2825) is the path Google takes when it refuses, and it prints that step rather than reporting a grant it did not make"
elif printf '%s' "$grant_err" | grep -Eqi 'invit'; then
  U8BESIDE="The workspace side makes the same call from SAAuthority.GrantOwner, and turns exactly this refusal into the step a person takes: \"$U8STEP\""
else
  U8BESIDE="The workspace side makes the same call from SAAuthority.GrantOwner; where Google refuses it, the CLI prints the console-invitation step instead of reporting a grant it did not make (ErrSAOwnerInvite, D2825)"
fi
fact u8 "$u8state" "U8: an owner grant by API on $NOORG (created with no organization, parent read: $NOORG_PARENT) for user:$GRANTEE: $u8. $U8BESIDE"

# ------------------------------------------------------------- teardown

deleted=""
left=""
if [ "$KEEP" = yes ]; then
  fact teardown UNVERIFIED "--keep was given: the projects $WS, $MEMBER and $NOORG, the folder $FOLDER and the workspace's folder $WS_FOLDER are STILL THERE, and the installations in them keep costing money until you shut them down yourself"
else
  for p in "$WS" "$MEMBER" "$NOORG"; do
    if g_do projects delete "$p" >/dev/null 2>&1; then
      deleted="${deleted}${deleted:+, }$p"
    else
      left="${left}${left:+, }$p"
    fi
  done
  if [ -n "$left" ]; then
    fact teardown FAIL "these were NOT deleted and are still costing money: $left (deleted: $deleted). Delete them yourself: gcloud projects delete <id>"
  else
    # NO folder delete is issued, deliberately. A project spends about 30 days in
    # DELETE_REQUESTED before it is gone, and Resource Manager refuses to delete a
    # folder while any project under it is still in that state — so a folder delete
    # issued here can only fail, and a FAIL row for a call that cannot succeed would
    # make the check unable to pass however right its moves are. Folders cost
    # nothing; the row names the commands to run once the projects are really gone.
    folders="$FOLDER"
    [ -n "$WS_FOLDER" ] && folders="$folders, $WS_FOLDER (the workspace's own)"
    fact teardown PASS "every project this check created was deleted: $deleted. These folders are left behind, and that is on purpose: $folders. A project spends about 30 days in DELETE_REQUESTED before it is really gone, Resource Manager refuses to delete a folder while a project under it is still in that state, and folders themselves cost nothing. Once the projects are gone, delete each with \`gcloud resource-manager folders delete <id>\`"
  fi
fi

say "reached the end of the check"
REACHED_END=yes
