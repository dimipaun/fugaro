#!/usr/bin/env bash
# Check 811 — the auth retirement's SECOND release, as ONE script YOU run.
#
# `credentials-belong-to-models` takes a repository's auth mode away and serves
# its calls from the installation's credentials service. Release 1 writes the
# access record (`creds/repoaccess/<slug>`, C1 (d)) and leaves the old one
# (`creds/repoauth/<slug>`) in place; release 2 takes the token out of the
# agent's environment (CM28), rejects a file that still writes a retired key
# (CM29) and stops writing the old record, deleting it only where the repository
# has no run in flight (CM31). Nothing offline can settle what that does to a
# real installation, and the plan's §8 says what to watch (CM22):
#
#   - a run on a subscription has no token in its agent's environment;
#   - a launch on an image older than release 1 is refused with the rebuild
#     command;
#   - a file that still writes `agent.auth` is rejected with the fix;
#   - an old record is kept while a run is in flight and gone after.
#
# So the check does its own setup and reads, row by row:
#
#   premise     the subscription checkout is a git checkout whose committed file
#               writes NO retired key (release 2 refuses a launch on one that
#               does), and the installation's `creds` node carries the
#               repository's ACCESS record and an OLD record whose `auth` is
#               `oauth` — the state release 1 left, which is the state this
#               check is about. A premise that does not hold is a STOP (exit 2),
#               not a finding.
#   launch      one run of the subscription repository is launched, and its id
#               and slug recorded. It is the run the rows below are about.
#   old-record  `fugaro update --repo` is run in the subscription checkout WHILE
#               that run is going: the old record must still be there
#               afterwards (CM31). Where the installation carries no old record
#               at all the row reads UNVERIFIED and says how to measure it.
#   token       the subscription repository's job is read for what its container
#               holds — no `CLAUDE_CODE_OAUTH_TOKEN` — and the run is followed
#               to its end: the service served its calls, and the run's own
#               record names no subscription credential for any of its stages,
#               so the agent went out on the service and on nothing of its own
#               (CM9, CM28).
#   record-gone the same `fugaro update --repo` is run again, now that no run of
#               the repository is going: the old record must be GONE (CM31).
#   retired-key `fugaro validate` in the checkout you point `--retired-key` at
#               must REJECT its file, naming the retired key and the fix
#               (CM29). UNVERIFIED without the flag.
#   old-image   a launch in the checkout you point `--old-image` at must be
#               REFUSED, naming the command that rebuilds the image (CM28).
#               UNVERIFIED without the flag.
#   restore     the check's own scratch directory is gone.
#
# It launches ONE paid run (and, where `--old-image` is given, a second launch
# that must be refused before it becomes one), and runs `fugaro update --repo`
# twice. The run COSTS MONEY: its model spend. It is NOT read-only.
#
# Usage:      bash scripts/live-checks/check811.sh --installation <name> \
#               --subscription <checkout> --job <the subscription repo's job> \
#               --gcp-project <project> --region <region> \
#               [--rtdb-url <url>] [--retired-key <checkout>] \
#               [--old-image <checkout>] [--service <name>] [--dry-run]
#             --dry-run       prints the plan and calls NOTHING: no gcloud, no
#                             fugaro, no run, no update.
#             --installation  the installation's Fugaro project name, the one
#                             `fugaro update --project` takes.
#             --subscription  the checkout of the repository on a subscription,
#                             the one the run is launched in.
#             --job           the Cloud Run job that repository's runs go out on,
#                             as the installation names it: the `token` row
#                             reads ITS container environment, because that is
#                             where the token would be.
#             --gcp-project   the installation's GCP project (also CHECK811_GCP_PROJECT).
#             --region        the Cloud Run region (also CHECK811_REGION).
#             --rtdb-url      the Realtime Database URL, when the installation's
#                             own local project config (budget.rtdb_url) does not
#                             name it.
#             --retired-key   a checkout whose `fugaro.yaml` still writes a
#                             retired key (`agent.auth`, `agent.oauth_token`, a
#                             craft's `access` or a grade's `access`): the row
#                             runs `fugaro validate` in it and reads the
#                             refusal. Without it that row reads UNVERIFIED.
#             --old-image     a checkout whose repository's job image is OLDER
#                             than release 1: the row launches in it and reads
#                             the refusal. Without it that row reads UNVERIFIED.
#             --service       the credentials service's Cloud Run service name
#                             (default fugaro-credentials).
# Needs:      bash, mktemp, python3 (every document is read by one python3
#             reader), gcloud (authenticated as the project owner: the log read,
#             the database read, the job read and the run records out of the
#             runs bucket), fugaro, git. Nothing is installed and nothing is
#             fetched.
# Where:      the installation, project and region you name, and NOWHERE else —
#             every call carries them explicitly and the script never reads
#             `gcloud config get-value project`. Run it on an installation you
#             may upgrade and on a repository you may spend on.
# Costs:      one run's model spend (bounded by the repository's own cap), and
#             nothing else. The `old-image` launch costs nothing when it is
#             refused, which is what the row is about.
# Runs:       by YOU. No agent runs any step of it — the check refuses to start
#             in a coding agent's session (CLAUDECODE, CLAUDE_CODE_ENTRYPOINT,
#             CLAUDE_CODE_REMOTE, CURSOR_AGENT, AI_AGENT) — and its own test
#             (scripts/live-checks/check811_test.sh) drives it against fake
#             `gcloud` and `fugaro` on PATH and touches no cloud.
# Cleanup:    its scratch directory is removed from the exit trap, and the
#             `restore` row says whether it really went. NOTHING ELSE IS
#             CREATED: the run it launches is left to end on its own, and the
#             two updates are the ones the owner would run anyway.
# Output:     progress on stderr, ONE block on stdout headed CHECK 811 RESULT,
#             to copy back. Its last line is the verdict, and the verdict is the
#             exit status:
#
#               CHECK 811 RESULT: PASS (exit 0)        every criterion passed;
#               CHECK 811 RESULT: FAIL (exit 1)        a criterion was measured
#                                                      and failed;
#               CHECK 811 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                      could measure everything;
#               CHECK 811 RESULT: INCOMPLETE (exit 3)  it ran to the end and at
#                                                      least one row reads
#                                                      UNVERIFIED.
#
#             ONLY an all-PASS block is a PASS.
#
# NO secret is read at all, and none may be printed: every document this check
# reads is a name, a status or a record. The job's own document is read for the
# NAMES of its container's environment and the IDs of the secrets it mounts, and
# this check never prints a value of one — a job document carries references,
# never a value, and the reader here prints no `value` field.
set -euo pipefail

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }

XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null

FACTS=""
SCRATCH=""
SCRATCH_REMOVED=no
REACHED_END=no
ABORT=""
RUN_ID=""
RUN_REF=""
RUN_SLUG=""
RUN_STATE=""
RUN_ENDED=no
CANONICAL=""
OLD_SLUG=""
OLD_BEFORE=""
AUTH_CONF=""

stop() {
  printf '%s: stopped: %s\n' "$PROG" "$1" >&2
  ABORT=$1
  exit 2
}

fact() { # fact KEY PASS|FAIL|UNVERIFIED "detail"
  [ -n "$FACTS" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS"
}

need() {
  command -v "$1" >/dev/null 2>&1 || stop "$1 is not on PATH, and this check reads a real installation with it"
}

# jget FILE PATH prints the value at a dotted path of a JSON document, or the
# empty string. A field that is absent is an absence rather than an error: a row
# that has to tell "absent" from "unreadable" reads the exit status of the
# command that wrote the file, not this.
jget() { # jget <file> <dotted.path>
  python3 - "$1" "$2" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    print("")
    sys.exit(0)
cur = doc
for part in sys.argv[2].split("."):
    if part == "":
        continue
    if isinstance(cur, list):
        try:
            cur = cur[int(part)]
        except Exception:
            print("")
            sys.exit(0)
    elif isinstance(cur, dict):
        if part not in cur:
            print("")
            sys.exit(0)
        cur = cur[part]
    else:
        print("")
        sys.exit(0)
if isinstance(cur, (dict, list)):
    print(json.dumps(cur))
elif isinstance(cur, bool):
    print("true" if cur else "false")
else:
    print(cur)
PY
}

guard_agent() {
  local marker found=""
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
      found="${found}${found:+, }$marker"
    fi
  done
  [ -z "$found" ] ||
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 811: it upgrades a real installation and launches a paid run. Run it yourself: bash scripts/live-checks/check811.sh --installation <name> --subscription <checkout> --job <job> --gcp-project <project> --region <region>"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x), and its trace would print the arguments of every command this check runs. Run it without -x: bash scripts/live-checks/check811.sh"
  fi
}

usage() {
  cat <<'USAGE'
Check 811 — the auth retirement's second release: the token out of the agent's
environment, the image gate, the retired key and the old record. Run by YOU,
against an installation release 1 reached and release 2 is reaching.

Usage:
  bash scripts/live-checks/check811.sh --installation <name> \
    --subscription <checkout> --job <the subscription repo's job> \
    --gcp-project <project> --region <region> \
    [--rtdb-url <url>] [--retired-key <checkout>] [--old-image <checkout>] \
    [--service <name>] [--dry-run]
  bash scripts/live-checks/check811.sh --help

It launches ONE paid run on the subscription repository and runs
`fugaro update --repo` in that checkout twice — once with the run in flight and
once after it ends. Every row it prints is one criterion of the register's Check
811; the last line of the block is the verdict and the exit status:

  PASS (exit 0)        every criterion passed
  FAIL (exit 1)        a criterion was measured and failed
  INCOMPLETE (exit 2)  the run stopped before it could measure everything
  INCOMPLETE (exit 3)  at least one row reads UNVERIFIED
USAGE
}

INSTALLATION=${CHECK811_INSTALLATION:-}
GCP_PROJECT=${CHECK811_GCP_PROJECT:-}
REGION=${CHECK811_REGION:-}
SUBSCRIPTION=${CHECK811_SUBSCRIPTION:-}
JOB=${CHECK811_JOB:-}
RTDB_URL=${CHECK811_RTDB_URL:-}
RETIRED_KEY=${CHECK811_RETIRED_KEY:-}
OLD_IMAGE=${CHECK811_OLD_IMAGE:-}
SERVICE=${CHECK811_SERVICE:-fugaro-credentials}
DRY_RUN=no
if [ "${CHECK811_DRY_RUN:-}" = "1" ]; then DRY_RUN=yes; fi

while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  --installation)
    [ $# -ge 2 ] || stop "--installation needs the installation's name"
    INSTALLATION=$2
    shift 2
    ;;
  --gcp-project)
    [ $# -ge 2 ] || stop "--gcp-project needs the project id"
    GCP_PROJECT=$2
    shift 2
    ;;
  --region)
    [ $# -ge 2 ] || stop "--region needs the Cloud Run region"
    REGION=$2
    shift 2
    ;;
  --subscription)
    [ $# -ge 2 ] || stop "--subscription needs the path of the subscription repository's checkout"
    SUBSCRIPTION=$2
    shift 2
    ;;
  --job)
    [ $# -ge 2 ] || stop "--job needs the name of the subscription repository's Cloud Run job"
    JOB=$2
    shift 2
    ;;
  --rtdb-url)
    [ $# -ge 2 ] || stop "--rtdb-url needs the database's URL"
    RTDB_URL=$2
    shift 2
    ;;
  --retired-key)
    [ $# -ge 2 ] || stop "--retired-key needs the path of a checkout whose file still writes a retired key"
    RETIRED_KEY=$2
    shift 2
    ;;
  --old-image)
    [ $# -ge 2 ] || stop "--old-image needs the path of a checkout whose job image is older than release 1"
    OLD_IMAGE=$2
    shift 2
    ;;
  --service)
    [ $# -ge 2 ] || stop "--service needs the credentials service's name"
    SERVICE=$2
    shift 2
    ;;
  --dry-run)
    DRY_RUN=yes
    shift
    ;;
  *)
    stop "unknown argument $1 (usage: check811.sh --installation <name> --subscription <checkout> --job <job> --gcp-project <project> --region <region>, --help, or --dry-run)"
    ;;
  esac
done

dry_run_plan() {
  cat <<PLAN
Check 811 plan (nothing is called: no gcloud, no fugaro, no run, no update)

  installation:  ${INSTALLATION:-<not given>}
  gcp project:   ${GCP_PROJECT:-<not given>}
  region:        ${REGION:-<not given>}
  subscription:  ${SUBSCRIPTION:-<not given>}
  job:           ${JOB:-<not given>}
  rtdb url:      ${RTDB_URL:-<read from the local project config of the installation: budget.rtdb_url>}
  retired key:   ${RETIRED_KEY:-<not given: the retired-key row is UNVERIFIED without it>}
  old image:     ${OLD_IMAGE:-<not given: the old-image row is UNVERIFIED without it>}
  service:       ${SERVICE}

What it would do, in order:

  premise      git -C ${SUBSCRIPTION:-<subscription>} show HEAD:fugaro.yaml
               -> its committed file must write NO retired key (agent.auth, agent.oauth_token, a craft's
               access, a grade's access), which release 2 refuses a launch on; and
               curl <rtdb>/creds.json
               -> the creds node must carry the repository's access record (creds/repoaccess) and an old
               record (creds/repoauth/<slug>) whose auth is oauth. A premise that does not hold STOPS
               the check (exit 2).
  launch       fugaro run --task-file <a task the check writes> --json   (in ${SUBSCRIPTION:-<subscription>})
               -> the paid run every row below is about.
  old-record   the run's own row is read (it must be going), then
               fugaro update --repo --project ${INSTALLATION:-<installation>}
               -> the old record must still be there afterwards (CM31: an update deletes a
               repository's old record only where it has no run in flight). The update's own output is
               read for the line that says so.
  token        gcloud run jobs describe ${JOB:-<job>} --format=json
               -> no CLAUDE_CODE_OAUTH_TOKEN in the job's container environment (CM28's flip takes the
               mount away); then the run is followed to its end, the service's log is read for a served
               line (it went through its gateway), and
               gcloud storage cat gs://fugaro-runs-<project>/runs/<ref>/result.json
               -> no stage of its record names a subscription credential (credential/served_by = oauth),
               so the run's agent had no token of its own.
  record-gone  with no run of the repository going (the check's run has ended, and no other row of
               'fugaro runs ls' is live), the same fugaro update --repo is run again
               -> the old record must be GONE (CM31).
  retired-key  cd ${RETIRED_KEY:-<checkout>} && fugaro validate
               -> it must FAIL, naming the retired key the file writes and the command that fixes it
               (CM29). UNVERIFIED without --retired-key.
  old-image    cd ${OLD_IMAGE:-<checkout>} && fugaro run --task-file <task> --json
               -> it must be REFUSED, naming the command that rebuilds the image (fugaro update, or
               fugaro image build) and the release it needs (CM28). UNVERIFIED without --old-image.
  restore      the check's own scratch directory is removed.

Costs: one run's model spend, bounded by the repository's own cap. The old-image launch costs nothing
when it is refused, which is what the row is about.
Cleanup: the check's scratch directory. Nothing else is created.
PLAN
}

if [ "$DRY_RUN" = no ]; then
  [ -n "$INSTALLATION" ] || stop "no --installation was given (or --help for the usage)"
  [ -n "$GCP_PROJECT" ] || stop "no --gcp-project was given (or --help for the usage)"
  [ -n "$REGION" ] || stop "no --region was given (or --help for the usage)"
  [ -n "$SUBSCRIPTION" ] || stop "no --subscription was given (or --help for the usage)"
  [ -n "$JOB" ] || stop "no --job was given (or --help for the usage)"
fi

guard_xtrace
guard_agent

if [ "$DRY_RUN" = yes ]; then
  dry_run_plan
  exit 0
fi

need bash
need mktemp
need python3
need gcloud
need fugaro
need git

[ -d "$SUBSCRIPTION" ] || stop "${SUBSCRIPTION} is not a directory: --subscription is the checkout of the repository on a subscription"
[ -d "$SUBSCRIPTION/.git" ] || stop "${SUBSCRIPTION} is not a git checkout: --subscription is the repository the check launches its run in"
if [ -n "$RETIRED_KEY" ] && [ ! -d "$RETIRED_KEY" ]; then
  stop "${RETIRED_KEY} is not a directory: --retired-key is a checkout whose file still writes a retired key"
fi
if [ -n "$OLD_IMAGE" ] && [ ! -d "$OLD_IMAGE" ]; then
  stop "${OLD_IMAGE} is not a directory: --old-image is a checkout whose job image is older than release 1"
fi

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk811.XXXXXXXX") || stop "cannot make a scratch directory"
chmod 700 "$SCRATCH"
FACTS="$SCRATCH/facts"
: >"$FACTS"
MARKER="$SCRATCH/.chk811-mine"
: >"$MARKER"
AUTH_CONF="$SCRATCH/auth.conf"

cleanup() {
  [ -n "$SCRATCH" ] || return 0
  [ -f "$MARKER" ] || { say "refusing to remove $SCRATCH: it carries no marker from this check"; return 0; }
  rm -rf "$SCRATCH" 2>/dev/null || true
  [ -e "$SCRATCH" ] || SCRATCH_REMOVED=yes
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
  local facts=$1 status=$2
  printf '\nCHECK 811 RESULT\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-14s %-11s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 811 RESULT: %s (exit %s)\n' "$(verdict "$facts")" "$status"
}

finish() {
  local status=$?
  trap - EXIT
  # The facts are read BEFORE the scratch directory holding them is removed.
  local facts
  facts=$(cat "$FACTS" 2>/dev/null || true)
  cleanup
  if [ -n "$ABORT" ] && ! printf '%s\n' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|$ABORT"
  fi
  if [ "$REACHED_END" = no ] && ! printf '%s\n' "$facts" | grep -q '^aborted|'; then
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): the rows that were not reached were NOT measured, which is this script ending on an error rather than an answer about the release"
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|the scratch directory is gone (its documents: the subscription checkout's committed file's retired keys, the database's creds node before, during and after the two updates, the two updates' own outputs, the run's own row and record, the job's own document, the service's own log lines, and the task files this check wrote). Nothing else was created: the run it launched is left to end on its own, and the two updates are the ones the owner would run anyway"
  else
    facts="${facts}"$'\n'"restore|FAIL|the scratch directory is STILL THERE, at ${SCRATCH} — remove it yourself"
  fi
  if [ "$REACHED_END" = yes ] && [ "$status" -eq 0 ]; then
    case "$(verdict "$facts")" in
    PASS) status=0 ;;
    FAIL) status=1 ;;
    *) status=3 ;;
    esac
  fi
  print_block "$facts" "$status"
  exit "$status"
}
trap finish EXIT

# ---------------------------------------------------------------- the readers

# committed_keys CHECKOUT prints the retired keys the checkout's COMMITTED
# `fugaro.yaml` writes, one per line, or nothing. It is the file a run of the
# repository reads (the runner clones the default branch), and release 2 refuses
# a launch from the default branch on a file that writes one (CM29).
committed_keys() { # committed_keys <checkout>
  local tmp="$SCRATCH/committed.yaml"
  git -C "$1" show HEAD:fugaro.yaml >"$tmp" 2>/dev/null || : >"$tmp"
  retired_keys "$tmp"
}

# working_keys CHECKOUT prints the retired keys the checkout's WORKING
# `fugaro.yaml` writes, one per line, or nothing. It is the file the LAUNCHER
# reads when the check launches its run in that checkout, so a working file that
# writes one is refused there even where the committed one is clean.
working_keys() { # working_keys <checkout>
  retired_keys "$1/fugaro.yaml"
}

# retired_keys FILE prints the retired keys a `fugaro.yaml` writes, one per line:
# `agent.auth` and `agent.oauth_token` under the `agent` block, `agent.crafts.
# <craft>.access` under it, and `model_grades.<grade>.access` at the top level
# (CM29's list; the fields are internal/config's — config.Agent.Auth,
# config.Agent.OAuthToken, config.Craft.Access and the grades' — and a
# `providers.<name>.auth`, which is NOT retired, is skipped by reading the
# section each key sits in rather than the key's name alone). It is a scan of
# the file's indentation, and it is only ever a PREMISE: the row that measures
# the refusal reads `fugaro validate`'s own answer.
retired_keys() { # retired_keys <file>
  python3 - "$1" <<'PY'
import re, sys
try:
    lines = open(sys.argv[1]).read().splitlines()
except Exception:
    sys.exit(0)
sec = sub = subsub = ""
found = []
for line in lines:
    if not line.strip() or line.lstrip().startswith("#"):
        continue
    indent = len(line) - len(line.lstrip())
    m = re.match(r"\s*([A-Za-z0-9_.-]+):\s*(.*?)\s*(?:#.*)?$", line)
    if not m:
        continue
    key, value = m.group(1), m.group(2).strip()
    if indent == 0:
        sec, sub, subsub = key, "", ""
        continue
    if key == "access" and value:
        if sec == "agent" and sub == "crafts" and subsub:
            found.append("agent.crafts.%s.access" % subsub)
        elif sec == "model_grades" and sub:
            found.append("model_grades.%s.access" % sub)
        continue
    if indent <= 2:
        sub, subsub = key, ""
        if sec == "agent" and key in ("auth", "oauth_token") and value:
            found.append("agent." + key)
        continue
    if indent <= 4:
        subsub = key
        continue
print("\n".join(found))
PY
}

# canonical_of CHECKOUT prints the checkout's repository as `owner/name`, read
# from its own `origin` and folded the way a slug's readable part folds one
# (ASCII lower case, a trailing `.git` off, the scp form's host off). It is what
# the rows that match a record to a checkout match on.
canonical_of() { # canonical_of <checkout>
  local tmp="$SCRATCH/origin.txt"
  git -C "$1" remote get-url origin >"$tmp" 2>/dev/null || : >"$tmp"
  python3 - "$tmp" <<'PY'
import re, sys
raw = open(sys.argv[1]).read().strip()
if not raw:
    sys.exit(0)
raw = raw.split("://")[-1]
raw = raw.split("@")[-1]
head = raw.split("/")[0]
if ":" in head:
    raw = raw.split(":", 1)[1]
parts = [p for p in raw.split("/") if p]
if len(parts) < 2:
    sys.exit(0)
repo = re.sub(r"\.git$", "", "/".join(parts[-2:]))
print("".join(c.lower() if "A" <= c <= "Z" else c for c in repo))
PY
}

# local_rtdb_url INSTALLATION prints the Realtime Database URL out of the
# installation's OWN local project config (`budget.rtdb_url`), or the empty
# string. `fugaro doctor --json` is not a source: its document carries the
# installation's project block and no database address at all, so a check that
# read one out of it would stop at its premise on every run that was not given
# --rtdb-url.
local_rtdb_url() { # local_rtdb_url <installation>
  local cfg=${CHECK811_CONFIG:-${XDG_CONFIG_HOME:-${HOME:-}/.config}/fugaro/projects/$1.yaml}
  [ -r "$cfg" ] || return 0
  sed -n 's/^[[:space:]]*rtdb_url:[[:space:]]*//p' "$cfg" | tr -d "\"'" | tr -d '[:space:]' | head -1
}

# record_read NODE OUT fetches one node of the installation's Realtime Database
# over its REST API, with the owner's own access token. The token is written to a
# 0600 curl config file rather than passed as an argument, and the node is read
# as its own JSON document.
record_read() { # record_read <node> <out>
  local node=$1 out=$2 rc=0
  if [ ! -s "$AUTH_CONF" ]; then
    if ! gcloud auth print-access-token >"$SCRATCH/access-token" 2>"$SCRATCH/access-token.err"; then
      printf "the owner's access token could not be read: %s" "$(head -c 300 "$SCRATCH/access-token.err" 2>/dev/null)" >"$out.unread"
      return 1
    fi
    python3 - "$SCRATCH/access-token" "$AUTH_CONF" <<'PY'
import os, sys
tok = open(sys.argv[1]).read().strip()
fd = os.open(sys.argv[2], os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
with os.fdopen(fd, "w") as fh:
    fh.write('header = "Authorization: Bearer %s"\n' % tok)
os.chmod(sys.argv[2], 0o600)
PY
    rm -f "$SCRATCH/access-token"
  fi
  curl -sS --max-time "${CHECK811_CURL_SECONDS:-60}" --config "$AUTH_CONF" \
    "${RTDB_URL%/}/${node}.json" -o "$out" 2>"$out.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    printf 'the database node %s could not be read: curl exited %s and printed: %s' \
      "$node" "$rc" "$(head -c 300 "$out.err" 2>/dev/null)" >"$out.unread"
    return 1
  fi
  return 0
}

record_unread() { cat "$1.unread" 2>/dev/null || true; }

# record_match NODEFILE NODE CANONICAL prints `<slug>|<json>` for the record
# under NODE (`repoauth` for the records the release behind writes, `repoaccess`
# for the ones release 1 writes) whose key is that repository's slug, or nothing.
# The records are the members of that node of the database's creds node
# (internal/creds/repoauth.go's path is creds/repoauth/<slug>), which is why the
# node is named here rather than assumed. A slug is its readable part (the
# canonical path with every run of characters that are not a-z0-9 turned into
# `-`) then `-` and sixteen hex of a hash (task.Slug), so the readable part is
# what a checkout can be matched on without this check recomputing the hash.
record_match() { # record_match <nodefile> <node> <canonical>
  python3 - "$1" "$2" "$3" <<'PY'
import json, re, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
node = doc.get(sys.argv[2]) if isinstance(doc, dict) else None
if not isinstance(node, dict):
    sys.exit(0)
readable = re.sub(r"[^a-z0-9]+", "-", sys.argv[3]).strip("-")
if not readable:
    sys.exit(0)
for slug in sorted(node):
    if re.sub(r"-[0-9a-f]{16}$", "", slug) == readable:
        print("%s|%s" % (slug, json.dumps(node[slug])))
PY
}

# old_record_auth FILE prints the `auth` an old record (creds/repoauth/<slug>,
# internal/creds/repoauth.go's closed shape: version, auth, accounts) names, or
# nothing. It is what tells the check whether the checkout it was pointed at is
# the repository on a subscription this check is about.
old_record_auth() { # old_record_auth <json>
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
if isinstance(doc, dict) and isinstance(doc.get("auth"), str):
    print(doc["auth"])
PY
}

# record_names FILE prints the names a JSON document carries, one per line, plus
# `version=<n>`. It is how a row reads what a record NAMES: the access record's
# own shape is contract C1's (d) and this check was written before that contract,
# so a row that has to say whether a record names a subscription reads the names
# in it rather than guessing at its members.
record_names() { # record_names <file>
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
def walk(node):
    if isinstance(node, dict):
        for key, value in node.items():
            if key == "version" and isinstance(value, int):
                print("version=%d" % value)
                continue
            walk(value)
    elif isinstance(node, list):
        for value in node:
            walk(value)
    elif isinstance(node, str) and node:
        print(node)
walk(doc)
PY
}

# stage_credentials FILE prints `<stages=N>` and then one line per stage of a run
# record: `stage|<name>|<credential>|<served_by>`. It is how the `token` row
# reads what served the run: a stage's `served_by` and `credential` name a
# credential the JOB ITSELF holds and are absent where the service serves the
# stage (CM10, F25), so a subscription stage the service serves carries neither
# — and a `oauth` in either is the job still holding the token. It exits 1 when
# the file is no JSON at all, which the caller reports rather than reading as a
# record with no stages.
stage_credentials() { # stage_credentials <file>
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
stages = doc.get("stages") if isinstance(doc, dict) else None
if not isinstance(stages, list):
    print("stages=none")
    sys.exit(0)
print("stages=%d" % len(stages))
for i, s in enumerate(stages):
    if not isinstance(s, dict):
        continue
    name = s.get("name") or s.get("stage") or ("#%d" % i)
    print("stage|%s|%s|%s" % (name, s.get("credential") or "", s.get("served_by") or ""))
PY
}

# stage_subscription_holders FILE prints the name of every stage of a run record
# whose `credential` or `served_by` is `oauth` — a subscription credential the
# JOB itself holds, which is the thing CM9 and CM28 take out of the agent's
# environment. A `served_by` of another class (a provider route's key) is not
# this clause's subject and is not printed.
stage_subscription_holders() { # stage_subscription_holders <file>
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
stages = doc.get("stages") if isinstance(doc, dict) else None
if not isinstance(stages, list):
    sys.exit(0)
for i, s in enumerate(stages):
    if not isinstance(s, dict):
        continue
    if s.get("credential") == "oauth" or s.get("served_by") == "oauth":
        print(s.get("name") or s.get("stage") or ("#%d" % i))
PY
}

# job_env_names FILE prints one line per variable and per secret reference in a
# `gcloud run jobs describe --format=json` answer: `env|<name>` for a container
# environment variable, `secret|<id>` for the secret a variable is read from and
# `volume|<name>` for a secret mounted as a volume. NO VALUE IS PRINTED, ever:
# the reader collects names only, so a document that carries a plain value (a
# job's own FUGARO_PROJECT and the like) prints the variable's name and not it.
# It finds the containers wherever the answer nests them — the v1 job shape
# `spec.template.spec.template.spec` and the API's other shapes — rather than
# pinning one spelling. It exits 1 when the file is no JSON at all.
job_env_names() { # job_env_names <file>
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
seen = set()
def containers(node):
    if isinstance(node, dict):
        for key, value in node.items():
            if key == "containers" and isinstance(value, list):
                for c in value:
                    if isinstance(c, dict):
                        yield c
            for got in containers(value):
                yield got
    elif isinstance(node, list):
        for value in node:
            for got in containers(value):
                yield got
def volumes(node):
    if isinstance(node, dict):
        for key, value in node.items():
            if key == "volumes" and isinstance(value, list):
                for v in value:
                    if isinstance(v, dict):
                        yield v
            for got in volumes(value):
                yield got
    elif isinstance(node, list):
        for value in node:
            for got in volumes(value):
                yield got
for c in containers(doc):
    for e in (c.get("env") or []):
        if not isinstance(e, dict):
            continue
        name = e.get("name") or ""
        if name:
            seen.add("env|" + str(name))
        src = e.get("valueSource") or e.get("value_source") or {}
        if not isinstance(src, dict):
            src = {}
        ref = src.get("secretKeyRef") or src.get("secret_key_ref") or {}
        if isinstance(ref, dict) and ref.get("secret"):
            seen.add("secret|" + str(ref["secret"]))
for v in volumes(doc):
    sec = v.get("secret") or {}
    if isinstance(sec, dict) and sec.get("secretName"):
        seen.add("volume|" + str(sec["secretName"]))
for line in sorted(seen):
    print(line)
PY
}

launch_run() { # launch_run <checkout> <outfile>
  (cd "$1" && fugaro run --task-file "$SCRATCH/task.md" --json) >"$2" 2>"$2.err"
}

# row_of RUNSFILE RUNID prints `<outcome>|<ref>` for one run of a listing, or
# nothing when the listing carries no row for it. The outcome is empty while the
# run is going, which is what tells a live row from an ended one.
row_of() { # row_of <runsfile> <run-id>
  python3 - "$1" "$2" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
rows = doc if isinstance(doc, list) else (doc.get("runs") or [])
for r in rows:
    if not isinstance(r, dict) or not sys.argv[2]:
        continue
    ref = r.get("run", "")
    if r.get("run_id") == sys.argv[2] or ref.endswith("/" + sys.argv[2]):
        print("%s|%s" % (r.get("outcome", ""), ref))
        break
PY
}

# live_rows RUNSFILE prints the number of rows of a listing that are still going
# (a row with no outcome yet). It is how the row that runs the second update
# tells "no run of this repository is going" from "one is", which is the
# condition CM31 deletes an old record under.
live_rows() { # live_rows <runsfile>
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
rows = doc if isinstance(doc, list) else (doc.get("runs") or [])
n = 0
for r in rows:
    if isinstance(r, dict) and not r.get("outcome"):
        n += 1
print(n)
PY
}

# run_state OUT prints one of `live`, `ended|<outcome>`, `absent` (the listing
# answered and carries no row for the run yet) or `unanswered` (the listing
# itself never answered — a read that did not answer is not a run that has not
# started). It retries the listing a few times, because a run launched a moment
# ago may not be listed yet.
run_state() { # run_state <out>
  local out=$1 i=0 row answered=no
  while [ "$i" -lt "${CHECK811_LIVE_TRIES:-5}" ]; do
    if (cd "$SUBSCRIPTION" && fugaro runs ls --json) >"$out" 2>"$out.err"; then
      answered=yes
      row=$(row_of "$out" "$RUN_ID")
      if [ -n "$row" ]; then
        if [ -n "${row%%|*}" ]; then
          printf 'ended|%s\n' "${row%%|*}"
        else
          printf 'live\n'
        fi
        return 0
      fi
    fi
    i=$((i + 1))
    sleep "${CHECK811_POLL_SECONDS:-20}"
  done
  if [ "$answered" = yes ]; then printf 'absent\n'; else printf 'unanswered\n'; fi
  return 0
}

# poll_row OUT waits for the run's row to be terminal and prints
# `<outcome>|<ref>`. It returns 1 when the check's own wait is over with the row
# still not terminal, and 2 when the listing itself never answered.
poll_row() { # poll_row <out>
  local out=$1 i=0 answered=no row=""
  while [ "$i" -lt "${CHECK811_POLLS:-90}" ]; do
    if (cd "$SUBSCRIPTION" && fugaro runs ls --json) >"$out" 2>"$out.err"; then
      answered=yes
      row=$(row_of "$out" "$RUN_ID")
      if [ -n "$row" ] && [ -n "${row%%|*}" ]; then
        printf '%s\n' "$row"
        return 0
      fi
    fi
    i=$((i + 1))
    sleep "${CHECK811_POLL_SECONDS:-20}"
  done
  [ "$answered" = yes ] || return 2
  return 1
}

run_record_read() { # run_record_read <ref> <out>
  gcloud storage cat "gs://fugaro-runs-${GCP_PROJECT}/runs/$1/result.json" --project="$GCP_PROJECT" \
    >"$2" 2>"$2.err"
}

# log_lines FILE prints one line per log entry, `|`-separated:
# `<msg>|<status>|<repository>|<run>|<credential>|<purpose>|<type>`.
# The fields are the service's own slog attributes (audit.go's line and
# refusallog.go's), and `msg` is what tells the two apart: `credsvc: audit` is
# the record of what the service did with a credential. Neither carries a value.
log_lines() { # log_lines <file>
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
if not isinstance(doc, list):
    sys.exit(0)
for entry in doc:
    if not isinstance(entry, dict):
        continue
    p = entry.get("jsonPayload") or {}
    print("|".join(str(p.get(k, "")) for k in
                   ("msg", "status", "repository", "run", "credential", "purpose", "type")))
PY
}

# audit_read RUNID OUT [PATTERN] retries `gcloud logging read` over the
# credentials service's own lines for one run until the read answers AND the
# lines match PATTERN — Cloud Logging ingests a container's lines after the
# request, so a read that answered with nothing yet is not the service having
# written nothing. It returns 1 only when the read never answered, and the reason
# is written beside the file it is about.
audit_read() { # audit_read <run-id> <out> [pattern]
  local rid=$1 out=$2 pattern=${3:-} waited=0 rc=0 filter
  filter='resource.type="cloud_run_revision" resource.labels.service_name="'"$SERVICE"'" jsonPayload.run="'"$rid"'"'
  while :; do
    rc=0
    gcloud logging read "$filter" --project="$GCP_PROJECT" --freshness="${CHECK811_LOG_AGE:-90m}" \
      --limit="${CHECK811_LOG_LIMIT:-200}" --order=asc --format=json >"$out" 2>"$SCRATCH/log.err" || rc=$?
    if [ "$rc" -eq 0 ] && log_lines "$out" | grep -q .; then
      if [ -z "$pattern" ] || log_lines "$out" | grep -q -- "$pattern"; then return 0; fi
    fi
    if [ "$waited" -ge "${CHECK811_LOG_WAIT_SECONDS:-120}" ]; then
      if [ "$rc" -ne 0 ]; then
        printf "the service's own lines for the run (%s) could not be read: 'gcloud logging read' exited %s and printed: %s. A read that did not answer is NOT the service having written no line. Read the log yourself (the filter is %s) and record it in the register's section" \
          "$rid" "$rc" "$(head -c 300 "$SCRATCH/log.err" 2>/dev/null)" "$filter" >"$out.unread"
        return 1
      fi
      return 0
    fi
    sleep "${CHECK811_LOG_POLL_SECONDS:-10}"
    waited=$((waited + ${CHECK811_LOG_POLL_SECONDS:-10}))
  done
}

audit_unread() { cat "$1.unread" 2>/dev/null || true; }

# ------------------------------------------------------------- the check's rows

# step_premise reads the subscription checkout's committed file and the
# installation's own records, and stops the check when what it finds is not the
# state Check 811 is about. A premise that does not hold is a STOP (exit 2) and
# never a finding: the check is about an installation release 1 reached and
# release 2 is reaching.
step_premise() {
  say "reading the subscription checkout's committed file and the installation's own records (premise)"
  local keys
  CANONICAL=$(canonical_of "$SUBSCRIPTION" || true)
  if [ -z "$CANONICAL" ]; then
    fact premise FAIL "${SUBSCRIPTION} names no origin, so its repository and its records cannot be matched: --subscription is the checkout of a repository of this installation, cloned from its own remote"
    stop "the subscription checkout names no origin"
  fi
  keys=$(committed_keys "$SUBSCRIPTION" || true)
  if [ -n "$keys" ]; then
    fact premise FAIL "the committed fugaro.yaml of ${CANONICAL} still writes a retired key (${keys//$'\n'/, }), and release 2 refuses a launch from the default branch on such a file (CM29): this check launches its run in that checkout, so every row below would be a row about a launch that cannot happen. Commit the removal 'fugaro update' offers (the register's section has the step), or point --subscription at a repository whose file no longer writes one"
    stop "the subscription checkout's committed file still writes a retired key"
  fi
  keys=$(working_keys "$SUBSCRIPTION" || true)
  if [ -n "$keys" ]; then
    fact premise FAIL "the working fugaro.yaml of ${CANONICAL} still writes a retired key (${keys//$'\n'/, }), and release 2 rejects a file that writes one — a launch in this checkout reads it (CM29) — so every row below would be a row about a launch that cannot happen. Commit the removal 'fugaro update' offers (the register's section has the step), or point --subscription at a checkout whose file no longer writes one"
    stop "the subscription checkout's working file still writes a retired key"
  fi
  if [ -z "$RTDB_URL" ]; then
    RTDB_URL=$(local_rtdb_url "$INSTALLATION" || true)
    if [ -z "$RTDB_URL" ]; then
      fact premise UNVERIFIED "the installation's own local project config names no Realtime Database URL (${CHECK811_CONFIG:-${XDG_CONFIG_HOME:-${HOME:-}/.config}/fugaro/projects/${INSTALLATION}.yaml}, key budget.rtdb_url), so the installation's records cannot be read and NOTHING below was measured. Give the URL with --rtdb-url and run the check again"
      stop "no database URL could be read — nothing is launched on an installation nobody read"
    fi
  fi
  if ! record_read creds "$SCRATCH/creds-before.json"; then
    fact premise UNVERIFIED "the database's own creds node could not be read, so which records this installation carries is NOT measured: $(record_unread "$SCRATCH/creds-before.json"). Read ${RTDB_URL%/}/creds.json yourself and run the check again"
    stop "the installation's records could not be read — nothing is launched on an installation nobody read"
  fi
  local access old auth
  access=$(record_match "$SCRATCH/creds-before.json" repoaccess "$CANONICAL" || true)
  if [ -z "$access" ]; then
    fact premise FAIL "the database's creds node carries no access record (creds/repoaccess/<slug>) for ${CANONICAL}: release 1's 'fugaro update --repo' writes it, and a repository the credentials service does not serve is a repository whose run still holds its token by design (D3067), so the first clause of Check 811 is about nothing here. Run 'fugaro update --repo' in ${SUBSCRIPTION} on release 1 first, or point --subscription at a repository it reached"
    stop "the installation carries no access record for the subscription repository"
  fi
  old=$(record_match "$SCRATCH/creds-before.json" repoauth "$CANONICAL" || true)
  OLD_BEFORE=$old
  OLD_SLUG=${old%%|*}
  if [ -n "$old" ]; then
    printf '%s' "${old#*|}" >"$SCRATCH/old-record.json"
    auth=$(old_record_auth "$SCRATCH/old-record.json" || true)
    if [ "$auth" != oauth ]; then
      fact premise FAIL "the old record of ${CANONICAL} names 'auth: ${auth:-<none>}', not 'oauth': --subscription has to be the repository on a subscription this check is about, and every row below — the token its agent does not hold, the record kept while its run is in flight — would be a row about another repository. Point it at the repository whose runs go out on a subscription"
      stop "the subscription checkout's old record does not name a subscription"
    fi
  else
    printf '%s' "${access#*|}" >"$SCRATCH/access-record.json"
    record_names "$SCRATCH/access-record.json" >"$SCRATCH/access-names.txt" 2>/dev/null || true
    if ! grep -qiE 'oauth|subscription' "$SCRATCH/access-names.txt"; then
      fact premise FAIL "the database's creds node carries no old record (creds/repoauth/<slug>) for ${CANONICAL}, and its access record names nothing this check reads as a subscription ($(tr '\n' ' ' <"$SCRATCH/access-names.txt" 2>/dev/null)): --subscription has to be the repository on a subscription this check is about. Note that a repository whose old record is already gone is one release 2's update has reached — the two record rows below then read UNVERIFIED, which is the honest answer rather than a pass"
      stop "the subscription repository's records do not name a subscription"
    fi
  fi
  if [ -n "$old" ]; then
    fact premise PASS "${CANONICAL} is the repository on a subscription this check is about: its committed file writes no retired key, the database's creds node carries its access record (creds/repoaccess) and an old record (creds/repoauth/${OLD_SLUG}) whose auth is oauth — the state release 1 left"
  else
    fact premise PASS "${CANONICAL} is the repository on a subscription this check is about: its committed file writes no retired key and the database's creds node carries its access record (creds/repoaccess), whose names carry a subscription. Its OLD record is already gone, so the two record rows below will read UNVERIFIED and say how to measure them"
  fi
}

step_launch() {
  say "launching the run on the subscription repository (paid)"
  cat >"$SCRATCH/task.md" <<'TASK'
# Task: check811 — the run on a subscription, with no token of its own

Change nothing that matters: add one line to README.md naming this run as Check
811's subscription run, and open a pull request.
TASK
  if ! launch_run "$SUBSCRIPTION" "$SCRATCH/run.json"; then
    fact launch FAIL "the run on the subscription repository could not be launched: $(head -c 400 "$SCRATCH/run.json.err" 2>/dev/null). Every row below reads THIS run — the record kept across the update that reaches the repository, and the token its agent does not hold — so the two record rows are not measured without it. A launch REFUSED here is one of the two refusals release 2 makes on purpose — a subscription the credentials service does not serve, or a job image older than release 1 (CM28) — and either message names the command that fixes it; read it and run the check again"
    return 0
  fi
  RUN_ID=$(jget "$SCRATCH/run.json" run_id)
  [ -n "$RUN_ID" ] || RUN_ID=$(jget "$SCRATCH/run.json" id)
  RUN_REF=$(jget "$SCRATCH/run.json" run)
  RUN_SLUG=${RUN_REF%%/*}
  if [ -z "$RUN_ID" ] || [ -z "$RUN_SLUG" ]; then
    fact launch UNVERIFIED "a run was launched but its own answer carries no run id or no ref (slug/run-id) this check can read, so the rows that follow it have nothing to follow: $(head -c 200 "$SCRATCH/run.json" 2>/dev/null)"
    return 0
  fi
  fact launch PASS "the run ${RUN_ID} of ${RUN_SLUG} was launched on the subscription repository before either update: the rows below read it"
}

# step_old_record runs the update that reaches the repository WHILE the run is
# going. CM31's clause is what it measures: an update deletes a repository's old
# record only where that repository has no run in flight, and otherwise leaves it
# and says so in one line. The run's own row is read first, because a run that
# had already ended would make the update's deletion the right answer rather than
# a finding — and the row says which of the two it measured.
step_old_record() {
  if [ -z "$RUN_ID" ]; then
    fact old-record UNVERIFIED "there is no run to be in flight across the update, so whether an update keeps a repository's old record while one is going is NOT measured: the launch row above says why"
    return 0
  fi
  say "reading the run's own row, then running 'fugaro update --repo' with it in flight"
  RUN_STATE=$(run_state "$SCRATCH/runs-live.json")
  if [ -z "$OLD_BEFORE" ]; then
    if ! (cd "$SUBSCRIPTION" && fugaro update --repo --project "$INSTALLATION") >"$SCRATCH/update-1.out" 2>&1; then
      fact old-record UNVERIFIED "the installation's creds node carries no old record for ${CANONICAL} to keep, and the update run for it did not exit 0 either: $(head -c 400 "$SCRATCH/update-1.out" 2>/dev/null)"
      return 0
    fi
    local wrote=""
    if record_read creds "$SCRATCH/creds-mid.json"; then
      wrote=$(record_match "$SCRATCH/creds-mid.json" repoauth "$CANONICAL" || true)
    fi
    if [ -n "$wrote" ]; then
      fact old-record FAIL "'fugaro update --repo' WROTE an old record (creds/repoauth/${wrote%%|*}) for ${CANONICAL}, which the installation did not have before it: release 2's update no longer writes that record (CM31), and one written now is a record the service goes on narrowing that repository's calls by"
      return 0
    fi
    fact old-record UNVERIFIED "the installation's creds node carries no old record (creds/repoauth/<slug>) for ${CANONICAL}, so whether an update KEEPS one while a run is in flight is NOT measured here: the update that reached this installation already deleted it (CM31 deletes a repository's old record where it has no run in flight). To measure it, run Check 811 before your own 'fugaro update', or on a repository no update has reached yet — the 'record-gone' row below reads the other half"
    return 0
  fi
  if [ "$RUN_STATE" != live ]; then
    local state_words
    case "$RUN_STATE" in
    ended*)
      state_words="the run ${RUN_ID} had already ended ('${RUN_STATE#ended|}') when the update was run"
      ;;
    absent)
      state_words="the listing of runs carried no row for the run ${RUN_ID} when the update was run"
      ;;
    *)
      state_words="the listing of runs never answered, so whether the run ${RUN_ID} was going when the update was run is NOT measured"
      ;;
    esac
    fact old-record UNVERIFIED "${state_words}, so an update that deleted the old record did the right thing by its own rule (CM31), and whether a record is kept WHILE a run is in flight is NOT measured here. The 'record-gone' row below still reads the deletion"
    return 0
  fi
  if ! (cd "$SUBSCRIPTION" && fugaro update --repo --project "$INSTALLATION") >"$SCRATCH/update-1.out" 2>&1; then
    fact old-record FAIL "the update that has to keep the old record did not exit 0: $(head -c 500 "$SCRATCH/update-1.out" 2>/dev/null). The whole upgrade is that one command (CLAUDE.md's HARD RULE), and an update that did not finish is not the migration this row is about"
    return 0
  fi
  if ! record_read creds "$SCRATCH/creds-mid.json"; then
    fact old-record UNVERIFIED "the update exited 0, but the database's creds node could not be read afterwards, so whether the old record survived it is NOT measured: $(record_unread "$SCRATCH/creds-mid.json"). Read ${RTDB_URL%/}/creds.json yourself and record it in the register's section"
    return 0
  fi
  local after line quoted=""
  after=$(record_match "$SCRATCH/creds-mid.json" repoauth "$CANONICAL" || true)
  line=$(grep -iE -- "repoauth|${OLD_SLUG}" "$SCRATCH/update-1.out" | head -1 || true)
  if [ -n "$line" ]; then
    quoted=", and the update's own output carries the line '${line}'"
  fi
  if [ -n "$after" ]; then
    fact old-record PASS "the old record ${OLD_SLUG} is still there after 'fugaro update --repo' was run while the run ${RUN_ID} was going (its own row was live when the update started): an update keeps a repository's old record while a run of it is in flight (CM31)${quoted}"
  else
    fact old-record FAIL "the old record ${OLD_SLUG} is GONE after 'fugaro update --repo' was run while the run ${RUN_ID} was going: a run that bootstrapped on the release behind holds that record — it is what the service narrows its calls by (CM31, F42) — and an update that deletes it takes the narrowing away from the run it is about (docs/design/update-in-flight.md). The update's own output was: $(head -c 300 "$SCRATCH/update-1.out" 2>/dev/null)"
  fi
}

# step_token reads what the run's agent had to go out on, from three places: the
# job's own document (the token would be a container variable), the service's own
# log (the run must have gone through its gateway), and the run's own record (no
# stage may name a credential the job itself holds, CM10). The job's own document
# is read FIRST and its finding kept, so a job that still mounts the token is
# reported as the FAIL it is even where the run itself cannot be followed.
step_token() {
  if [ -z "$RUN_ID" ]; then
    fact token UNVERIFIED "there is no run to read, so whether a run on a subscription has a token in its agent's environment is NOT measured: the launch row above says why"
    return 0
  fi
  say "reading the job ${JOB}'s own environment, then following the run ${RUN_ID} to its end"
  local jobstate=clean jobdetail="" jn="" carried="" row="" rc=0 outcome="" served=0 held="" stages="" nstages="" good=""
  if ! gcloud run jobs describe "$JOB" --project="$GCP_PROJECT" --region="$REGION" --format=json \
    >"$SCRATCH/job.json" 2>"$SCRATCH/job.json.err"; then
    jobstate=unreadable
    jobdetail="the job ${JOB}'s own document could not be read: $(head -c 300 "$SCRATCH/job.json.err" 2>/dev/null)"
  elif ! jn=$(job_env_names "$SCRATCH/job.json"); then
    jobstate=unreadable
    jobdetail="the job ${JOB}'s own document is no JSON this check can read"
  elif [ -z "$jn" ]; then
    jobstate=unreadable
    jobdetail="the job ${JOB}'s own document names no container environment at all, so whether the token is in it cannot be read from it"
  elif printf '%s\n' "$jn" | grep -qx 'env|CLAUDE_CODE_OAUTH_TOKEN'; then
    jobstate=token
  else
    jobdetail="the job ${JOB} carries no CLAUDE_CODE_OAUTH_TOKEN in its container's environment"
    if printf '%s\n' "$jn" | grep -qx 'env|ANTHROPIC_API_KEY'; then
      carried=", though it does carry ANTHROPIC_API_KEY, which this row does not judge"
    fi
  fi
  row=$(poll_row "$SCRATCH/runs-end.json") || rc=$?
  if [ "$rc" -eq 0 ]; then
    outcome=${row%%|*}
    RUN_ENDED=yes
    [ -n "${row#*|}" ] && RUN_REF=${row#*|}
  fi
  if [ "$jobstate" = token ]; then
    if [ "$rc" -eq 0 ]; then
      fact token FAIL "the job ${JOB} carries CLAUDE_CODE_OAUTH_TOKEN in its container's environment, and the run ${RUN_ID} it serves ended '${outcome}': a run on a subscription must have no token in its agent's environment (CM9, CM28), and the mount that variable comes from is the one the flip takes away"
    else
      fact token FAIL "the job ${JOB} carries CLAUDE_CODE_OAUTH_TOKEN in its container's environment: a run on a subscription must have no token in its agent's environment (CM9, CM28), and the mount that variable comes from is the one the flip takes away"
    fi
    return 0
  fi
  if [ "$jobstate" = unreadable ]; then
    fact token UNVERIFIED "${jobdetail}, so whether the subscription token is in its container's environment is NOT measured. Read it yourself ('gcloud run jobs describe ${JOB} --region ${REGION}') and record it in the register's section"
    return 0
  fi
  if [ "$rc" -ne 0 ]; then
    if [ "$rc" -eq 2 ]; then
      fact token UNVERIFIED "${jobdetail}, but the listing of runs never answered, so the run ${RUN_ID} was not followed to its end and the service's lines for it are not read: $(head -c 300 "$SCRATCH/runs-end.json.err" 2>/dev/null). Read it yourself ('fugaro runs ls') and record the outcome in the register's section"
    else
      fact token UNVERIFIED "${jobdetail}, but the run ${RUN_ID} is still not terminal after the check's own wait, so the service's lines for it and its own record are NOT read: what its agent had to go out on is only half measured. Read it yourself ('fugaro runs ls', then the run's own record) and record it in the register's section"
    fi
    return 0
  fi
  good="${jobdetail}"
  if ! audit_read "$RUN_ID" "$SCRATCH/log.json" '^credsvc: audit|ok|'; then
    fact token UNVERIFIED "${good}, and the run ${RUN_ID} ended '${outcome}', but the service's own lines for it could not be read, so whether its calls went through its gateway is NOT measured: $(audit_unread "$SCRATCH/log.json")"
    return 0
  fi
  served=$(log_lines "$SCRATCH/log.json" | grep -c '^credsvc: audit|ok|' || true)
  if [ "$served" -eq 0 ]; then
    fact token UNVERIFIED "${good}, and the run ${RUN_ID} ended '${outcome}', but the service's own lines for it carry no served line (a 'credsvc: audit' line with status ok): whether the run went through its gateway is NOT measured, and a run whose calls nothing served is not the run this clause is about"
    return 0
  fi
  if ! run_record_read "$RUN_REF" "$SCRATCH/record.json"; then
    fact token UNVERIFIED "${good}, the service served ${served} of the run ${RUN_ID}'s calls through its gateway, but the run's own record could not be read out of the runs bucket (gs://fugaro-runs-${GCP_PROJECT}/runs/${RUN_REF}/result.json): $(head -c 300 "$SCRATCH/record.json.err" 2>/dev/null)"
    return 0
  fi
  if ! stages=$(stage_credentials "$SCRATCH/record.json"); then
    fact token UNVERIFIED "${good}, the service served ${served} of the run ${RUN_ID}'s calls through its gateway, but the run's own record is no document this check can read, so what it names for its stages is NOT measured"
    return 0
  fi
  nstages=$(printf '%s\n' "$stages" | sed -n 's/^stages=//p')
  if [ -z "$nstages" ] || [ "$nstages" = none ] || [ "$nstages" = 0 ]; then
    fact token UNVERIFIED "${good}, the service served ${served} of the run ${RUN_ID}'s calls through its gateway, but its record names no stage (stages=${nstages:-absent}), so whether any stage was served by a credential the job holds is NOT measured"
    return 0
  fi
  held=$(stage_subscription_holders "$SCRATCH/record.json" | tr '\n' ',' || true)
  held=${held%,}
  if [ -n "$held" ]; then
    fact token FAIL "${good}, but the run's own record names a subscription credential (oauth) for the stage(s) ${held}: a stage's credential/served_by names a credential the JOB itself holds (CM10), so the run went out on a token of its own and not on the service alone. The run ${RUN_ID} ended '${outcome}'"
    return 0
  fi
  fact token PASS "${good}; the service served ${served} of the run ${RUN_ID}'s calls through its gateway; and its own record names no subscription credential for any of its ${nstages} stages, so its agent had nothing of its own to go out on (CM9, CM28)${carried}. The run ended '${outcome}'"
}

# step_record_gone runs the same update again, now that no run of the repository
# is going, and reads the record. It refuses to read anything into an update run
# while a run is still going — its own or another's — because such an update
# would rightly keep the record.
step_record_gone() {
  if [ -z "$RUN_ID" ]; then
    fact record-gone UNVERIFIED "there is no run to have ended, so whether an update deletes a repository's old record once none of its runs is going is NOT measured: the launch row above says why"
    return 0
  fi
  if [ -z "$OLD_BEFORE" ]; then
    fact record-gone UNVERIFIED "the installation's creds node carries no old record for ${CANONICAL} to delete, so the other half of the clause is NOT measured here: the update that reached this installation already deleted it (CM31). Run Check 811 before your own 'fugaro update', or on a repository no update has reached yet"
    return 0
  fi
  if [ "$RUN_ENDED" != yes ]; then
    fact record-gone UNVERIFIED "the run ${RUN_ID} had not ended when this row was reached, so an update run now would still find a run of the repository in flight and would rightly keep the record: whether the old record goes once none of them is going is NOT measured. Read it yourself ('fugaro update --repo' in ${SUBSCRIPTION}, then ${RTDB_URL%/}/creds.json) and record it in the register's section"
    return 0
  fi
  say "reading the repository's own rows, then running 'fugaro update --repo' again with none going"
  local live=""
  if (cd "$SUBSCRIPTION" && fugaro runs ls --json) >"$SCRATCH/runs-after.json" 2>"$SCRATCH/runs-after.json.err"; then
    live=$(live_rows "$SCRATCH/runs-after.json" 2>/dev/null || true)
  fi
  if [ "$live" != 0 ]; then
    fact record-gone UNVERIFIED "the run ${RUN_ID} has ended, but this row could not establish that no OTHER run of ${CANONICAL} is going (${live:-the listing of runs could not be read}: $(head -c 200 "$SCRATCH/runs-after.json.err" 2>/dev/null)), and an update run with one going would rightly keep the record: whether it goes once none is going is NOT measured. Read it yourself and record it in the register's section"
    return 0
  fi
  if ! (cd "$SUBSCRIPTION" && fugaro update --repo --project "$INSTALLATION") >"$SCRATCH/update-2.out" 2>&1; then
    fact record-gone FAIL "the update that has to delete the old record did not exit 0: $(head -c 500 "$SCRATCH/update-2.out" 2>/dev/null). The whole upgrade is that one command (CLAUDE.md's HARD RULE), and an update that did not finish is not the deletion this row is about"
    return 0
  fi
  if ! record_read creds "$SCRATCH/creds-after.json"; then
    fact record-gone UNVERIFIED "the update exited 0, but the database's creds node could not be read afterwards, so whether the old record went is NOT measured: $(record_unread "$SCRATCH/creds-after.json"). Read ${RTDB_URL%/}/creds.json yourself and record it in the register's section"
    return 0
  fi
  local after line quoted=""
  after=$(record_match "$SCRATCH/creds-after.json" repoauth "$CANONICAL" || true)
  line=$(grep -i -- 'repoauth' "$SCRATCH/update-2.out" | head -1 || true)
  if [ -n "$line" ]; then
    quoted=", and the update's own output carries the line '${line}'"
  fi
  if [ -n "$after" ]; then
    fact record-gone FAIL "the old record ${after%%|*} is STILL there after 'fugaro update --repo' was run with no run of ${CANONICAL} going (the run ${RUN_ID} has ended and no other row of 'fugaro runs ls' is live): release 2's update deletes a repository's old record where none of its runs is in flight (CM31), and one left behind is a record the service goes on narrowing its calls by. The update's own output was: $(head -c 300 "$SCRATCH/update-2.out" 2>/dev/null)"
  else
    fact record-gone PASS "the old record ${OLD_SLUG} is GONE after 'fugaro update --repo' was run with no run of ${CANONICAL} going: an update deletes a repository's old record where none of its runs is in flight (CM31)${quoted}"
  fi
}

# step_retired_key reads `fugaro validate`'s own answer in the checkout the
# owner pointed --retired-key at. The check never decides what the file writes
# from its own scan: the scan is the premise (the file must write one, or this
# row would be about nothing), and the refusal is what the row measures.
step_retired_key() {
  if [ -z "$RETIRED_KEY" ]; then
    fact retired-key UNVERIFIED "no --retired-key was given, so whether a file that still writes agent.auth is rejected with the fix is NOT measured. Point it at a checkout whose fugaro.yaml still writes a retired key (agent.auth, agent.oauth_token, a craft's access or a grade's access) and run the check again"
    return 0
  fi
  say "running 'fugaro validate' in ${RETIRED_KEY} (retired-key)"
  local keys rc=0 line
  keys=$(retired_keys "$RETIRED_KEY/fugaro.yaml" || true)
  if [ -z "$keys" ]; then
    fact retired-key UNVERIFIED "${RETIRED_KEY}/fugaro.yaml writes no retired key this check can see (agent.auth, agent.oauth_token, agent.crafts.<craft>.access, model_grades.<grade>.access), so there is nothing for 'fugaro validate' to reject and the clause is NOT measured here. Point --retired-key at a checkout whose file still writes one and run the check again"
    return 0
  fi
  (cd "$RETIRED_KEY" && fugaro validate) >"$SCRATCH/validate.out" 2>&1 || rc=$?
  line=$(head -1 "$SCRATCH/validate.out" 2>/dev/null || true)
  if [ "$rc" -eq 0 ]; then
    fact retired-key FAIL "${RETIRED_KEY}/fugaro.yaml still writes ${keys//$'\n'/, } and 'fugaro validate' ACCEPTED it (exit 0): release 2 rejects a repository's file that writes a retired key, with the fix (CM29), and a file that validates is a file a launch from the default branch will run on a key nothing reads any more"
    return 0
  fi
  local named="" key
  while IFS= read -r key; do
    [ -n "$key" ] || continue
    if grep -qF -- "$key" "$SCRATCH/validate.out" || grep -qF -- "${key##*.}" "$SCRATCH/validate.out"; then
      named="${named}${named:+, }${key}"
    fi
  done <<<"$keys"
  if [ -z "$named" ]; then
    fact retired-key UNVERIFIED "'fugaro validate' refused ${RETIRED_KEY}/fugaro.yaml (exit ${rc}) but its own output names none of the retired keys the file writes (${keys//$'\n'/, }), so whether this refusal is the retired key's is NOT measured: ${line}. Read it yourself and record it in the register's section"
    return 0
  fi
  if ! grep -qi -- 'update' "$SCRATCH/validate.out"; then
    fact retired-key FAIL "the file's retired key (${named}) is refused (exit ${rc}) but the refusal names no fix: release 2's refusal offers the removal 'fugaro update' writes as a diff (CM29), and an owner who is not told how to have it removed is stopped with no way forward. The refusal was: ${line}"
    return 0
  fi
  fact retired-key PASS "'fugaro validate' refused ${RETIRED_KEY}/fugaro.yaml (exit ${rc}), naming the retired key it writes (${named}) and the command that fixes it: the removal 'fugaro update' offers as a diff is the fix the refusal carries (CM29). Its first line: ${line}"
}

# step_old_image launches in the checkout the owner pointed --old-image at. The
# launch is the measurement: CM28's gate refuses a job image older than release 1
# and names the command that rebuilds it. A launch that is NOT refused is the
# finding, and the run it started is left to end on its own.
step_old_image() {
  if [ -z "$OLD_IMAGE" ]; then
    fact old-image UNVERIFIED "no --old-image was given, so whether a launch on an image older than release 1 is refused with the rebuild command is NOT measured. Point it at a checkout whose repository's job image predates release 1 and run the check again"
    return 0
  fi
  say "launching in ${OLD_IMAGE}, which must be REFUSED (old-image)"
  local rc=0 line
  (cd "$OLD_IMAGE" && fugaro run --task-file "$SCRATCH/task.md" --json) >"$SCRATCH/old-image.out" 2>&1 || rc=$?
  line=$(head -1 "$SCRATCH/old-image.out" 2>/dev/null || true)
  if [ "$rc" -eq 0 ]; then
    fact old-image FAIL "the launch in ${OLD_IMAGE} was NOT refused (exit 0): a run was launched on a job image older than release 1, which CM28's gate must refuse — a run whose runner reads a mounted credential the service no longer mounts would die at its first model call. The run it started is left to end on its own; cancel it if you do not want to pay for it. The launch's own output was: $(head -c 300 "$SCRATCH/old-image.out" 2>/dev/null)"
    return 0
  fi
  if ! grep -qE 'fugaro update|fugaro image build' "$SCRATCH/old-image.out"; then
    fact old-image UNVERIFIED "the launch in ${OLD_IMAGE} was refused (exit ${rc}), but its refusal names no command that rebuilds the image (neither 'fugaro update' nor 'fugaro image build'), so whether this refusal is the image gate's is NOT measured: ${line}. Read it yourself and record it in the register's section"
    return 0
  fi
  if ! grep -qE 'fugaro [0-9]+\.[0-9]+\.[0-9]+' "$SCRATCH/old-image.out"; then
    fact old-image UNVERIFIED "the launch in ${OLD_IMAGE} was refused (exit ${rc}) and the refusal names a command that rebuilds the image, but it names no fugaro release the image has to be at or past, so whether it is the release-1 gate is NOT measured: ${line}. Read it yourself and record it in the register's section"
    return 0
  fi
  fact old-image PASS "the launch in ${OLD_IMAGE} was refused (exit ${rc}) with the command that rebuilds the image and the release it needs (CM28), and no run was launched: ${line}"
}

step_premise
step_launch
step_old_record
step_token
step_record_gone
step_retired_key
step_old_image
REACHED_END=yes
