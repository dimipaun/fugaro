#!/usr/bin/env bash
# Check 810 — the auth retirement's FIRST release, watched across four
# repositories and a run in flight, as ONE script YOU run.
#
# `credentials-belong-to-models` takes a repository's auth mode away: a
# repository's runs stop saying how they go out, and the installation records,
# per repository, which credential its crafts resolve to. Release 1 writes that
# record (`creds/repoaccess/<slug>`, C1 (d)) and migrates every repository
# `fugaro update` is run for, while a repository's file still writes the old
# `agent.auth` — which the release reads as the same answer it always meant (C1
# (c)). Nothing offline can settle what that migration does to a real
# installation, and the plan's §8 says what to watch (CM22):
#
#   - `fugaro update` migrates all four repositories and says what each craft
#     resolves to;
#   - the run in flight across it finishes (U5);
#   - a new run of each of the four launches, goes through its gateway, and is
#     recorded with the same `model_basis` as before;
#   - the service's audit log holds one line per refusal provoked by a hand-built
#     call that declares a craft its record does not name (U5, O1).
#
# So the check does its own setup and reads, row by row:
#
#   premise     the installation is one this release has not reached: each of
#               the four checkouts' COMMITTED `fugaro.yaml` writes its own old
#               `agent.auth` (oauth, api-key, cascade, vertex), the database's
#               `creds` node carries the four old records (`creds/repoauth/
#               <slug>`) and no access record at all. A premise that does not
#               hold is a STOP (exit 2), not a finding: this check is about an
#               installation at the PREVIOUS release.
#   in-flight   a run of the subscription repository is launched BEFORE the
#               update, and its id and slug recorded.
#   update      `fugaro update` is run as the project owner, as the plan's
#               after-deploy steps name it: once for the installation (which
#               migrates the config and the published policy) and once for each
#               of the four repositories (`--repo` in its checkout). All five
#               must exit 0.
#   crafts      each of the four repositories' new record is read back from the
#               database: it names the credential its old auth means, names
#               `vertex` for the Vertex repository and never for the others, and
#               carries version 1.
#   says        the update's own output names each repository and a credential
#               its crafts resolve to.
#   in-flight-2 the run launched before the update is read until it ends: the
#               migration must not have killed it (U5).
#   oauth-run, key-run, cascade-run, vertex-run
#               a run of each repository, launched after the update: it ends,
#               the service's own log holds a served line for it (it went
#               through its gateway), and its own record carries the same
#               `model_basis` it carried before (the subscription for the
#               subscription repository, the API list for the other three).
#   audit       a call this check builds BY HAND, carrying a live run's own
#               identity and declaring a craft its record does not name, is
#               refused, and the service's log holds exactly one audit line for
#               it (U5, O1). The identity is minted the way the launcher mints
#               it (the runs' signer, a Firebase custom token, the identity
#               toolkit exchange), so the call reaches the record read. The
#               four runs above are launched FIRST and read afterwards, so the
#               call is made while they are going.
#
# It launches FIVE runs on the four repositories you name, and a project owner's
# `fugaro update` on the installation. Both COST MONEY: the runs' model spend.
# It is NOT read-only.
#
# Usage:      bash scripts/live-checks/check810.sh --installation <name> \
#               --oauth <checkout> --api-key <checkout> --cascade <checkout> \
#               --vertex <checkout> --job <the subscription repo's Cloud Run job> \
#               --gcp-project <project> --region <region> \
#               [--rtdb-url <url>] [--access-header <name>] [--signer <email>] \
#               [--service <name>] [--dry-run]
#             --dry-run       prints the plan and calls NOTHING: no gcloud, no
#                             fugaro, no run, no update.
#             --installation  the installation's Fugaro project name, the one
#                             `fugaro update --project` takes and the one every
#                             job of it carries as FUGARO_PROJECT. It is also
#                             the `fp` claim a run's identity carries, which is
#                             why the audit row needs it.
#             --oauth         the checkout of the repository on a subscription.
#             --api-key       the checkout of the repository on an Anthropic key.
#             --cascade       the checkout of the repository on the cascade.
#             --vertex        the checkout of the repository on Vertex.
#             --job           the subscription repository's Cloud Run job, as the
#                             installation names it: the audit row's hand-built
#                             call is made AS this job's account and names it, so
#                             the identity it presents has to be a run of THAT
#                             repository.
#             --gcp-project   the installation's GCP project (also CHECK810_GCP_PROJECT).
#             --region        the Cloud Run region (also CHECK810_REGION).
#             --rtdb-url      the Realtime Database URL, when the installation's
#                             own local project config (budget.rtdb_url) does not
#                             name it.
#             --access-header the header a run declares its CRAFT in. Its name is
#                             contract C1's (C1 (e): the headers a new runner
#                             declares its craft in, its task's grade where it
#                             names one, and the credential it chose where it
#                             chose one) and this script was written before that
#                             contract, so it is an argument and never a guess:
#                             without it the audit row reads UNVERIFIED rather
#                             than making a call whose header this check made up.
#             --signer        the account the runs' Firebase custom tokens are
#                             signed as (default fugaro-token-signer@<gcp-project>,
#                             internal/infra's SignerAccountID). Its own domain
#                             names the Firebase project the exchange happens at.
#             --service       the credentials service's Cloud Run service name
#                             (default fugaro-credentials).
# Needs:      bash, mktemp, python3 (every document is read by one python3
#             reader), gcloud (authenticated as the project owner: the update
#             half, the log read, the database read, the job and service reads
#             and the run records out of the runs bucket), curl, fugaro, git.
#             Nothing is installed and nothing is fetched.
# Where:      the installation, project and region you name, and NOWHERE else —
#             every call carries them explicitly and the script never reads
#             `gcloud config get-value project`. Run it on an installation you
#             may upgrade and on repositories you may spend on.
# Costs:      the five runs' model spend (bounded by the repositories' own caps).
#             Nothing else.
# Runs:       by YOU. No agent runs any step of it — the check refuses to start
#             in a coding agent's session (CLAUDECODE, CLAUDE_CODE_ENTRYPOINT,
#             CLAUDE_CODE_REMOTE, CURSOR_AGENT, AI_AGENT) — and its own test
#             (scripts/live-checks/check810_test.sh) drives it against fake
#             `gcloud`, `fugaro` and `curl` on PATH and touches no cloud.
# Cleanup:    its scratch directory is removed from the exit trap, and the
#             `restore` row says whether it really went. NOTHING ELSE IS
#             CREATED: the identity the audit row mints is signed in as the run
#             the check launched, whose Auth user that run's own launch already
#             made, so the exchange creates no user and none is deleted. The
#             five runs it launched are left to end on their own, as any run is.
# Output:     progress on stderr, ONE block on stdout headed CHECK 810 RESULT,
#             to copy back. Its last line is the verdict, and the verdict is the
#             exit status:
#
#               CHECK 810 RESULT: PASS (exit 0)        every criterion passed;
#               CHECK 810 RESULT: FAIL (exit 1)        a criterion was measured
#                                                      and failed;
#               CHECK 810 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                      could measure everything;
#               CHECK 810 RESULT: INCOMPLETE (exit 3)  it ran to the end and at
#                                                      least one row reads
#                                                      UNVERIFIED.
#
#             ONLY an all-PASS block is a PASS.
#
# One secret is READ and never printed: the job's Firebase web API key, which
# goes from the job's own document into a 0600 curl config file inside the
# python3 process that reads it, the way Check 840 reads it. The tokens this
# check mints are the same: they live in 0600 files inside this run's 0700
# scratch directory and are never arguments, never printed and never exported.
# Everything else this check reads is a name, a status or a record: the four
# committed files' `agent.auth`, the database's two records, the update's own
# output, a run's own row and record, and the service's own log lines.
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
IN_FLIGHT_ID=""
IN_FLIGHT_SLUG=""
IN_FLIGHT_REF=""
BEFORE_BASIS=""
AUTH_CONF=""
IDENTITY_CONF=""
OAUTH_RUN_ID=""
OAUTH_RUN_REF=""

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
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 810: it migrates a real installation and launches five paid runs. Run it yourself: bash scripts/live-checks/check810.sh --installation <name> --oauth <checkout> --api-key <checkout> --cascade <checkout> --vertex <checkout> --job <job> --gcp-project <project> --region <region>"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x), and its trace would print the arguments of every command this check runs. Run it without -x: bash scripts/live-checks/check810.sh"
  fi
}

usage() {
  cat <<'USAGE'
Check 810 — the auth retirement's first release, watched across four repositories
and a run in flight. Run by YOU, against an installation at the PREVIOUS release
that has a repository on a subscription, one on an Anthropic key, one on the
cascade and one on Vertex.

Usage:
  bash scripts/live-checks/check810.sh --installation <name> \
    --oauth <checkout> --api-key <checkout> --cascade <checkout> \
    --vertex <checkout> --job <the subscription repo's Cloud Run job> \
    --gcp-project <project> --region <region> \
    [--rtdb-url <url>] [--access-header <name>] [--dry-run]
  bash scripts/live-checks/check810.sh --help

It launches FIVE paid runs and runs `fugaro update` for the installation and for
each of the four repositories. Every row it prints is one criterion of the
register's Check 810; the last line of the block is the verdict and the exit
status:

  PASS (exit 0)        every criterion passed
  FAIL (exit 1)        a criterion was measured and failed
  INCOMPLETE (exit 2)  the run stopped before it could measure everything
  INCOMPLETE (exit 3)  at least one row reads UNVERIFIED
USAGE
}

INSTALLATION=${CHECK810_INSTALLATION:-}
GCP_PROJECT=${CHECK810_GCP_PROJECT:-}
REGION=${CHECK810_REGION:-}
OAUTH=${CHECK810_OAUTH:-}
API_KEY=${CHECK810_API_KEY:-}
CASCADE=${CHECK810_CASCADE:-}
VERTEX=${CHECK810_VERTEX:-}
JOB=${CHECK810_JOB:-}
RTDB_URL=${CHECK810_RTDB_URL:-}
ACCESS_HEADER=${CHECK810_ACCESS_HEADER:-}
SIGNER=${CHECK810_SIGNER:-}
SERVICE=${CHECK810_SERVICE:-fugaro-credentials}
DRY_RUN=no
if [ "${CHECK810_DRY_RUN:-}" = "1" ]; then DRY_RUN=yes; fi

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
  --oauth)
    [ $# -ge 2 ] || stop "--oauth needs the path of the subscription repository's checkout"
    OAUTH=$2
    shift 2
    ;;
  --api-key)
    [ $# -ge 2 ] || stop "--api-key needs the path of the Anthropic-key repository's checkout"
    API_KEY=$2
    shift 2
    ;;
  --cascade)
    [ $# -ge 2 ] || stop "--cascade needs the path of the cascade repository's checkout"
    CASCADE=$2
    shift 2
    ;;
  --vertex)
    [ $# -ge 2 ] || stop "--vertex needs the path of the Vertex repository's checkout"
    VERTEX=$2
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
  --access-header)
    [ $# -ge 2 ] || stop "--access-header needs the header's name"
    ACCESS_HEADER=$2
    shift 2
    ;;
  --signer)
    [ $# -ge 2 ] || stop "--signer needs the signing account's email"
    SIGNER=$2
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
    stop "unknown argument $1 (usage: check810.sh --installation <name> --oauth <checkout> --api-key <checkout> --cascade <checkout> --vertex <checkout> --job <job> --gcp-project <project> --region <region>, --help, or --dry-run)"
    ;;
  esac
done

dry_run_plan() {
  cat <<PLAN
Check 810 plan (nothing is called: no gcloud, no fugaro, no run, no update)

  installation:     ${INSTALLATION:-<not given>}
  gcp project:      ${GCP_PROJECT:-<not given>}
  region:           ${REGION:-<not given>}
  oauth repo:       ${OAUTH:-<not given>}
  api-key repo:     ${API_KEY:-<not given>}
  cascade repo:     ${CASCADE:-<not given>}
  vertex repo:      ${VERTEX:-<not given>}
  subscription job: ${JOB:-<not given>}
  rtdb url:         ${RTDB_URL:-<read from the local project config of the installation: budget.rtdb_url>}
  access header:    ${ACCESS_HEADER:-<not given: the audit row is UNVERIFIED without it>}
  signer:           ${SIGNER:-fugaro-token-signer@${GCP_PROJECT:-<project>}.iam.gserviceaccount.com}

What it would do, in order:

  premise      git -C <each checkout> show HEAD:fugaro.yaml
               -> each committed file must write its own old agent.auth (oauth, api-key, cascade, vertex); and
               curl <rtdb>/creds.json
               -> creds/repoauth must carry the four repositories' records and creds/repoaccess must be ABSENT.
               A premise that does not hold STOPS the check (exit 2).
  in-flight    fugaro run --task-file <a task the check writes> --json   (in ${OAUTH:-<oauth>})
               -> the FIRST paid run, launched before the update.
  update       fugaro update --project ${INSTALLATION:-<installation>}   (in ${OAUTH:-<oauth>})
               fugaro update --repo --project ${INSTALLATION:-<installation>}   (in each of the four checkouts)
               -> the project owner's upgrade and each repository's own re-apply; all five must exit 0.
  crafts       curl <rtdb>/creds.json
               -> each repository's access record must name the credential its old auth means, name 'vertex'
               for the Vertex repository and never for the others, and carry version 1.
  says         the five updates' own output must name each repository and a credential its crafts resolve to.
  in-flight-2  fugaro runs ls --json, polled until the FIRST run's row is terminal: the migration must not
               have killed it (U5). Its own record is read out of the runs bucket for the basis the
               subscription repository's runs carried BEFORE the update.
  oauth-run, key-run, cascade-run, vertex-run
               fugaro run ... (in each checkout) -> FOUR more paid runs, one per repository, all four
               launched together and read AFTER the audit row below, so the hand-built call is made
               while they are going.
  audit        the check mints a run identity the way the launcher mints it (IAM Credentials signJwt as
               ${SIGNER:-<signer>}, the identity toolkit exchange with the job's own web key) for the run it
               launched on ${OAUTH:-<oauth>}, then POSTs to the credentials service's /v1/messages with that
               identity, the job's own ID token and the ${ACCESS_HEADER:-<access header>} header declaring a craft
               no record names. It must be REFUSED, and the service's log must hold exactly one audit line
               for it (U5, O1).
  then         gcloud logging read '<the service's own lines for each run>' -> a served line (it went through
               its gateway); and gcloud storage cat gs://fugaro-runs-<project>/runs/<ref>/result.json
               -> cost.model_basis must be the same as before (the subscription for the subscription
               repository, the api list for the other three).

Costs: the five runs' model spend, bounded by the repositories' own caps.
Cleanup: the check's scratch directory. Nothing else is created: the identity it mints is signed in as a
run it launched, whose Auth user that run's own launch already made.
PLAN
}

if [ "$DRY_RUN" = no ]; then
  [ -n "$INSTALLATION" ] || stop "no --installation was given (or --help for the usage)"
  [ -n "$GCP_PROJECT" ] || stop "no --gcp-project was given (or --help for the usage)"
  [ -n "$REGION" ] || stop "no --region was given (or --help for the usage)"
  [ -n "$OAUTH" ] || stop "no --oauth was given (or --help for the usage)"
  [ -n "$API_KEY" ] || stop "no --api-key was given (or --help for the usage)"
  [ -n "$CASCADE" ] || stop "no --cascade was given (or --help for the usage)"
  [ -n "$VERTEX" ] || stop "no --vertex was given (or --help for the usage)"
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
need curl
need fugaro
need git

for pair in "oauth:$OAUTH" "api-key:$API_KEY" "cascade:$CASCADE" "vertex:$VERTEX"; do
  dir=${pair#*:}
  [ -d "$dir" ] || stop "${dir} is not a directory: --${pair%%:*} is a checkout of a repository"
  [ -d "$dir/.git" ] || stop "${dir} is not a git checkout: --${pair%%:*} is the repository the check migrates and launches runs from"
done

if [ -z "$SIGNER" ]; then SIGNER="fugaro-token-signer@${GCP_PROJECT}.iam.gserviceaccount.com"; fi
# The signer is an account OF the Firebase project the custom token is exchanged
# at: its own domain names that project (check-840 derives it the same way, and
# internal/infra/firebase.go renders the account in the Firebase project).
FB_PROJECT=${SIGNER#*@}
FB_PROJECT=${FB_PROJECT%.iam.gserviceaccount.com}

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk810.XXXXXXXX") || stop "cannot make a scratch directory"
chmod 700 "$SCRATCH"
FACTS="$SCRATCH/facts"
: >"$FACTS"
MARKER="$SCRATCH/.chk810-mine"
: >"$MARKER"
REPOS="$SCRATCH/repos.txt"
LAUNCHED="$SCRATCH/launched.tsv"
: >"$REPOS"
: >"$LAUNCHED"
AUTH_CONF="$SCRATCH/auth.conf"
IDENTITY_CONF="$SCRATCH/identity.conf"

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
  printf '\nCHECK 810 RESULT\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-14s %-11s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 810 RESULT: %s (exit %s)\n' "$(verdict "$facts")" "$status"
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
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): the rows that were not reached were NOT measured, which is this script ending on an error rather than an answer about the migration"
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|the scratch directory is gone (its documents: the four committed files' agent.auth, the database's creds node before and after, the five updates' own outputs, five runs' rows, the in-flight run's record and the four post-update runs' records, the service's own log lines, and the task files this check wrote). Nothing else was created: the identity the audit row minted is signed in as a run this check launched, whose Auth user that run's own launch already made, so no user was created and none was deleted. The five runs it launched are left to end on their own"
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

# committed_auth CHECKOUT prints the `agent.auth` the checkout's COMMITTED
# `fugaro.yaml` writes, or the empty string. It is the committed file the plan
# names: a working tree whose offered removal is not committed yet is a
# repository the release has already reached, and reading the working file would
# take such a checkout for one at the previous release.
committed_auth() { # committed_auth <checkout>
  # The file is written out and read by name: a heredoc is this script's own
  # stdin, so a pipeline into `python3 - <<'PY'` would leave the reader with the
  # program and no document.
  local tmp="$SCRATCH/committed.yaml"
  git -C "$1" show HEAD:fugaro.yaml >"$tmp" 2>/dev/null || : >"$tmp"
  python3 - "$tmp" <<'PY'
import re, sys
top = ""
for line in open(sys.argv[1]):
    line = line.rstrip("\n")
    if not line.strip() or line.lstrip().startswith("#"):
        continue
    if not line[:1].isspace():
        top = line.split(":", 1)[0].strip()
        continue
    if top != "agent":
        continue
    m = re.match(r"\s+auth:\s*(\S+)\s*$", line)
    if m:
        print(m.group(1).strip("'\""))
        break
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
  local cfg=${CHECK810_CONFIG:-${XDG_CONFIG_HOME:-${HOME:-}/.config}/fugaro/projects/$1.yaml}
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
  curl -sS --max-time "${CHECK810_CURL_SECONDS:-60}" --config "$AUTH_CONF" \
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
# under NODE (`repoauth` for the records the release behind wrote, `repoaccess`
# for the ones this release writes) whose key is that repository's slug, or
# nothing. The records are the members of that node of the database's creds node
# (internal/creds/repoauth.go's path is creds/repoauth/<slug>), which is why the
# node is named here rather than assumed. A slug is its readable part (the
# canonical path with every run of characters that are not a-z0-9 turned into
# `-`) then `-` and sixteen hex of a hash (task.Slug), so the readable part is
# what a checkout can be matched on without this check recomputing the hash — and
# a key that matches two records is printed twice, which the caller reports
# rather than picks between.
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

# record_names FILE [wide] prints the names a JSON document carries, one per
# line, plus `version=<n>`. It is how a row reads what a record NAMES: the
# access record's own shape is contract C1's (d), and this check was written
# before that contract, so a row that has to say whether a record names a
# credential reads the names in it rather than guessing at its members. With
# `wide`, the keys of every object are printed too — a document that names a
# credential as a member's NAME rather than as its value is read either way.
record_names() { # record_names <file> [wide]
  python3 - "$1" "${2:-}" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
wide = sys.argv[2] == "wide"
def walk(node):
    if isinstance(node, dict):
        for key, value in node.items():
            if key == "version" and isinstance(value, int):
                print("version=%d" % value)
                continue
            if wide and isinstance(value, str) and value:
                print(key)
            walk(value)
    elif isinstance(node, list):
        for value in node:
            walk(value)
    elif isinstance(node, str) and node:
        print(node)
walk(doc)
PY
}

launch_run() { # launch_run <checkout> <outfile>
  (cd "$1" && fugaro run --task-file "$SCRATCH/task.md" --json) >"$2" 2>"$2.err"
}

# row_of RUNSFILE RUNID prints `<outcome>|<ref>` for one run of a listing, or
# nothing when the listing carries no row for it. `fugaro runs ls --json` prints
# the ref (slug/run-id) each row's record is read under, and the outcome is
# written when the run has ended.
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

# poll_row CHECKOUT RUNID OUT waits for one run's row to be terminal and prints
# `<outcome>|<ref>`. It returns 1 when the check's own wait is over with the row
# still not terminal, and 2 when the listing itself never answered — a read that
# did not answer is not a run that has not ended.
poll_row() { # poll_row <checkout> <run-id> <out>
  local checkout=$1 rid=$2 out=$3 i=0 answered=no row=""
  while [ "$i" -lt "${CHECK810_POLLS:-90}" ]; do
    if (cd "$checkout" && fugaro runs ls --json) >"$out" 2>"$out.err"; then
      answered=yes
      row=$(row_of "$out" "$rid")
      if [ -n "$row" ] && [ -n "${row%%|*}" ]; then
        printf '%s\n' "$row"
        return 0
      fi
    fi
    i=$((i + 1))
    sleep "${CHECK810_POLL_SECONDS:-20}"
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
# the record of what the service did with a credential — one per refusal on the
# new narrowing, C1.13 — and `credsvc: refused` is the diagnosis written BESIDE
# it for a refusal whose run identity the service verified. Neither carries a
# value.
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
# written nothing. With no pattern it stops at the first line for the run. It
# returns 1 only when the read never answered, and the reason is written beside
# the file it is about; a wait that ran out returns 0 with whatever was read,
# which the caller reports rather than this turning into an answer.
audit_read() { # audit_read <run-id> <out> [pattern]
  local rid=$1 out=$2 pattern=${3:-} waited=0 rc=0 filter
  filter='resource.type="cloud_run_revision" resource.labels.service_name="'"$SERVICE"'" jsonPayload.run="'"$rid"'"'
  while :; do
    rc=0
    gcloud logging read "$filter" --project="$GCP_PROJECT" --freshness="${CHECK810_LOG_AGE:-90m}" \
      --limit="${CHECK810_LOG_LIMIT:-200}" --order=asc --format=json >"$out" 2>"$SCRATCH/log.err" || rc=$?
    if [ "$rc" -eq 0 ] && log_lines "$out" | grep -q .; then
      if [ -z "$pattern" ] || log_lines "$out" | grep -q -- "$pattern"; then return 0; fi
    fi
    if [ "$waited" -ge "${CHECK810_LOG_WAIT_SECONDS:-120}" ]; then
      if [ "$rc" -ne 0 ]; then
        printf "the service's own lines for the run (%s) could not be read: 'gcloud logging read' exited %s and printed: %s. A read that did not answer is NOT the service having written no line. Read the log yourself (the filter is %s) and record it in the register's section" \
          "$rid" "$rc" "$(head -c 300 "$SCRATCH/log.err" 2>/dev/null)" "$filter" >"$out.unread"
        return 1
      fi
      return 0
    fi
    sleep "${CHECK810_LOG_POLL_SECONDS:-10}"
    waited=$((waited + ${CHECK810_LOG_POLL_SECONDS:-10}))
  done
}

audit_unread() { cat "$1.unread" 2>/dev/null || true; }

# ------------------------------------------------- the audit row's own machinery

# read_job_facts reads the installation's job for the two facts the audit row
# needs off it — the account it runs as (the identity its calls are admitted by,
# which is also the repository that account is bound to) and its Firebase web API
# key, which addresses the identity toolkit exchange. The key's VALUE is printed
# nowhere: it goes from the job's document into a 0600 curl config file inside
# this python3 process, the way Check 840 reads it.
#
# The document is read at both nestings a `gcloud run jobs describe --format=json`
# answer has been seen in — the v1 job shape the checklist pins with the gcloud
# version it was read from (`spec.template.spec.template.spec`, see Check 39 and
# Check LC4) and the API's other shape under `template.template` — and the
# account under both of its keys, so a gcloud that answers either way is read
# rather than taken for a job with no account.
read_job_facts() { # read_job_facts
  gcloud run jobs describe "$JOB" --project "$GCP_PROJECT" --region "$REGION" --format=json \
    >"$SCRATCH/job.json" 2>"$SCRATCH/job.err" || {
    printf 'the job %s could not be read in %s/%s: %s' "$JOB" "$GCP_PROJECT" "$REGION" \
      "$(head -c 300 "$SCRATCH/job.err" 2>/dev/null)"
    return 1
  }
  python3 - "$SCRATCH/job.json" "$SCRATCH/firebase-signin.conf" "$FIREBASE_KEY_ENV" "$IDENTITY_URL" <<'PY'
import json, os, sys

def spec(doc):
    for path in (
        ("spec", "template", "spec", "template", "spec"),
        ("spec", "template", "spec"),
        ("template", "template"),
        ("spec",),
    ):
        cur = doc
        for part in path:
            if isinstance(cur, dict) and part in cur:
                cur = cur[part]
            else:
                cur = None
                break
        if isinstance(cur, dict) and (cur.get("containers") or cur.get("serviceAccount")
                                      or cur.get("serviceAccountName")):
            return cur
    return {}

doc = json.load(open(sys.argv[1]))
conf, key_env, url = sys.argv[2:5]
s = spec(doc)
sa = s.get("serviceAccountName") or s.get("serviceAccount") or ""
env = {}
for c in s.get("containers") or []:
    for e in c.get("env") or []:
        env[e.get("name") or ""] = e.get("value") or ""
key = env.get(key_env) or ""
print("sa=%s" % sa)
print("key=%s" % ("yes" if key else "no"))
if key:
    fd = os.open(conf, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as fh:
        fh.write('url = "%s?key=%s"\n' % (url, key))
    os.chmod(conf, 0o600)
PY
}

# mint_identity SLUG RUN: one Firebase custom token signed as the runs' signer
# through IAM Credentials signJwt, with the launcher's own claim set (iss and sub
# the signer, the identity toolkit audience, an hour of life, the uid r~slug~run
# and the fs/fr/fp/fx claims the credentials service checks), traded at
# accounts:signInWithCustomToken for the ID token a run presents as
# X-Fugaro-Run-Identity. Neither token is ever printed: both live in 0600 files
# inside this run's 0700 scratch directory.
#
# The exchange signs in as the uid the RUN's own launch already made, so it
# creates no Firebase Auth user: it asks for a fresh ID token for a user that is
# there. That is why this check deletes nothing on the way out.
mint_identity() { # mint_identity <slug> <run>
  local slug=$1 run=$2 code="" out=""
  python3 - "$SIGNER" "$slug" "$run" "$INSTALLATION" "$SCRATCH/signjwt-body.json" <<'PY' || return 1
import json, sys, time
signer, slug, run, project, out = sys.argv[1:6]
now = int(time.time())
payload = {
    "iss": signer,
    "sub": signer,
    "aud": "https://identitytoolkit.googleapis.com/google.identity.identitytoolkit.v1.IdentityToolkit",
    "iat": now,
    "exp": now + 3600,
    "uid": "r~%s~%s" % (slug, run),
    "claims": {"fs": slug, "fr": run, "fp": project, "fx": (now + 3600) * 1000},
}
open(out, "w").write(json.dumps({"payload": json.dumps(payload)}))
PY
  if ! code=$(curl -sS --max-time "${CHECK810_CURL_SECONDS:-60}" -X POST \
    -H 'Content-Type: application/json' --config "$AUTH_CONF" \
    -d "@$SCRATCH/signjwt-body.json" -o "$SCRATCH/signjwt.json" -w '%{http_code}' \
    "$IAMCRED_URL/$SIGNER:signJwt" 2>"$SCRATCH/signjwt.err"); then
    printf 'the signJwt call did not answer: curl failed before any HTTP response (%s)' "$(head -c 200 "$SCRATCH/signjwt.err" 2>/dev/null)"
    return 1
  fi
  if [ "$code" != 200 ]; then
    printf "signJwt answered HTTP %s for %s, so no custom token was minted: signing as the runs' signer needs the token-minter role on it (roles/iam.serviceAccountTokenCreator for the account you are signed in as, which fugaro doctor's token-signers check reports)" "$code" "$SIGNER"
    return 1
  fi
  if ! python3 - "$SCRATCH/signjwt.json" "$SCRATCH/signin-body.json" <<'PY'
import json, os, sys
tok = (json.load(open(sys.argv[1])).get("signedJwt") or "").strip()
if not tok:
    raise SystemExit(1)
fd = os.open(sys.argv[2], os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
with os.fdopen(fd, "w") as fh:
    json.dump({"token": tok, "returnSecureToken": True}, fh)
os.chmod(sys.argv[2], 0o600)
PY
  then
    printf 'the signJwt answer carried no token, so no identity was minted'
    return 1
  fi
  if ! code=$(curl -sS --max-time "${CHECK810_CURL_SECONDS:-60}" -X POST \
    -H 'Content-Type: application/json' --config "$SCRATCH/firebase-signin.conf" \
    -d "@$SCRATCH/signin-body.json" -o "$SCRATCH/signin.json" -w '%{http_code}' \
    2>"$SCRATCH/signin.err"); then
    printf 'the exchange did not answer: curl failed before any HTTP response (%s)' "$(head -c 200 "$SCRATCH/signin.err" 2>/dev/null)"
    return 1
  fi
  if [ "$code" != 200 ]; then
    printf 'the identity toolkit refused the exchange at %s (HTTP %s): %s' "$FB_PROJECT" "$code" "$(head -c 300 "$SCRATCH/signin.json" 2>/dev/null)"
    return 1
  fi
  if ! python3 - "$SCRATCH/signin.json" "$SCRATCH/identity.jwt" "$SCRATCH/identity.conf" <<'PY'
import json, os, sys
tok = (json.load(open(sys.argv[1])).get("idToken") or "").strip()
if not tok:
    raise SystemExit(1)
fd = os.open(sys.argv[2], os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
with os.fdopen(fd, "w") as fh:
    fh.write(tok)
os.chmod(sys.argv[2], 0o600)
fd = os.open(sys.argv[3], os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
with os.fdopen(fd, "w") as fh:
    fh.write('header = "X-Fugaro-Run-Identity: %s"\n' % tok)
os.chmod(sys.argv[3], 0o600)
PY
  then
    printf 'the exchange answered without an ID token, so no identity was minted'
    return 1
  fi
  return 0
}

# service_url prints the credentials service's own URL, which is the audience
# every caller's Google ID token must carry.
service_url() {
  gcloud run services describe "$SERVICE" --project "$GCP_PROJECT" --region "$REGION" \
    --format='value(status.url)' 2>"$SCRATCH/service.err"
}

# job_token writes the 0600 curl config the hand-built call is authorised with:
# a Google ID token minted for the job's own account, with the service's URL as
# its audience — the identity Cloud Run IAM admits and the service verifies, and
# the account that decides which class of caller and which repository the call is
# (internal/credsvc/auth.go's verifyJob: the account is the job, and the
# X-Fugaro-Run-Job header decides nothing).
job_token() { # job_token <service account> <url>
  local sa=$1 url=$2
  if ! gcloud auth print-identity-token --impersonate-service-account="$sa" \
    --audiences="$url" --include-email >"$SCRATCH/job-token" 2>"$SCRATCH/job-token.err"; then
    printf 'a Google ID token for %s could not be minted: %s' "$sa" "$(head -c 300 "$SCRATCH/job-token.err" 2>/dev/null)"
    return 1
  fi
  python3 - "$SCRATCH/job-token" "$IDENTITY_CONF" <<'PY'
import os, sys
tok = open(sys.argv[1]).read().strip()
with open(sys.argv[2], "a") as fh:
    fh.write('header = "Authorization: Bearer %s"\n' % tok)
os.chmod(sys.argv[2], 0o600)
PY
  rm -f "$SCRATCH/job-token"
}

IAMCRED_URL="https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts"
TOOLKIT_BASE="https://identitytoolkit.googleapis.com/v1"
IDENTITY_URL="$TOOLKIT_BASE/accounts:signInWithCustomToken"
MESSAGES_PATH="/v1/messages"
FIREBASE_KEY_ENV="FUGARO_FIREBASE_API_KEY"

# The craft the hand-built call declares: a name no record of this installation
# carries, spelled the way a craft's key is (the per-repository craft block's
# keys, C1 (g): a config key).
CRAFT="chk810absent"

# ---------------------------------------------------------------- the rows

# repos_build reads each checkout's own committed file and canonical repository
# into $REPOS: one `<key>|<checkout>|<canonical>|<auth>` line per repository. It
# is built once and read by every row, so a row never re-reads a checkout's git
# state.
repos_build() {
  : >"$REPOS"
  local pair key dir auth canonical
  for pair in "oauth:$OAUTH" "api-key:$API_KEY" "cascade:$CASCADE" "vertex:$VERTEX"; do
    key=${pair%%:*}
    dir=${pair#*:}
    auth=$(committed_auth "$dir" || true)
    canonical=$(canonical_of "$dir" || true)
    printf '%s|%s|%s|%s\n' "$key" "$dir" "$canonical" "$auth" >>"$REPOS"
  done
}

step_premise() {
  say "reading the four checkouts' committed files and the installation's own records (premise)"
  repos_build
  local key dir canonical auth bad=""
  while IFS='|' read -r key dir canonical auth; do
    [ -n "$key" ] || continue
    if [ "$auth" != "$key" ]; then
      bad="${bad}${bad:+, }${dir} writes agent.auth '${auth:-<none>}' and --${key} is the repository on ${key}"
    fi
    if [ -z "$canonical" ]; then
      bad="${bad}${bad:+, }${dir} names no origin, so its repository and its record cannot be matched"
    fi
  done <"$REPOS"
  if [ -n "$bad" ]; then
    fact premise FAIL "the four checkouts are not the four repositories this check is about: ${bad}. Check 810 migrates an installation whose repositories still write the OLD agent.auth — one on a subscription (oauth), one on an Anthropic key (api-key), one on the cascade and one on Vertex — and every row below would be a row about nothing. Point the flags at those four checkouts and run it again"
    stop "the four checkouts are not the four this check is about"
  fi
  if [ -z "$RTDB_URL" ]; then
    RTDB_URL=$(local_rtdb_url "$INSTALLATION" || true)
    if [ -z "$RTDB_URL" ]; then
      fact premise UNVERIFIED "the installation's own local project config names no Realtime Database URL (${CHECK810_CONFIG:-${XDG_CONFIG_HOME:-${HOME:-}/.config}/fugaro/projects/${INSTALLATION}.yaml}, key budget.rtdb_url), so the installation's records cannot be read and NOTHING below was measured. Give the URL with --rtdb-url and run the check again"
      stop "no database URL could be read — nothing is launched on an installation nobody read"
    fi
  fi
  if ! record_read creds "$SCRATCH/creds-before.json"; then
    fact premise UNVERIFIED "the database's own creds node could not be read, so whether this installation is at the previous release is NOT measured: $(record_unread "$SCRATCH/creds-before.json"). Read ${RTDB_URL%/}/creds.json yourself and run the check again"
    stop "the installation's records could not be read — nothing is launched on an installation nobody read"
  fi
  if python3 - "$SCRATCH/creds-before.json" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
raise SystemExit(0 if isinstance(doc, dict) and "repoaccess" in doc else 1)
PY
  then
    fact premise FAIL "the database's creds node already carries an ACCESS record (creds/repoaccess), so this installation has already been reached by the release: Check 810 is about an installation at the PREVIOUS release, and every row below would be a row about nothing. Run it on such an installation, or read the register's section for what to do with one already migrated"
    stop "the installation already has an access record — this check is about the release before it"
  fi
  local missing=""
  while IFS='|' read -r key dir canonical auth; do
    [ -n "$key" ] || continue
    [ -n "$(record_match "$SCRATCH/creds-before.json" repoauth "$canonical")" ] || missing="${missing}${missing:+, }${canonical}"
  done <"$REPOS"
  if [ -n "$missing" ]; then
    fact premise FAIL "the database's creds node carries no repository auth record (creds/repoauth/<slug>) for ${missing}, so the old auth those repositories' runs go out on is not recorded and the row that reads what each craft resolves to has nothing to compare against: an installation whose repositories were never re-applied by 'fugaro update' is not the state this check is about"
    stop "the installation carries no auth record for at least one of the four repositories"
  fi
  fact premise PASS "the four committed files write the four old agent.auth values (oauth, api-key, cascade, vertex), the database's creds node carries their repository auth records (creds/repoauth/<slug>), and it carries NO access record (creds/repoaccess): this installation is at the previous release, which is the state Check 810 is about"
}

step_inflight() {
  say "launching the run that will be IN FLIGHT across the update (paid)"
  cat >"$SCRATCH/task.md" <<'TASK'
# Task: check810 — the run in flight across the auth retirement

Change nothing that matters: add one line to README.md naming this run as Check
810's in-flight run, and open a pull request.
TASK
  if ! launch_run "$OAUTH" "$SCRATCH/inflight.json"; then
    fact in-flight FAIL "the run that has to be in flight across the update could not be launched: $(head -c 400 "$SCRATCH/inflight.json.err" 2>/dev/null). The migration is watched across a run already going, so nothing below is read without one"
    stop "the in-flight run could not be launched, and the update is not run without it"
  fi
  IN_FLIGHT_ID=$(jget "$SCRATCH/inflight.json" run_id)
  [ -n "$IN_FLIGHT_ID" ] || IN_FLIGHT_ID=$(jget "$SCRATCH/inflight.json" id)
  IN_FLIGHT_REF=$(jget "$SCRATCH/inflight.json" run)
  IN_FLIGHT_SLUG=${IN_FLIGHT_REF%%/*}
  if [ -z "$IN_FLIGHT_ID" ] || [ -z "$IN_FLIGHT_SLUG" ]; then
    fact in-flight UNVERIFIED "a run was launched but its own answer carries no run id or no ref (slug/run-id) this check can read, so the rows that follow it across the update have nothing to follow: $(head -c 200 "$SCRATCH/inflight.json" 2>/dev/null)"
    return 0
  fi
  fact in-flight PASS "the run ${IN_FLIGHT_ID} of ${IN_FLIGHT_SLUG} was launched before the update and is in flight: the rows below read it, and the migration is applied underneath it"
}

step_update() {
  say "running the project owner's 'fugaro update' for ${INSTALLATION} (this is the migration)"
  if ! (cd "$OAUTH" && fugaro update --project "$INSTALLATION") >"$SCRATCH/update-installation.out" 2>&1; then
    fact update FAIL "the project owner's 'fugaro update --project ${INSTALLATION}' did not exit 0: $(head -c 500 "$SCRATCH/update-installation.out" 2>/dev/null). The whole upgrade is that one command, and a run that did not finish it leaves the installation where it was"
    stop "the owner's update failed — nothing below can be read"
  fi
  cp "$SCRATCH/update-installation.out" "$SCRATCH/update.out"
  local key dir canonical auth failed=""
  while IFS='|' read -r key dir canonical auth; do
    [ -n "$key" ] || continue
    say "re-applying ${canonical}'s own root ('fugaro update --repo', in ${dir})"
    if ! (cd "$dir" && fugaro update --repo --project "$INSTALLATION") >"$SCRATCH/update-$key.out" 2>&1; then
      failed="${failed}${failed:+, }${canonical}"
    fi
    printf '\n== %s (%s)\n' "$canonical" "$dir" >>"$SCRATCH/update.out"
    cat "$SCRATCH/update-$key.out" >>"$SCRATCH/update.out"
  done <"$REPOS"
  if [ -n "$failed" ]; then
    fact update FAIL "the project owner's 'fugaro update --repo --project ${INSTALLATION}' did not exit 0 in ${failed}: a repository's access record and its job are written by the update run for THAT repository, and a run that did not finish it leaves that repository without a record. Each run's own output is in the scratch directory's update-*.out"
    stop "the owner's repository update failed — the records cannot be read"
  fi
  fact update PASS "the project owner's 'fugaro update --project ${INSTALLATION}' and each of the four repositories' own 'fugaro update --repo' exited 0"
}

# step_crafts reads each repository's OLD record (creds/repoauth/<slug>, the
# closed shape internal/creds/repoauth.go pins: version, auth, accounts) and its
# NEW one (creds/repoaccess/<slug>) out of the database, and says what the new
# record names: the credential the old auth means. The new record's own shape is
# contract C1's (d), and this script was written before that contract, so the row
# reads the NAMES the record carries (record_names) rather than a member this
# check guessed at.
step_crafts() {
  say "reading the four access records the update wrote (creds/repoaccess)"
  if ! record_read creds "$SCRATCH/creds-after.json"; then
    fact crafts UNVERIFIED "the database's own creds node could not be read after the update, so what each repository's crafts resolve to is NOT measured: $(record_unread "$SCRATCH/creds-after.json")"
    return 0
  fi
  : >"$SCRATCH/craft-summary.txt"
  local key dir canonical auth slug oldjson newjson names version verdict="" ok=yes
  local oldauth hasvertex hasaccount account n
  while IFS='|' read -r key dir canonical auth; do
    [ -n "$key" ] || continue
    oldjson=$(record_match "$SCRATCH/creds-before.json" repoauth "$canonical")
    slug=${oldjson%%|*}
    newjson=$(record_match "$SCRATCH/creds-after.json" repoaccess "$canonical")
    printf '%s|%s|%s|%s\n' "$key" "$canonical" "$slug" "${newjson%%|*}" >>"$SCRATCH/craft-summary.txt"
    if [ -z "$newjson" ]; then
      verdict="${verdict}${verdict:+, }${canonical}: NO access record was written"
      ok=no
      continue
    fi
    printf '%s' "${newjson#*|}" >"$SCRATCH/new-$key.json"
    printf '%s' "${oldjson#*|}" >"$SCRATCH/old-$key.json"
    oldauth=$(jget "$SCRATCH/old-$key.json" auth)
    [ -n "$oldauth" ] || oldauth=$auth
    record_names "$SCRATCH/new-$key.json" >"$SCRATCH/names-$key.txt"
    record_names "$SCRATCH/new-$key.json" wide >"$SCRATCH/names-$key-wide.txt"
    version=$(sed -n 's/^version=//p' "$SCRATCH/names-$key.txt" | head -1)
    hasvertex=no
    grep -qx 'vertex' "$SCRATCH/names-$key-wide.txt" && hasvertex=yes
    account=$(jget "$SCRATCH/old-$key.json" accounts.0)
    hasaccount=n/a
    if [ "$oldauth" = oauth ] && [ -n "$account" ]; then
      hasaccount=no
      grep -qx -- "$account" "$SCRATCH/names-$key-wide.txt" && hasaccount=yes
    fi
    # What the new record must name, per the old auth: `vertex` is the built-in
    # credential's name (C1 (c)), the subscription repository's record names the
    # account its own record carries, and the other two name a credential that is
    # neither `vertex` nor that account.
    case "$oldauth" in
    vertex)
      if [ "$hasvertex" = yes ]; then
        verdict="${verdict}${verdict:+, }${canonical} (agent.auth vertex): names 'vertex'"
      else
        verdict="${verdict}${verdict:+, }${canonical} (agent.auth vertex): names NO 'vertex' — the built-in credential is what this repository's old auth meant"
        ok=no
      fi
      ;;
    oauth)
      if [ "$hasvertex" = yes ]; then
        verdict="${verdict}${verdict:+, }${canonical} (agent.auth oauth): names 'vertex' — a subscription repository's record must not"
        ok=no
      elif [ "$hasaccount" = yes ]; then
        verdict="${verdict}${verdict:+, }${canonical} (agent.auth oauth): names the account '${account}' its own record carries"
      elif [ "$hasaccount" = n/a ]; then
        verdict="${verdict}${verdict:+, }${canonical} (agent.auth oauth): its old record names no account to compare, and the new one names $(sed -n '/^version=/!p' "$SCRATCH/names-$key.txt" | paste -sd, -)"
        ok=no
      else
        verdict="${verdict}${verdict:+, }${canonical} (agent.auth oauth): does NOT name the account '${account}' its own record carries"
        ok=no
      fi
      ;;
    *)
      if [ "$hasvertex" = yes ]; then
        verdict="${verdict}${verdict:+, }${canonical} (agent.auth ${oldauth}): names 'vertex', which is not what its old auth meant"
        ok=no
      elif [ "$(grep -cv '^version=' "$SCRATCH/names-$key.txt")" -eq 0 ]; then
        verdict="${verdict}${verdict:+, }${canonical} (agent.auth ${oldauth}): names NO credential at all"
        ok=no
      else
        verdict="${verdict}${verdict:+, }${canonical} (agent.auth ${oldauth}): names $(sed -n '/^version=/!p' "$SCRATCH/names-$key.txt" | paste -sd, -)"
      fi
      ;;
    esac
    if [ -n "$version" ] && [ "$version" != 1 ]; then
      verdict="${verdict}${verdict:+, }${canonical}: version ${version}, not 1"
      ok=no
    fi
  done <"$REPOS"
  if [ "$ok" = yes ]; then
    fact crafts PASS "each of the four repositories' access record (creds/repoaccess/<slug>) names the credential its old agent.auth means: ${verdict}"
  else
    fact crafts FAIL "a repository's access record does not name what its old agent.auth means: ${verdict}. Read the register's section for what to do, and read the records themselves at ${RTDB_URL%/}/creds/repoaccess.json"
  fi
}

step_says() {
  say "reading the update's own output for what each repository's crafts resolve to"
  local key canonical slug newslug said="" ok=yes name names found n
  while IFS='|' read -r key canonical slug newslug; do
    [ -n "$key" ] || continue
    if [ -z "$newslug" ]; then
      said="${said}${said:+, }${canonical}: no record to read"
      ok=no
      continue
    fi
    name=${canonical##*/}
    names=$(sed -n '/^version=/!p' "$SCRATCH/names-$key.txt" 2>/dev/null || true)
    found=""
    while IFS= read -r n; do
      [ -n "$n" ] || continue
      if grep -qF -- "$n" "$SCRATCH/update.out"; then
        found="$n"
        break
      fi
    done <<<"$names"
    if ! grep -qF -- "$name" "$SCRATCH/update.out"; then
      said="${said}${said:+, }${canonical}: the update's own output never names it"
      ok=no
    elif [ -z "$found" ]; then
      said="${said}${said:+, }${canonical}: named, but no credential its record carries is"
      ok=no
    else
      said="${said}${said:+, }${canonical}: named, and so is '${found}'"
    fi
  done <"$SCRATCH/craft-summary.txt"
  if [ "$ok" = yes ]; then
    fact says PASS "the update's own output names each of the four repositories and a credential its crafts resolve to: ${said}"
  else
    fact says FAIL "the update's own output does not say what each repository's crafts resolve to: ${said}. The plan's after-deploy step for this release is that the update says it (§9), and an owner who is not told what a repository's runs will be served by cannot tell a migration from a stop"
  fi
}

step_inflight_finished() {
  if [ -z "$IN_FLIGHT_ID" ]; then
    fact in-flight-2 UNVERIFIED "there is no in-flight run id to follow: the launch row above says why"
    return 0
  fi
  say "reading the run ${IN_FLIGHT_ID} across the update"
  local row="" rc=0 outcome ref
  row=$(poll_row "$OAUTH" "$IN_FLIGHT_ID" "$SCRATCH/runs-inflight.json") || rc=$?
  if [ "$rc" -ne 0 ]; then
    if [ "$rc" -eq 2 ]; then
      fact in-flight-2 UNVERIFIED "the listing of runs never answered, so whether the run ${IN_FLIGHT_ID} launched before the update finished is NOT measured: $(head -c 300 "$SCRATCH/runs-inflight.json.err" 2>/dev/null). Read it yourself ('fugaro runs ls') and record the outcome in the register's section"
    else
      fact in-flight-2 UNVERIFIED "the run ${IN_FLIGHT_ID} launched before the update is still not terminal after the check's own wait, so whether the migration killed it is NOT measured. Read it yourself ('fugaro runs ls') and record the outcome in the register's section"
    fi
    return 0
  fi
  outcome=${row%%|*}
  ref=${row#*|}
  [ -n "$ref" ] || ref=$IN_FLIGHT_REF
  # The record of the run launched BEFORE the update is the "before" of the
  # plan's "recorded with the same model_basis as before": the subscription
  # repository's runs carried one basis before the migration, and the run
  # launched after it must carry the same.
  if run_record_read "$ref" "$SCRATCH/record-inflight.json"; then
    BEFORE_BASIS=$(jget "$SCRATCH/record-inflight.json" cost.model_basis)
  fi
  case "$outcome" in
  failed | cancelled)
    fact in-flight-2 FAIL "the run ${IN_FLIGHT_ID}, which was in flight when the migration was applied underneath it, ended '${outcome}': a run that started on the release behind must still finish (U5), and the migration asks no run to end first"
    ;;
  *)
    fact in-flight-2 PASS "the run ${IN_FLIGHT_ID}, in flight when the four repositories were migrated underneath it, ended '${outcome}': the migration left a run already going alone (U5)"
    ;;
  esac
}

# basis_of AUTH prints the model basis the runner records for one old auth value.
# It is runstore.ModelBasis's answer: a run whose repository went out on the
# subscription is recorded against the subscription, and every other auth's run
# against the API list. The plan's clause is that a run is recorded with the SAME
# basis as before, so this is the "before" for the three repositories no run was
# launched on before the update.
basis_of() { # basis_of <auth>
  case "$1" in
  oauth) printf 'subscription' ;;
  *) printf 'api-list' ;;
  esac
}

# step_runs_launch launches a run of each of the four repositories AFTER the
# update. The four are launched together and READ by step_runs_read, which runs
# after the audit row: the hand-built call has to present a run's identity while
# that run is still going (the service refuses an identity whose run has ended,
# before it reads the record — internal/credsvc/auth.go's checkLive), so the runs
# it could present must not have been waited for yet.
step_runs_launch() {
  local key dir canonical auth rid ref
  while IFS='|' read -r key dir canonical auth; do
    [ -n "$key" ] || continue
    say "launching a run of ${canonical} after the update (paid)"
    cat >"$SCRATCH/task.md" <<TASK
# Task: check810 — ${canonical}'s run after the auth retirement

Change nothing that matters: add one line to README.md naming this run as Check
810's post-update run of ${canonical}, and open a pull request.
TASK
    if ! launch_run "$dir" "$SCRATCH/run-$key.json"; then
      fact "$key-run" FAIL "a run of ${canonical} could not be launched after the update: $(head -c 400 "$SCRATCH/run-$key.json.err" 2>/dev/null). A repository the migration reached must still launch its runs; if the launcher refused it, the refusal it printed is the finding"
      continue
    fi
    rid=$(jget "$SCRATCH/run-$key.json" run_id)
    [ -n "$rid" ] || rid=$(jget "$SCRATCH/run-$key.json" id)
    ref=$(jget "$SCRATCH/run-$key.json" run)
    if [ -z "$rid" ]; then
      fact "$key-run" UNVERIFIED "a run of ${canonical} was launched but its own answer carries no run id this check can read, so neither its own record nor the service's lines for it can be read: $(head -c 200 "$SCRATCH/run-$key.json" 2>/dev/null)"
      continue
    fi
    [ -n "$ref" ] || ref="$canonical/$rid"
    printf '%s|%s|%s|%s|%s|%s\n' "$key" "$dir" "$canonical" "$auth" "$rid" "$ref" >>"$LAUNCHED"
    if [ "$key" = oauth ]; then
      OAUTH_RUN_ID=$rid
      OAUTH_RUN_REF=$ref
    fi
  done <"$REPOS"
  fact runs-launched PASS "$(grep -c . "$LAUNCHED" || true) of the four repositories' runs were launched after the update (a launch that failed has its own row above), and they are read after the audit row so that the hand-built call can present one of them while it is going"
}

step_audit() {
  if [ -z "$ACCESS_HEADER" ]; then
    fact audit UNVERIFIED "no --access-header was given, so the hand-built call that declares a craft its record does not name was NOT made: the header's name is contract C1's (C1 (e), the headers a new runner declares its craft in) and this script was written before that contract, so it is an argument and never a guess. Pass --access-header <name> and run the check again"
    return 0
  fi
  if [ -z "$OAUTH_RUN_ID" ]; then
    fact audit UNVERIFIED "no run of the subscription repository is going, so there is no identity the hand-built call could present: the service refuses an identity whose run has ended before it reads the record, and a call with such an identity would measure a refusal that is not the one this row is about. The launch row above says why there is none"
    return 0
  fi
  say "reading the job ${JOB} for the account and the key the hand-built call needs"
  local facts sa haskey
  if ! facts=$(read_job_facts); then
    fact audit UNVERIFIED "the job ${JOB} could not be read, so the hand-built call could not be authorised: ${facts}"
    return 0
  fi
  sa=$(printf '%s\n' "$facts" | sed -n 's/^sa=//p' | head -1)
  haskey=$(printf '%s\n' "$facts" | sed -n 's/^key=//p' | head -1)
  if [ -z "$sa" ] || [ "$haskey" != yes ]; then
    fact audit UNVERIFIED "the job ${JOB} names no account, or carries no ${FIREBASE_KEY_ENV} for the identity toolkit to be addressed with (account: ${sa:-none}, key: ${haskey:-no}), so the hand-built call cannot be made. Name the subscription repository's own job, which internal/infra gives both (the account is what binds the call to that repository), and run the check again"
    return 0
  fi
  # The craft the call declares must be one no record of this installation names,
  # or the call is not the one this row is about.
  if grep -qx -- "$CRAFT" "$SCRATCH/names-oauth-wide.txt" 2>/dev/null; then
    fact audit UNVERIFIED "the craft this row declares ('${CRAFT}') is NAMED in the subscription repository's own access record, so a call declaring it is not a call declaring a craft the record does not name, and nothing here is measured. Read the register's section (contract C1's record names the crafts the release wrote) and run the check again"
    return 0
  fi
  local url
  if ! url=$(service_url); then
    fact audit UNVERIFIED "the credentials service ${SERVICE} could not be described in ${GCP_PROJECT}/${REGION}, so its URL — the audience the call's own token must carry — is not known: $(head -c 300 "$SCRATCH/service.err" 2>/dev/null)"
    return 0
  fi
  if [ -z "$url" ]; then
    fact audit UNVERIFIED "the credentials service ${SERVICE} answered with no URL in ${GCP_PROJECT}/${REGION}, so the hand-built call cannot be addressed"
    return 0
  fi
  say "minting a run identity for the run ${OAUTH_RUN_ID} the way the launcher mints it (signJwt as ${SIGNER}, then the exchange at ${FB_PROJECT})"
  local reason=""
  if ! reason=$(mint_identity "${OAUTH_RUN_REF%%/*}" "$OAUTH_RUN_ID"); then
    fact audit UNVERIFIED "the run identity the hand-built call presents could not be minted: ${reason}. A minted identity needs roles/iam.serviceAccountTokenCreator on ${SIGNER}, and the exchange needs the job's own web key"
    return 0
  fi
  if ! reason=$(job_token "$sa" "$url"); then
    fact audit UNVERIFIED "the call's own Google ID token could not be minted for ${sa}: ${reason}"
    return 0
  fi
  python3 - "$SCRATCH/call-body.json" <<'PY'
import json, sys
open(sys.argv[1], "w").write(json.dumps({
    "model": "claude-sonnet-5",
    "max_tokens": 1,
    "messages": [{"role": "user", "content": "check810"}],
}))
PY
  say "making the hand-built call: ${ACCESS_HEADER}: ${CRAFT} (a craft no record names)"
  local code="" rc=0
  code=$(curl -sS --max-time "${CHECK810_CURL_SECONDS:-60}" -X POST \
    -H 'Content-Type: application/json' \
    -H "X-Fugaro-Run-Job: ${JOB}" \
    -H "${ACCESS_HEADER}: ${CRAFT}" \
    --config "$IDENTITY_CONF" \
    -d "@$SCRATCH/call-body.json" -o "$SCRATCH/call.json" -w '%{http_code}' \
    "$url$MESSAGES_PATH" 2>"$SCRATCH/call.err") || rc=$?
  if [ "$rc" -ne 0 ]; then
    fact audit UNVERIFIED "the hand-built call did not answer: curl exited ${rc} before any HTTP response ($(head -c 200 "$SCRATCH/call.err" 2>/dev/null)). Nothing is concluded about a refusal nobody saw"
    return 0
  fi
  local why
  why=$(python3 - "$SCRATCH/call.json" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
if isinstance(doc, dict):
    err = doc.get("error")
    if isinstance(err, dict):
        print(err.get("type") or err.get("code") or "")
    elif isinstance(err, str):
        print(err)
    elif doc.get("type"):
        print(doc.get("type"))
PY
)
  # The refusal this row is about is the NEW narrowing's: the service decides from
  # the record, so a refusal that names the craft is the one that proves it read
  # the record and refused what it does not name. A refusal for any other reason
  # (a declaration missing altogether, a claim the minted token lacked) leaves the
  # row UNVERIFIED with the words the service used, rather than a PASS that would
  # claim the new narrowing wrote a line it did not.
  local lines refusals refused refused_repo refused_type
  if ! audit_read "$OAUTH_RUN_ID" "$SCRATCH/log-audit.json" '^credsvc: refused|'; then
    fact audit UNVERIFIED "the hand-built call answered HTTP ${code}${why:+, ${why}}, but the service's own lines for ${OAUTH_RUN_ID} could not be read, so the line the refusal must leave is NOT measured: $(audit_unread "$SCRATCH/log-audit.json")"
    return 0
  fi
  lines=$(log_lines "$SCRATCH/log-audit.json")
  refusals=$(printf '%s\n' "$lines" | grep '^credsvc: audit|' | grep -vc '|ok|' || true)
  refused=$(printf '%s\n' "$lines" | grep -c '^credsvc: refused|' || true)
  refused_repo=$(printf '%s\n' "$lines" | grep '^credsvc: refused|' | cut -d'|' -f3 | sort -u | paste -sd, -)
  refused_type=$(printf '%s\n' "$lines" | grep '^credsvc: refused|' | cut -d'|' -f7 | sort -u | paste -sd, -)
  case "$code" in
  2*)
    fact audit FAIL "the service SERVED the hand-built call (HTTP ${code}${why:+, ${why}}), which declared the craft '${CRAFT}' in ${ACCESS_HEADER} — a craft no repository's access record names. A repository is never offered a credential its record does not name, whatever its call declares (O1): a service that serves what the caller declares has no bound at all"
    return 0
    ;;
  esac
  if [ "$refusals" -eq 0 ]; then
    if [ "$refused" -gt 0 ]; then
      fact audit FAIL "the hand-built call was refused (HTTP ${code}${why:+, ${why}}) and the service wrote its own diagnosis line for the run (${refused_repo:-a repository it names}), but the AUDIT log holds no line for it: a refusal on the new narrowing writes exactly one audit line (C1.13, CM6's TestTheAccessRefusalWritesOneAuditLine), and a refusal an operator cannot see in the audit is a call answered with nothing to read"
    else
      fact audit UNVERIFIED "the hand-built call was refused (HTTP ${code}${why:+, ${why}}), but the service's log holds neither an audit line nor a diagnosis line for the run ${OAUTH_RUN_ID}: that is the shape of a refusal taken BEFORE the identity was verified — an identity whose run has ended, or a claim the minted token did not carry (internal/credsvc/auth.go's verifyRun) — so whether a refusal of the craft writes its one line is NOT measured. Check the minted token's claims against verifyRun, or make the call yourself while a run of the repository is going (the register's section names it)"
    fi
    return 0
  fi
  if [ "$refusals" -gt 1 ]; then
    fact audit UNVERIFIED "the service's log holds ${refusals} audit lines with a status other than ok for the run ${OAUTH_RUN_ID}, and the check cannot say which of them is its own hand-built call: a run whose own calls were refused is a finding in its own right. Read them yourself ('gcloud logging read' with jsonPayload.run=\"${OAUTH_RUN_ID}\")"
    return 0
  fi
  if [ "$refused" -eq 0 ]; then
    fact audit UNVERIFIED "the service's log holds exactly one audit line with a status other than ok for the run ${OAUTH_RUN_ID}, but no diagnosis line beside it, so the check cannot say the line is its own hand-built call rather than a refusal of the run's own traffic: read it yourself ('gcloud logging read' with jsonPayload.run=\"${OAUTH_RUN_ID}\") and record what it says in the register's section"
    return 0
  fi
  if ! printf '%s' "${refused_type}${why}" | grep -qi 'craft'; then
    fact audit UNVERIFIED "the hand-built call was refused (HTTP ${code}${why:+, ${why}}) and the service's log holds exactly one audit line with a status other than ok for the run ${OAUTH_RUN_ID}, with its own diagnosis line for ${refused_repo:-a repository it names} — but neither the refusal nor the diagnosis NAMES the craft ('${CRAFT}' in ${ACCESS_HEADER}), so this is a refusal for another reason and whether the NEW narrowing refused it is NOT measured. A refusal whose words are not the craft's is not the line C1.13 is about; read it yourself and record it in the register's section"
    return 0
  fi
  fact audit PASS "the hand-built call declaring the craft '${CRAFT}' in ${ACCESS_HEADER} was refused (HTTP ${code}${why:+, ${why}}) with the service's own diagnosis line naming the craft (${refused_repo}, ${refused_type}), and the service's log holds exactly ONE audit line with a status other than ok for the run ${OAUTH_RUN_ID}: the refusal of a craft a repository's record does not name is recorded, once, where an operator can read it (U5, O1)"
}

step_runs_read() {
  local key dir canonical auth rid ref row rc outcome basis want compared served
  while IFS='|' read -r key dir canonical auth rid ref; do
    [ -n "$key" ] || continue
    rc=0
    row=$(poll_row "$dir" "$rid" "$SCRATCH/runs-$key.json") || rc=$?
    if [ "$rc" -ne 0 ]; then
      if [ "$rc" -eq 2 ]; then
        fact "$key-run" UNVERIFIED "the listing of runs never answered, so the run ${rid} of ${canonical} was not followed to its end: $(head -c 300 "$SCRATCH/runs-$key.json.err" 2>/dev/null)"
      else
        fact "$key-run" UNVERIFIED "the run ${rid} of ${canonical} is still not terminal after the check's own wait, so what its own record carries is NOT measured"
      fi
      continue
    fi
    outcome=${row%%|*}
    [ -n "${row#*|}" ] && ref=${row#*|}
    if [ "$outcome" = failed ] || [ "$outcome" = cancelled ]; then
      fact "$key-run" FAIL "the run ${rid} of ${canonical}, launched after the migration, ended '${outcome}': a repository the migration reached must still run, and its own record and the service's lines for it are what this row reads"
      continue
    fi
    if ! run_record_read "$ref" "$SCRATCH/record-$key.json"; then
      fact "$key-run" UNVERIFIED "the run ${rid} of ${canonical} ended '${outcome}', but its own record could not be read out of the runs bucket (gs://fugaro-runs-${GCP_PROJECT}/runs/${ref}/result.json): $(head -c 300 "$SCRATCH/record-$key.json.err" 2>/dev/null). A project whose config sets runs_bucket to another name is the one case where that is not this run's record"
      continue
    fi
    # The gateway: the run's own model calls are served by the credentials
    # service, so the service's log holds a line for it whose status is ok. A run
    # whose calls never reached the service did not go through its gateway.
    if ! audit_read "$rid" "$SCRATCH/log-$key.json" '^credsvc: audit|ok|'; then
      fact "$key-run" UNVERIFIED "the run ${rid} of ${canonical} ended '${outcome}', but the service's own lines for it could not be read, so whether it went through its gateway is NOT measured: $(audit_unread "$SCRATCH/log-$key.json")"
      continue
    fi
    served=$(log_lines "$SCRATCH/log-$key.json" | grep -c '^credsvc: audit|ok|' || true)
    if [ "$served" -eq 0 ]; then
      fact "$key-run" FAIL "the service's own lines for the run ${rid} of ${canonical} carry no served line (a 'credsvc: audit' line with status ok), so the run's model calls did not go through its gateway: a repository whose runs are not served by the credentials service is a repository the migration has not reached, whatever its record says"
      continue
    fi
    basis=$(jget "$SCRATCH/record-$key.json" cost.model_basis)
    if [ -z "$basis" ]; then
      fact "$key-run" UNVERIFIED "the run ${rid} of ${canonical} ended '${outcome}' and was served by the service (${served} served line(s)), but its own record carries no cost.model_basis, so whether the migration changed the basis its runs are recorded against is NOT measured. Read gs://fugaro-runs-${GCP_PROJECT}/runs/${ref}/result.json yourself and record it in the register's section"
      continue
    fi
    want=$(basis_of "$auth")
    compared=""
    if [ "$auth" = oauth ] && [ -n "$BEFORE_BASIS" ]; then
      want=$BEFORE_BASIS
      compared=" (the same basis the run launched before the update recorded, read from its own record)"
    fi
    if [ "$basis" != "$want" ]; then
      fact "$key-run" FAIL "the run ${rid} of ${canonical} (agent.auth ${auth}) is recorded with cost.model_basis '${basis}', not '${want}': the plan's clause is that a run is recorded with the SAME model_basis as before the migration, and a repository whose runs move from the subscription to the API list (or the other way) is a repository whose spending changed under it"
      continue
    fi
    fact "$key-run" PASS "the run ${rid} of ${canonical} ended '${outcome}', the service served ${served} of its calls through its gateway, and its own record carries cost.model_basis '${basis}'${compared}"
  done <"$LAUNCHED"
}

step_premise
step_inflight
step_update
step_crafts
step_says
step_inflight_finished
step_runs_launch
step_audit
step_runs_read
REACHED_END=yes
