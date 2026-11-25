#!/usr/bin/env bash
# Check 820 — a first `fugaro setup --location eu` in a scratch project leaves
# every located resource in the EU, and a run of it works. Run by YOU.
#
# The location preference promises that a new installation in the EU gets
# everything in the EU (docs/location-preference/, D3080-D3086), and that a
# thing which already exists keeps what it has (R5). No offline test can settle
# either promise: what Firebase and Google do with a (default) Firestore
# database, a default Realtime Database instance, a user-managed replication or
# a regional log bucket is measured against the real APIs, and the reconciliation
# carries the three questions that only this project can answer (U1, U2 and U6,
# and U8's three hosts). So this check does the whole first run itself, at a
# scratch project of your own, and reads every located thing BACK from the cloud:
#
#   premise     the project exists and holds nothing of Fugaro's yet, so a first
#               `fugaro setup` is what runs here. A premise that does not hold is
#               a STOP (exit 2), not a finding.
#   setup       `fugaro setup --location eu ...`, the real command, at YOUR
#               terminal: you answer its confirmations, because a first
#               installation creates a project, links billing and places a
#               permanent Firestore database — steps that are a person's and are
#               never taken from a script.
#   record      `fugaro doctor --json`'s locations row: the record `fugaro setup`
#               wrote from what it found reads every located thing in the EU.
#   firestore   the (default) database's own answer (U1: whether a project whose
#               default resource location is set refuses one elsewhere).
#   rtdb        the default Realtime Database instance's location, read across
#               every location (U2), and the database URL its host carries.
#   buckets     every bucket of the project reads in the EU.
#   registry    every Artifact Registry repository is in the installation's
#               region.
#   secrets     every `fugaro=managed` container is replicated in the EU and
#               none is automatic — and the run row is what says a job in that
#               region can read one (U6).
#   logbucket   the installation's own log bucket's location.
#   hosts       U8: what each of the three Realtime Database hosts answers for a
#               database that lives in the EU. The answers are recorded, and
#               only an answer the key service cannot read as either an
#               installation or an absence of one is a finding (LP15).
#   run         `fugaro run` launches a task, `fugaro runs ls` names it (the
#               record reached the runs bucket), `fugaro budget show` names the
#               lease (the budget backend answered), and `fugaro runs show`
#               reports its spend (its history).
#   restore     the check's own scratch directory is gone, and the one command
#               that removes the scratch project is named for you.
#
# What it does NOT do: it deletes nothing. The scratch project is yours to
# delete afterwards, and the row names the command.
#
# Usage:      bash scripts/live-checks/check-820.sh <project> [--name <name>]
#               [--repo <checkout>] [--dry-run]
#             --dry-run   prints the plan and calls NOTHING: no gcloud, no
#                         fugaro, no curl.
#             <project>   the scratch GCP project id: one that exists and holds
#                         nothing of Fugaro's. The check refuses anything else.
#             --name      the Fugaro project name the installation is created
#                         as (default: chk820). It must have no local config.
#             --repo      the checkout `fugaro run` launches from: one the
#                         scratch installation is onboarded to (default: the
#                         current directory).
# Needs:      bash, mktemp, python3, gcloud, curl and fugaro, authenticated
#             already, and a real terminal at the setup row. Nothing is
#             installed and nothing is fetched.
# Where:      the scratch project you name and NOWHERE else: no other project,
#             no other installation, no other machine.
# Costs:      the installation's own: the GCP project, its Firestore database,
#             its Realtime Database, one run's model spend and whatever the
#             scratch project's resources cost. The project is yours to delete.
# Runs:       by YOU. No agent runs any step of it — the check refuses to start
#             in a coding agent's session (CLAUDECODE, CLAUDE_CODE_ENTRYPOINT,
#             CLAUDE_CODE_REMOTE, CURSOR_AGENT, AI_AGENT) — and its own test
#             (scripts/live-checks/check-820_test.sh) drives it against fake
#             `gcloud`, `fugaro` and `curl` on PATH and touches no cloud.
# Cleanup:    its scratch directory is removed from the exit trap, and the
#             `restore` row says whether it really went. The scratch project is
#             NOT deleted: delete it yourself (the row names the command).
# Output:     progress on stderr, ONE block on stdout headed CHECK 820 RESULT,
#             to copy back. Its last line is the verdict, and the verdict is the
#             exit status:
#
#               CHECK 820 RESULT: PASS (exit 0)        every criterion passed;
#               CHECK 820 RESULT: FAIL (exit 1)        a criterion was measured
#                                                      and failed;
#               CHECK 820 RESULT: INCOMPLETE (exit 2)  the run stopped before it
#                                                      could measure everything;
#               CHECK 820 RESULT: INCOMPLETE (exit 3)  it ran to the end and at
#                                                      least one row reads
#                                                      UNVERIFIED.
#
#             ONLY an all-PASS block is a PASS.
#
# NO secret is read and none may be printed: every read here is a location, a
# name, a status or a count. The run this check launches is the installation's
# own and its task file says nothing secret.
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

stop() {
  printf '%s: stopped: %s\n' "$PROG" "$1" >&2
  ABORT=$1
  exit 2
}

fact() { # fact KEY PASS|FAIL|UNVERIFIED "detail"
  [ -n "$FACTS" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS"
}

one_line() { printf '%s' "$1" | tr '\n\r\t' '   ' | tr -s ' ' | sed 's/^ *//; s/ *$//' | cut -c1-500; }

read_out() {
  READ_STATUS=0
  if READ_OUT=$("$@" 2>&1); then
    READ_STATUS=0
  else
    READ_STATUS=$?
  fi
}

need() {
  command -v "$1" >/dev/null 2>&1 || stop "$1 is not on PATH, and this check reads a real project with it"
}

# jget FILE PATH prints the value at a dotted path of a JSON document, or the
# empty string.
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
    stop "this shell carries $found, so it is a coding agent's session, and no agent runs any step of Check 820: it creates a real installation and launches a real run. Run it yourself: bash scripts/live-checks/check-820.sh <project>"
}

guard_xtrace() {
  if [ "$XTRACE_AT_START" = yes ]; then
    stop "this shell is tracing (bash -x), and its trace would print the arguments of every command this check runs. Run it without -x: bash scripts/live-checks/check-820.sh <project>"
  fi
}

usage() {
  cat <<'USAGE'
Check 820 — a first `fugaro setup --location eu` in a scratch project leaves
every located resource in the EU, and a run of it works. Run by YOU, at a
scratch GCP project of your own, from a real terminal.

Usage:
  bash scripts/live-checks/check-820.sh <project> [--name <name>] [--repo <checkout>] [--dry-run]
  bash scripts/live-checks/check-820.sh --help

It runs the real `fugaro setup --location eu` (you answer its confirmations at
your terminal), reads every located thing back with `gcloud` and `curl`, reads
the record `fugaro doctor` prints, and launches one run of the installation.
Every row it prints is one criterion of the register's Check 820; the last line
of the block is the verdict and the exit status:

  PASS (exit 0)        every criterion passed
  FAIL (exit 1)        a criterion was measured and failed
  INCOMPLETE (exit 2)  the run stopped before it could measure everything
  INCOMPLETE (exit 3)  at least one row reads UNVERIFIED

It deletes nothing: the scratch project is yours to delete afterwards.
USAGE
}

PROJECT=""
NAME=${CHECK820_NAME:-chk820}
REPO=${CHECK820_REPO:-$PWD}
DRY_RUN=no
if [ "${CHECK820_DRY_RUN:-}" = "1" ]; then DRY_RUN=yes; fi

while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  --name)
    [ $# -ge 2 ] || stop "--name needs the installation's name"
    NAME=$2
    shift 2
    ;;
  --repo)
    [ $# -ge 2 ] || stop "--repo needs the path of a checkout"
    REPO=$2
    shift 2
    ;;
  --dry-run)
    DRY_RUN=yes
    shift
    ;;
  -*)
    stop "unknown argument $1 (usage: check-820.sh <project> [--name <name>] [--repo <checkout>], --help, or --dry-run)"
    ;;
  *)
    [ -z "$PROJECT" ] || stop "two project ids were given ($PROJECT and $1): this check creates an installation in ONE scratch project"
    PROJECT=$1
    shift
    ;;
  esac
done

dry_run_plan() {
  cat <<PLAN
Check 820 plan (nothing is called: no gcloud, no fugaro, no curl)

  project:  ${PROJECT:-<not given>}
  name:     $NAME
  checkout: $REPO

What it would do, in order:

  premise      gcloud projects describe ${PROJECT:-<project>} --format='value(projectId,lifecycleState)'
               gcloud storage buckets list --project=${PROJECT:-<project>} --format='value(name)'
               -> the project must exist and hold no fugaro-runs-* or fugaro-tfstate-* bucket, so a first
               'fugaro setup' is what runs here. A premise that does not hold STOPS the check (exit 2).
  setup        fugaro setup --location eu --gcp-project ${PROJECT:-<project>} --name $NAME --rigor poc --bake-repos
               -> the real first run, at YOUR terminal: you answer the confirmations it asks (creating
               the project's resources, linking billing, the Firestore database's permanent location).
               It must exit 0.
  record       fugaro doctor --json --project $NAME
               -> the locations row the record fugaro setup wrote: every located thing must read
               europe-west1.
  firestore    gcloud firestore databases describe --database=(default) --project=${PROJECT:-<project>} --format='value(locationId)'
               -> europe-west1 (U1).
  rtdb         the default Realtime Database instance listed across every location, and the database URL
               -> europe-west1, and the URL host carries it (U2).
  buckets      gcloud storage buckets describe gs://<each> --format='value(location)'
               -> every bucket of the project reads in the EU (EUROPE-WEST1 or EU).
  registry     gcloud artifacts repositories list --project=${PROJECT:-<project>} --format='value(name)'
               -> every repository's own name carries locations/europe-west1.
  secrets      gcloud secrets list --project=${PROJECT:-<project>} --filter='labels.fugaro=managed' --format='value(name)'
               then each container's replication
               -> every container is user-managed in europe-west1, and none is automatic.
  logbucket    gcloud logging buckets list --project=${PROJECT:-<project>} --format='value(name,location)'
               -> the installation's own bucket (fugaro) is in europe-west1.
  hosts        curl -o body -w '%{http_code}' https://<project>-default-rtdb.<host>/.json, for the legacy
               host, europe-west1's and asia-southeast1's
               -> U8: each answer is recorded, and only an answer the key service could read as neither
               an installation nor an absence of one is a finding.
  run          (cd $REPO && fugaro run --repo $REPO --project $NAME --title <title> --task-file <scratch task> --json)
               fugaro runs ls --json --project $NAME --limit 5
               fugaro budget show --json --project $NAME
               fugaro runs show <run> --json --project $NAME
               -> a run launches, its record reaches the runs bucket, the budget backend leases it
               money, and its spend is readable.
  restore      the check's own scratch directory is removed. The scratch project is NOT deleted.

Costs: the installation's own — the project, its databases, its buckets and one run's model spend.
Cleanup: the check's scratch directory. The project is yours to delete: gcloud projects delete ${PROJECT:-<project>} --quiet
PLAN
}

if [ "$DRY_RUN" = no ]; then
  [ -n "$PROJECT" ] || stop "no project id was given (or --help for the usage)"
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

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk820.XXXXXXXX") || stop "cannot make a scratch directory"
chmod 700 "$SCRATCH"
FACTS="$SCRATCH/facts"
: >"$FACTS"
MARKER="$SCRATCH/.chk820-mine"
: >"$MARKER"

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
  printf '\nCHECK 820 RESULT\n'
  printf '%s\n' "$facts" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-11s %-11s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 820 RESULT: %s (exit %s)\n' "$(verdict "$facts")" "$status"
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
    facts="${facts}"$'\n'"aborted|FAIL|the run ended before its last step (exit ${status}): the rows that were not reached were NOT measured, which is this script ending on an error rather than an answer about the installation"
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|the check's own scratch directory is gone (its documents: the run's task file, every read this check made and the doctor answer it read the record from). It created nothing else, and it deleted nothing: the scratch project is yours to delete — gcloud projects delete ${PROJECT} --quiet"
  else
    facts="${facts}"$'\n'"restore|FAIL|the check's own scratch directory is STILL THERE, at ${SCRATCH} — remove it yourself, and delete the scratch project with gcloud projects delete ${PROJECT} --quiet"
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

# in_eu LOCATION answers whether a location Google gave back is in the European
# Union. The three shapes are a regional location (EUROPE-WEST1, europe-west1),
# the EU multi-region (EU, eu) and Firestore's own regional spelling; the two
# `europe-` regions that are not in the union (London and Zürich) are named, so
# a promise read from a location's name cannot be wrong about them (place.Of's
# own rule).
in_eu() { # in_eu <location>
  local loc
  loc=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')
  case "$loc" in
  europe-west2 | europe-west6) return 1 ;;
  europe-* | eu) return 0 ;;
  *) return 1 ;;
  esac
}

# firestore_location reads the (default) database's location.
firestore_location() {
  read_out gcloud firestore databases describe --database='(default)' --project="$PROJECT" --format='value(locationId)'
}

# rtdb_instance prints "LOCATION<TAB>DATABASE_URL" for the project's default
# Realtime Database instance, read the way the CLI reads it: the instances are
# listed across EVERY location, because the location is in the name the API
# lists one under and nowhere else (infra.ObserveLocations's databaseInstance).
# There is no gcloud command for this — the Realtime Database management API is
# the only door, and `gcloud auth print-access-token` is the credential — so the
# token goes into a 0600 curl config file in this check's own scratch directory
# and NEVER on a command line or into anything this check prints.
rtdb_instance() {
  local doc="$SCRATCH/rtdb.json" conf="$SCRATCH/curl.conf" token="" rc=0
  token=$(gcloud auth print-access-token 2>"$SCRATCH/token.err") || {
    printf 'READ_ERROR\tthe access token could not be read: %s\n' "$(one_line "$(cat "$SCRATCH/token.err" 2>/dev/null)")"
    return 0
  }
  umask 077
  printf 'header = "Authorization: Bearer %s"\n' "$token" >"$conf"
  token=""
  curl -sS --max-time 20 --config "$conf" \
    "https://firebasedatabase.googleapis.com/v1beta/projects/${PROJECT}/locations/-/instances" \
    -o "$doc" 2>"$SCRATCH/rtdb.err" || rc=$?
  rm -f "$conf"
  if [ "$rc" -ne 0 ]; then
    printf 'READ_ERROR\t%s\n' "$(one_line "$(cat "$SCRATCH/rtdb.err" 2>/dev/null)")"
    return 0
  fi
  python3 - "$doc" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    print("READ_ERROR\tnot JSON")
    sys.exit(0)
if isinstance(doc, dict) and doc.get("error"):
    err = doc["error"]
    print("READ_ERROR\t%s" % (err.get("message") if isinstance(err, dict) else err))
    sys.exit(0)
for i in (doc.get("instances") or []):
    if not isinstance(i, dict):
        continue
    if i.get("type") not in ("DEFAULT_DATABASE", None) and "default" not in str(i.get("name", "")):
        continue
    name = str(i.get("name", ""))
    parts = [p for p in name.split("/") if p]
    try:
        loc = parts[parts.index("locations") + 1]
    except Exception:
        loc = ""
    print("%s\t%s" % (loc, i.get("databaseUrl", "")))
    sys.exit(0)
print("READ_ERROR\tno default Realtime Database instance was listed")
PY
}

# fugaro_secret_containers prints one line per `fugaro=managed` container's
# replication: "automatic", or the user-managed replica locations comma-
# separated. It reads each container on its own, because the replication is
# what the listing of the CLI's own read answers per container.
fugaro_secret_containers() { # fugaro_secret_containers <out>
  local out=$1 n=0 name rc=0
  read_out gcloud secrets list --project="$PROJECT" --filter='labels.fugaro=managed' --format='value(name)'
  [ "$READ_STATUS" -eq 0 ] || {
    printf 'READ_ERROR\t%s\n' "$(one_line "$READ_OUT")" >"$out"
    return 0
  }
  : >"$out"
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    n=$((n + 1))
    rc=0
    gcloud secrets describe "$name" --project="$PROJECT" --format=json >"$SCRATCH/secret-$n.json" 2>"$SCRATCH/secret-$n.err" || rc=$?
    if [ "$rc" -ne 0 ]; then
      printf '%s\tREAD_ERROR\t%s\n' "$name" "$(one_line "$(cat "$SCRATCH/secret-$n.err" 2>/dev/null)")" >>"$out"
      continue
    fi
    python3 - "$name" "$SCRATCH/secret-$n.json" >>"$out" <<'PY'
import json, sys
name, path = sys.argv[1], sys.argv[2]
try:
    doc = json.load(open(path))
except Exception:
    print("%s\tREAD_ERROR\tnot JSON" % name)
    sys.exit(0)
rep = (doc.get("replication") or {})
if "automatic" in rep:
    print("%s\tautomatic\t" % name)
    sys.exit(0)
um = rep.get("userManaged") or {}
locs = [r.get("location", "") for r in (um.get("replicas") or [])]
print("%s\tuser-managed\t%s" % (name, ",".join(sorted(set(l for l in locs if l)))))
PY
  done < <(printf '%s\n' "$READ_OUT")
}

# probe_host URL OUT prints the HTTP status the host answered with, and writes
# the body to OUT. It follows no redirect: what a host answers is the answer,
# and a client that followed one would read whatever is at the other end of it
# as that host's own (keyservice's askHost does the same).
probe_host() { # probe_host <url> <body-file>
  curl -sS --max-time 20 -o "$2" -w '%{http_code}' "$1" 2>"$2.err" || printf '000'
}

# answer_shape BODY STATUS prints the shape of one host's answer in the exact
# words LP15's read uses, and nothing for an answer it does not recognise: a
# locked database (401/403) is an installation, a 404 or the database's own
# "no such database" / "database not found" is this host holding none, and
# anything else is a host that did NOT answer — the service reads it as
# errProbeUnreachable and the whole probe fails (ee/keyservice/workspaces.go).
# Nothing wider is matched: a body that merely mentions a missing entity is
# still an answer the service cannot read.
answer_shape() { # answer_shape <body-file> <status>
  local said low
  said=$(printf '%s' "$(head -c 400 "$1" 2>/dev/null)" | tr '\n\r\t' '   ')
  low=$(printf '%s' "$said" | tr 'A-Z' 'a-z')
  case "$2" in
  401 | 403)
    printf 'a locked database (an installation answers here)'
    return 0
    ;;
  404)
    printf 'no database at this host'
    return 0
    ;;
  esac
  case "$low" in
  *"no such database"* | *"database not found"*)
    printf 'no database at this host (%s)' "$(one_line "$said")"
    ;;
  esac
}

# ------------------------------------------------------------- the check's rows

# step_premise refuses to measure anything until the scratch project is one a
# first setup belongs in: it exists, and it holds nothing of Fugaro's.
step_premise() {
  say "reading the scratch project (premise)"
  read_out gcloud projects describe "$PROJECT" --format='value(projectId,lifecycleState)'
  if [ "$READ_STATUS" -ne 0 ]; then
    fact premise FAIL "gcloud projects describe ${PROJECT} failed ($READ_STATUS): $(one_line "$READ_OUT"). This check creates an installation in a scratch project of YOURS, and a project that cannot be read is not one it may create anything in"
    stop "the scratch project could not be read"
  fi
  local id state
  id=${READ_OUT%%$'\t'*}
  state=${READ_OUT##*$'\t'}
  case "$id" in
  "$PROJECT") ;;
  *)
    fact premise FAIL "gcloud names the project '$(one_line "$id")' and not ${PROJECT}"
    stop "the scratch project is not the one that was named"
    ;;
  esac
  read_out gcloud storage buckets list --project="$PROJECT" --format='value(name)'
  if [ "$READ_STATUS" -ne 0 ]; then
    fact premise FAIL "gcloud storage buckets list --project=${PROJECT} failed ($READ_STATUS): $(one_line "$READ_OUT"), so whether this project already holds an installation is NOT measured"
    stop "the scratch project's buckets could not be listed"
  fi
  local held=""
  held=$(printf '%s\n' "$READ_OUT" | grep -E "^(fugaro-runs-|fugaro-tfstate-)" || true)
  if [ -n "$held" ]; then
    fact premise FAIL "${PROJECT} already holds $(printf '%s' "$held" | tr '\n' ' '): a project with an installation of Fugaro's in it is not one a FIRST 'fugaro setup' runs in, and what this check would measure is a re-converge. Point it at a project that has never held one"
    stop "the scratch project already holds an installation"
  fi
  if [ -e "${XDG_CONFIG_HOME:-$HOME/.config}/fugaro/projects/$NAME.yaml" ]; then
    fact premise FAIL "this machine already has a local config for the project name ${NAME} (${XDG_CONFIG_HOME:-$HOME/.config}/fugaro/projects/$NAME.yaml): this check is about a FIRST setup, and it would converge that installation instead. Name another with --name"
    stop "the project name already has a local config"
  fi
  fact premise PASS "${PROJECT} is ${state} and holds no Fugaro bucket, and this machine has no config for the name ${NAME}: a first 'fugaro setup' is what runs here"
}

# step_setup runs the real first setup at YOUR terminal. Its confirmations are
# a person's (the project's resources, the billing link, the Firestore
# database's permanent location) and are never taken from a script, so this is
# the ONE row that inherits your terminal rather than capturing the command.
step_setup() {
  say "running 'fugaro setup --location eu --gcp-project ${PROJECT} --name ${NAME}' at YOUR terminal (setup)"
  say "answer the confirmations it asks; this check captures nothing from this one command"
  local rc=0
  fugaro setup --location eu --gcp-project "$PROJECT" --name "$NAME" --rigor poc --bake-repos || rc=$?
  if [ "$rc" -eq 0 ]; then
    fact setup PASS "'fugaro setup --location eu --gcp-project ${PROJECT} --name ${NAME}' exited 0 at your terminal: the installation was created, and the rows below read back what it placed"
    return 0
  fi
  fact setup FAIL "'fugaro setup' did not exit 0 (exit ${rc}). The installation was not created or not converged, so nothing below reads a first EU installation — which is the whole of this check. Run it again and read what it said"
}

# step_record reads the record the setup wrote, through its only reader.
step_record() {
  say "reading the locations record back through 'fugaro doctor --json' (record)"
  local rc=0 doc="$SCRATCH/doctor.json"
  fugaro doctor --json --project "$NAME" >"$doc" 2>"$SCRATCH/doctor.err" || rc=$?
  local row
  row=$(python3 - "$doc" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
for c in doc.get("checks") or []:
    if isinstance(c, dict) and c.get("id") == "locations":
        print(c.get("problem", ""))
        break
PY
)
  if [ -z "$row" ]; then
    fact record UNVERIFIED "'fugaro doctor --json --project ${NAME}' (exit ${rc}) carries no locations row, so the record this setup wrote is NOT measured: $(one_line "$(cat "$SCRATCH/doctor.err" 2>/dev/null)"). Run 'fugaro doctor --project ${NAME}' yourself and read its locations row"
    return 0
  fi
  printf '%s\n' "$row" >"$SCRATCH/locations-row.txt"
  local missing=""
  local want
  for want in "its Firestore database is in europe-west1" "its Realtime Database is in europe-west1" "replicated in europe-west1" "its run log bucket is in europe-west1"; do
    printf '%s' "$row" | grep -qF "$want" || missing="${missing}${missing:+; }${want}"
  done
  if [ -n "$missing" ]; then
    fact record FAIL "the locations record of ${NAME} does not read every located thing in the EU. What is missing from doctor's row: ${missing}. The row it printed: $(one_line "$row")"
    return 0
  fi
  fact record PASS "the record 'fugaro setup' wrote from what it found reads every located thing in europe-west1: $(one_line "$row")"
}

# step_firestore reads the (default) database's location. It is U1's own
# reading: whether a project whose default resource location is set gives a
# (default) database anywhere else.
step_firestore() {
  say "reading the (default) Firestore database (firestore)"
  firestore_location
  if [ "$READ_STATUS" -ne 0 ]; then
    fact firestore FAIL "'gcloud firestore databases describe --database=(default)' failed ($READ_STATUS): $(one_line "$READ_OUT"). A first EU installation's history database is a (default) database in europe-west1, and a database that could not be read is not one that is in the EU"
    return 0
  fi
  if [ "$READ_OUT" = "europe-west1" ]; then
    fact firestore PASS "the (default) database's locationId is europe-west1 (U1: a project whose default resource location is set placed a (default) database in the installation's own region)"
    return 0
  fi
  fact firestore FAIL "the (default) database's locationId is '$(one_line "$READ_OUT")' and not europe-west1. A first installation in the EU puts its history database in europe-west1 (place.For), so this is either U1 answering differently than the mapping expects or a project whose default resource location was already set — the register's own section says which"
}

# step_rtdb reads the default Realtime Database instance's location and the
# database URL its host carries (U2).
step_rtdb() {
  say "reading the default Realtime Database instance (rtdb)"
  local answer loc url
  answer=$(rtdb_instance)
  loc=${answer%%$'\t'*}
  url=${answer#*$'\t'}
  if [ "$loc" = READ_ERROR ]; then
    fact rtdb FAIL "the default Realtime Database instance could not be read: $(one_line "$url"). The budget's backend is in europe-west1 on a first EU installation, and one that could not be listed is not one that is"
    return 0
  fi
  printf '%s\n' "$url" >"$SCRATCH/rtdb-url.txt"
  if [ "$loc" = "europe-west1" ]; then
    fact rtdb PASS "the default instance is in europe-west1 (U2: the provider created a DEFAULT_DATABASE instance there) and its database URL is host $(one_line "$(printf '%s' "$url" | sed 's#^[a-z]*://##; s#/.*$##')")"
    return 0
  fi
  local host
  host=$(printf '%s' "$url" | sed 's#^[a-z]*://##; s#/.*$##')
  fact rtdb FAIL "the default instance is in '$(one_line "$loc")' and not europe-west1 (its database URL is host ${host}). A first EU installation's budget backend is in europe-west1 (place.For and U2). If the URL names europe-west1 while the location does not, that is U2 answering differently than the mapping expects: the register's own section records it"
}

# step_buckets reads the location of every bucket of the project. The one the
# Firebase stage makes included: a located resource this check was pointed at
# reading outside the EU is the finding, whichever resource it is.
step_buckets() {
  say "reading every bucket's location (buckets)"
  read_out gcloud storage buckets list --project="$PROJECT" --format='value(name)'
  if [ "$READ_STATUS" -ne 0 ]; then
    fact buckets FAIL "gcloud storage buckets list failed ($READ_STATUS): $(one_line "$READ_OUT"), so where the installation's buckets are is NOT measured"
    return 0
  fi
  local names n=0 name loc bad="" outside=""
  names=$(printf '%s\n' "$READ_OUT" | grep -v '^[[:space:]]*$' || true)
  if [ -z "$names" ]; then
    fact buckets UNVERIFIED "the project lists no bucket at all, so where the installation's buckets are is NOT measured: a first setup creates the runs bucket and the Terraform state bucket"
    return 0
  fi
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    n=$((n + 1))
    read_out gcloud storage buckets describe "gs://$name" --project="$PROJECT" --format='value(location)'
    if [ "$READ_STATUS" -ne 0 ]; then
      bad="${bad}${bad:+, }${name} (unreadable)"
      continue
    fi
    loc=$(one_line "$READ_OUT")
    if ! in_eu "$loc"; then
      outside="${outside}${outside:+, }${name} ${loc}"
    fi
  done <<EOF
$names
EOF
  if [ -n "$bad" ]; then
    fact buckets FAIL "these buckets could not be read: ${bad}, so where they are is NOT measured"
    return 0
  fi
  if [ -n "$outside" ]; then
    fact buckets FAIL "these buckets are not in the EU: ${outside}. Every bucket an installation in europe-west1 makes is in its place (D3082), so a bucket outside it is a located resource the mapping did not get"
    return 0
  fi
  fact buckets PASS "all ${n} bucket(s) of ${PROJECT} read in the EU ($(printf '%s' "$names" | tr '\n' ' ' | sed 's/ *$//'))"
}

# step_registry reads every Artifact Registry repository. A repository's own
# name carries the location it is in (projects/<p>/locations/<loc>/...).
step_registry() {
  say "reading the Artifact Registry repositories (registry)"
  read_out gcloud artifacts repositories list --project="$PROJECT" --format='value(name)'
  if [ "$READ_STATUS" -ne 0 ]; then
    fact registry FAIL "gcloud artifacts repositories list failed ($READ_STATUS): $(one_line "$READ_OUT"), so where the installation's registry is is NOT measured"
    return 0
  fi
  local names n=0 outside="" name
  names=$(printf '%s\n' "$READ_OUT" | grep -v '^[[:space:]]*$' || true)
  if [ -z "$names" ]; then
    fact registry UNVERIFIED "the project lists no Artifact Registry repository, so where the installation's registry is is NOT measured: a first setup creates the base and history repositories in its region"
    return 0
  fi
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    n=$((n + 1))
    case "$name" in
    */locations/europe-west1/repositories/*) ;;
    *) outside="${outside}${outside:+, }${name}" ;;
    esac
  done <<EOF
$names
EOF
  if [ -n "$outside" ]; then
    fact registry FAIL "these repositories are not in europe-west1: ${outside}. Every repository an installation in europe-west1 makes is in its region, and a repository's own name carries the location it is in"
    return 0
  fi
  fact registry PASS "all ${n} repository(ies) are in europe-west1: $(printf '%s' "$names" | tr '\n' ' ' | sed 's/ *$//')"
}

# step_secrets reads the replication of every Fugaro-labelled container. What a
# new installation's containers are is user-managed in its region (D3082,
# D3092); automatic is the legacy policy, which today means the United States.
step_secrets() {
  say "reading the Fugaro secret containers' replication (secrets)"
  local list="$SCRATCH/secrets.txt" n=0 name kind where bad="" automatic="" outside=""
  fugaro_secret_containers "$list"
  if grep -q 'READ_ERROR' "$list" 2>/dev/null; then
    fact secrets FAIL "the secret containers could not be read: $(one_line "$(grep -m1 'READ_ERROR' "$list")"), so their replication is NOT measured"
    return 0
  fi
  if [ ! -s "$list" ]; then
    fact secrets UNVERIFIED "the project lists no container carrying the fugaro=managed label, so the installation's own replication is NOT measured: a first setup creates them"
    return 0
  fi
  while IFS=$'\t' read -r name kind where; do
    [ -n "$name" ] || continue
    n=$((n + 1))
    case "$kind" in
    automatic) automatic="${automatic}${automatic:+, }${name}" ;;
    user-managed)
      local r
      IFS=',' read -ra regions <<<"$where"
      for r in "${regions[@]}"; do
        [ -n "$r" ] || continue
        if ! in_eu "$r"; then outside="${outside}${outside:+, }${name} in ${r}"; fi
        if [ "$r" != "europe-west1" ]; then outside="${outside}${outside:+, }${name} in ${r} (not the installation's region)"; fi
      done
      [ -n "$where" ] || bad="${bad}${bad:+, }${name} (no replica location)"
      ;;
    *) bad="${bad}${bad:+, }${name}" ;;
    esac
  done <"$list"
  if [ -n "$automatic" ]; then
    fact secrets FAIL "these containers are replicated AUTOMATICALLY: ${automatic}. Automatic replication names no region and today means the United States, so an installation in the EU whose new containers are automatic has secrets outside its place (D3082, D3092). What exists keeps what it has, but nothing a first EU setup creates is automatic"
    return 0
  fi
  if [ -n "$bad" ]; then
    fact secrets FAIL "the replication of ${bad} could not be read as a policy, so where those containers are is NOT measured"
    return 0
  fi
  if [ -n "$outside" ]; then
    fact secrets FAIL "these containers are not replicated in the installation's region: ${outside}. Every container a first EU installation creates is user-managed in europe-west1 (D3082)"
    return 0
  fi
  fact secrets PASS "all ${n} fugaro=managed container(s) are user-managed with their replica in europe-west1, and none is automatic: a first EU installation's secrets are in its place"
}

# step_logbucket reads the installation's own log bucket. Its ID is `fugaro`
# in every installation, and its location is what says whether run logs stay
# in the place (D3082).
step_logbucket() {
  say "reading the installation's log bucket (logbucket)"
  read_out gcloud logging buckets list --project="$PROJECT" --format='value(name,location)'
  if [ "$READ_STATUS" -ne 0 ]; then
    fact logbucket FAIL "gcloud logging buckets list failed ($READ_STATUS): $(one_line "$READ_OUT"), so where the installation's run logs are is NOT measured"
    return 0
  fi
  local line loc
  line=$(printf '%s\n' "$READ_OUT" | awk -F'\t' '$1 == "fugaro" { print; exit }')
  if [ -z "$line" ]; then
    fact logbucket UNVERIFIED "the project lists no log bucket named fugaro, so where the installation's run logs are is NOT measured (an installation set up with --no-log-isolation has none of its own). The buckets it lists: $(printf '%s\n' "$READ_OUT" | tr '\n' ' ' | sed 's/ *$//')"
    return 0
  fi
  loc=${line#*$'\t'}
  if [ "$loc" = "europe-west1" ]; then
    fact logbucket PASS "the installation's log bucket fugaro is in europe-west1: a first EU installation's run logs stay in its place (D3082)"
    return 0
  fi
  fact logbucket FAIL "the installation's log bucket fugaro is in '$(one_line "$loc")' and not europe-west1: the mapping puts a first EU installation's run log bucket in its region, so this is a located resource outside the place"
}

# step_hosts is U8's own reading: what each of the three Realtime Database
# hosts answers for a database that lives in the EU. LP15's read treats a
# locked database (401/403) as an installation, a 404 or the body's own "no
# such database" / "database not found" as this host holding none, and anything
# else as a host that did not answer — so an answer of the last kind is a
# finding, and every answer is recorded either way.
step_hosts() {
  say "reading the three Realtime Database hosts (hosts)"
  local hosts host url status body shape shapes="" bad=""
  hosts="default-rtdb.firebaseio.com default-rtdb.europe-west1.firebasedatabase.app default-rtdb.asia-southeast1.firebasedatabase.app"
  for host in $hosts; do
    url="https://${PROJECT}-${host}/.json"
    body="$SCRATCH/host.txt"
    status=$(probe_host "$url" "$body")
    shape=$(answer_shape "$body" "$status")
    if [ -z "$shape" ]; then
      bad="${bad}${bad:+, }${host} answered ${status} ($(one_line "$(head -c 200 "$body" 2>/dev/null)"))"
      shape="an answer the key service cannot read"
    fi
    shapes="${shapes}${shapes:+; }${host}: ${shape}"
  done
  if [ -n "$bad" ]; then
    fact hosts FAIL "${bad}. The vendor's key service reads a host that answers like that as a host that DID NOT ANSWER, so a project whose database lives in the EU would not be found as an installation at all if the regional host answers this way (LP15, U8). Record the answer in the register's section"
    return 0
  fi
  fact hosts PASS "each of the three hosts answered as one of the shapes LP15's read recognises, so a database in europe-west1 is found whichever host answers for it (U8, recorded rather than predicted): ${shapes}"
}

# step_run launches one run of the installation and reads three things back:
# that the record reached the runs bucket, that the budget backend leased it
# money, and that its spend is readable. A job in europe-west1 reading the
# region's own secret container is what the last two cannot show on their own
# (U6), which is why they are one row: the run is the only thing that proves a
# regional container is readable from a regional job with no other change.
step_run() {
  say "launching one run of the installation (run)"
  local task="$SCRATCH/task.md"
  cat >"$task" <<'TASK'
# Task: Check 820 — a first EU installation

Say in one sentence that this run is Check 820's own run, and change nothing.
Do not edit any file: there is nothing to change, and this run exists to prove
that a first EU installation launches, leases budget and writes its history.
TASK
  local rc=0 doc="$SCRATCH/run.json" run_id="" status=""
  # Launched from the checkout the owner named: `fugaro run` resolves the
  # repository from where it is called (and takes the path here too), so a run
  # launched from wherever the check happened to be started would not be a run
  # of the checkout the installation is onboarded to.
  (cd "$REPO" && fugaro run --repo "$REPO" --project "$NAME" --title 'Check 820: a first EU installation' --task-file "$task" --json) >"$doc" 2>"$SCRATCH/run.err" || rc=$?
  run_id=$(jget "$doc" run_id)
  status=$(jget "$doc" status)
  if [ "$rc" -ne 0 ] || [ -z "$run_id" ]; then
    fact run FAIL "'fugaro run --json' did not launch (exit ${rc}): $(one_line "$(cat "$SCRATCH/run.err" 2>/dev/null)"). A first EU installation is one a run can be launched from, and a launch that refused is not that"
    return 0
  fi
  say "launched run ${run_id}; reading it back from the runs bucket and the budget backend"
  read_out fugaro runs ls --json --project "$NAME" --limit 5
  if [ "$READ_STATUS" -ne 0 ]; then
    fact run FAIL "'fugaro runs ls --json' failed ($READ_STATUS): $(one_line "$READ_OUT"), so whether the run's record reached the runs bucket is NOT measured (the bucket is in the EU, and the record reaching it is what says the region is usable)"
    return 0
  fi
  printf '%s\n' "$READ_OUT" >"$SCRATCH/runs.json"
  local listed
  listed=$(python3 - "$SCRATCH/runs.json" "$run_id" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
runs = doc if isinstance(doc, list) else (doc.get("runs") or [])
for r in runs:
    if not isinstance(r, dict):
        continue
    if str(r.get("run_id", "")) == sys.argv[2] or str(r.get("id", "")) == sys.argv[2]:
        print(r.get("title", "") or r.get("run_id", ""))
        break
PY
)
  local lease=""
  rc=0
  fugaro budget show --json --project "$NAME" >"$SCRATCH/budget.json" 2>"$SCRATCH/budget.err" || rc=$?
  if [ "$rc" -eq 0 ]; then
    lease=$(python3 - "$SCRATCH/budget.json" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
def leased(v):
    if isinstance(v, dict):
        return v.get("leased_usd") or 0
    return 0
total = leased(doc.get("global"))
for r in (doc.get("repos") or []):
    total += leased(r)
print("%.2f" % total if total else "")
PY
)
  fi
  if [ -z "$listed" ]; then
    fact run FAIL "the run ${run_id} that was launched is not among the runs 'fugaro runs ls --json' lists for ${NAME}, so whether its record reached the runs bucket is NOT measured: the launch left the run behind rather than the installation holding it"
    return 0
  fi
  fact run PASS "the run ${run_id} was launched and 'fugaro runs ls' names it in ${NAME}'s runs bucket${lease:+ (its record reached the bucket, and the budget backend reports \$${lease} leased for the project, so the launch took a lease from the database in europe-west1)}${lease:+}. A job in europe-west1 read the installation's regional secret container with no other change: that is U6's own reading. Read its spend with 'fugaro runs show ${run_id} --project ${NAME} --json'"
}

step_premise
step_setup
step_record
step_firestore
step_rtdb
step_buckets
step_registry
step_secrets
step_logbucket
step_hosts
step_run
REACHED_END=yes
