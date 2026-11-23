#!/usr/bin/env bash
# Check 890 — the data layout of a real project. Run by YOU.
#
# The repo-coupling audit (docs/repo-coupling-audit/report.md) names every
# storage path a project keys by repository: rows RC40–RC50 and RC52–RC56. A
# static reading of the code cannot say whether a real project holds anything
# else keyed by a repository, so this check lists the project's three stores,
# keys only, and compares what it finds with those rows:
#
#   rtdb       the Realtime Database: the top-level keys, one level under every
#              top-level key, one level under each child of config/ and creds/,
#              and the parent of the slug segment of every RTDB row of the
#              report (config/caps/repos, config/kill/repos, config/policy/repos,
#              config/policy/voice/repos, config/policy/route, creds/repoauth,
#              creds/repoaccess, runs, spend/<today>/repos, agents,
#              outcomes/<today>, buildleases, coordinator/repos,
#              spend/<today>/runs). Every read is shallow=true and the check
#              prints keys, never a value.
#   gcs        the runs bucket: its root, and two levels under runs/, builds/,
#              cache/, locks/, tokens/ and reports/ (one level under any other
#              top-level prefix).
#   firestore  the Firebase project's Firestore: the collection ids
#              (documents:listCollectionIds) and the first page of spendDaily's
#              document ids, through a list whose mask names no field.
#   layout     every listed path with a repository slug in it (marked [slug])
#              must match one of the patterns below; one that matches none is
#              printed UNNAMED and the row is a FAIL naming it.
#   restore    the check's scratch directory, which held the token file and
#              every response body, is gone.
#
# A level keyed per repository or per run is sampled: the check descends into at
# most --sample N of its keys (default 3) and prints at most N of them with the
# count, so runs/ does not enumerate every run.
#
# Usage:      bash scripts/live-checks/check-890-data-layout.sh <fugaro-project> \
#               [--sample N] [--config <file>] [--dry-run]
#             <fugaro-project>  the Fugaro project's name (e.g. fugaro); its
#                               local config names the GCP project, the runs
#                               bucket, the database and the Firebase project.
#             --config          that config, when it is not
#                               $XDG_CONFIG_HOME/fugaro/projects/<name>.yaml.
#             --dry-run         prints the plan and calls nothing.
# Needs:      bash, mktemp, python3, gcloud, curl.
# Calls:      gcloud auth print-access-token --quiet; gcloud storage ls --quiet;
#             curl GET <database>/<path>.json?shallow=true; curl POST
#             .../documents:listCollectionIds; curl GET .../documents/spendDaily
#             with mask.fieldPaths; both Firestore calls carry
#             X-Goog-User-Project, from a second curl config file. Every call
#             runs with stdin from /dev/null.
#             Nothing is written, launched or deleted.
# Costs:      a few hundred reads at most.
# Runs:       by YOU. The check refuses to start in a coding agent's session
#             (CLAUDECODE, CLAUDE_CODE_ENTRYPOINT, CLAUDE_CODE_REMOTE,
#             CURSOR_AGENT, AI_AGENT); its own test
#             (scripts/live-checks/check-890-data-layout_test.sh) drives it
#             against fake gcloud, curl and fugaro on PATH.
# Output:     progress on stderr, ONE block on stdout headed CHECK 890 RESULT.
#             Its last line is the verdict, and the verdict is the exit status:
#               PASS (exit 0)        every row passed
#               FAIL (exit 1)        a row was measured and failed
#               INCOMPLETE (exit 2)  the run stopped before it measured
#               INCOMPLETE (exit 3)  at least one row reads UNVERIFIED
#
# The access token never reaches an argv: it is written into a 0600 curl config
# file inside a 0700 scratch directory that the exit trap removes.
set -euo pipefail

PROG=${0##*/}
say() { printf '%s: %s\n' "$PROG" "$*" >&2; }

XTRACE_AT_START=no
case "${-:-}" in *x*) XTRACE_AT_START=yes ;; esac
{ set +x; } 2>/dev/null

# One line per storage-path row of report.md: the row, the store, the pattern.
# <slug> is a repository's task slug, <day> a day key, <run> a run id,
# <date>_<slug> spendDaily's document id; <wf>, <id> and <entry> are any segment
# that is not itself repo-keyed, and ** is anything below. The literal patterns
# of a prefix come before its wildcard ones, so a path is credited to the row
# that names it. Rows that are not storage paths (RC51, RC57–RC59, RC95) are left
# out on purpose.
PATTERNS='
RC40 rtdb config/caps/repos/<slug>/**
RC41 rtdb config/kill/repos/<slug>/**
RC42 rtdb runs/<slug>/<run>/**
RC42 rtdb spend/<day>/runs/<slug>/<run>/**
RC42 rtdb agents/<slug>/<run>/**
RC42 rtdb outcomes/<day>/<slug>/<run>/**
RC43 rtdb spend/<day>/repos/<slug>/**
RC44 rtdb buildleases/<slug>/<id>/**
RC45 rtdb config/policy/repos/<slug>/**
RC46 rtdb config/policy/voice/repos/<slug>/**
RC47 rtdb config/policy/route/<slug>/**
RC48 rtdb creds/repoauth/<slug>/**
RC48 rtdb creds/repoaccess/<slug>/**
RC49 rtdb coordinator/repos/<slug>/**
RC99 rtdb creds/workflowsecrets/<slug>/**
RC100 rtdb creds/leases/<slug>/<run>/**
RC101 rtdb creds/review-signs/<slug>/<run>/**
RC102 rtdb creds/issuance/<slug>/<run>/**
RC106 rtdb creds/current/<slug>/<run>/**
RC50 firestore spendDaily/<date>_<slug>
RC52 gcs runs/<slug>/pr-index/**
RC52 gcs runs/<slug>/<run>/**
RC53 gcs cache/<slug>/**
RC53 gcs locks/<slug>/**
RC53 gcs tokens/<slug>/**
RC53 gcs reports/<slug>/**
RC56 gcs builds/<slug>/project-layer.yaml
RC55 gcs builds/<slug>/catalog/<entry>/**
RC54 gcs builds/<slug>/<wf>/image.json
RC54 gcs builds/<slug>/<wf>/check.json
'

GCS_PREFIXES="runs builds cache locks tokens reports"
RTDB_DEEPER="config creds"
# A spendDaily mask that names a field no document carries returns each
# document's name and none of its fields.
FS_NO_FIELD=chk890NoSuchField
SLUG_RE='^[a-z0-9]+(-[a-z0-9]+)*-[0-9a-f]{16}$'
RUN_RE='^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$'
LITERAL_CAP=40
CURL_SECONDS=${CHECK890_CURL_SECONDS:-30}

FACTS=""
SCRATCH=""
SCRATCH_REMOVED=no
REACHED_END=no
ABORT=""
AUTH_CONF=""
LISTING=""
BLOCK_BODY=""

stop() {
  printf '%s: stopped: %s\n' "$PROG" "$1" >&2
  ABORT=$1
  exit 2
}

fact() { # fact KEY PASS|FAIL|UNVERIFIED|INFO "detail"
  [ -n "$FACTS" ] || return 0
  printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$FACTS"
}

usage() {
  cat <<'USAGE'
Check 890 — the data layout of a real project. Run by YOU; it only reads.

Usage:
  bash scripts/live-checks/check-890-data-layout.sh <fugaro-project> [--sample N] [--config <file>] [--dry-run]
  bash scripts/live-checks/check-890-data-layout.sh --help

It lists the project's Realtime Database (shallow, keys only), its runs bucket
and its Firestore collection and spendDaily document ids, marks every path with a
repository slug in it, and fails on one that report.md's storage-path rows
(RC40–RC50, RC52–RC56) do not name. The last line is the verdict and the exit
status: PASS (0), FAIL (1), INCOMPLETE (2: stopped; 3: a row UNVERIFIED).
USAGE
}

PROJECT=""
CONFIG=""
SAMPLE=3
DRY_RUN=no
while [ $# -gt 0 ]; do
  case "$1" in
  --help | -h)
    usage
    exit 0
    ;;
  --sample)
    [ $# -ge 2 ] || stop "--sample needs a number"
    SAMPLE=$2
    shift 2
    ;;
  --config)
    [ $# -ge 2 ] || stop "--config needs the path of the project's local config"
    CONFIG=$2
    shift 2
    ;;
  --dry-run)
    DRY_RUN=yes
    shift
    ;;
  -*) stop "unknown argument $1 (usage: check-890-data-layout.sh <fugaro-project> [--sample N] [--config <file>] [--dry-run])" ;;
  *)
    [ -z "$PROJECT" ] || stop "one project only: '$PROJECT' and '$1' were both given"
    PROJECT=$1
    shift
    ;;
  esac
done

[ -n "$PROJECT" ] || stop "no project was given (usage: check-890-data-layout.sh <fugaro-project>, or --help)"
printf '%s' "$PROJECT" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9._-]*$' || stop "'$PROJECT' is not a Fugaro project name"
printf '%s' "$SAMPLE" | grep -Eq '^[1-9][0-9]{0,2}$' || stop "--sample '$SAMPLE' is not a number from 1 to 999"
[ -n "$CONFIG" ] || CONFIG=${XDG_CONFIG_HOME:-${HOME:-}/.config}/fugaro/projects/${PROJECT}.yaml

if [ "$XTRACE_AT_START" = yes ]; then
  stop "this shell is tracing (bash -x), and the trace would print the access token. Run it without -x"
fi
for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
  if [ -n "$(eval "printf '%s' \"\${$marker:-}\"")" ]; then
    stop "this shell carries $marker, so it is a coding agent's session, and no agent runs Check 890: it reads a real project. Run it yourself"
  fi
done

if [ -n "${CHECK890_DAY:-}" ]; then
  printf '%s' "$CHECK890_DAY" | grep -Eq '^[0-9]+$' || stop "CHECK890_DAY '$CHECK890_DAY' is not a day key"
  DAY=$CHECK890_DAY
else
  DAY=$(($(date -u +%s) / 86400))
fi

RTDB_PARENTS="config/caps/repos config/kill/repos config/policy/repos config/policy/voice/repos config/policy/route creds/repoauth creds/repoaccess runs spend/$DAY/repos agents outcomes/$DAY buildleases coordinator/repos spend/$DAY/runs"

if [ "$DRY_RUN" = yes ]; then
  cat <<PLAN
Check 890 plan (nothing is called)

  project: $PROJECT
  config:  $CONFIG (gcp_project, runs_bucket or bucket_url, budget.rtdb_url, budget.firebase_project)
  sample:  $SAMPLE keys at a per-repository or per-run level

  gcloud auth print-access-token --quiet         into a 0600 curl config file, never printed
  curl GET <rtdb>/.json?shallow=true             the top-level keys, then one level under each,
                                                 under each child of: $RTDB_DEEPER
  curl GET <rtdb>/<parent>.json?shallow=true     for: $RTDB_PARENTS
  gcloud storage ls gs://<bucket>/ --quiet        and two levels under: $GCS_PREFIXES
  curl POST <firestore>/documents:listCollectionIds
  curl GET  <firestore>/documents/spendDaily?pageSize=$SAMPLE&mask.fieldPaths=$FS_NO_FIELD
PLAN
  exit 0
fi

for tool in mktemp python3 gcloud curl; do
  command -v "$tool" >/dev/null 2>&1 || stop "$tool is not on PATH, and this check reads the project with it"
done

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk890.XXXXXXXX") || stop "cannot make a scratch directory"
chmod 700 "$SCRATCH"
MARKER="$SCRATCH/.chk890-mine"
: >"$MARKER"
FACTS="$SCRATCH/facts"
: >"$FACTS"
LISTING="$SCRATCH/listing"
: >"$LISTING"
: >"$SCRATCH/rtdb.read"
AUTH_CONF="$SCRATCH/auth.conf"
FS_CONF="$SCRATCH/firestore.conf"
say "scratch directory: $SCRATCH (mode 0700, removed at the end)"

cleanup() {
  [ -n "$SCRATCH" ] || return 0
  [ -f "$MARKER" ] || {
    say "refusing to remove $SCRATCH: it carries no marker from this check"
    return 0
  }
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
    if [ "$status" -eq 0 ] || [ "$status" -eq 1 ]; then status=2; fi
  fi
  if [ "$SCRATCH_REMOVED" = yes ]; then
    facts="${facts}"$'\n'"restore|PASS|the scratch directory, with the token file and every response body, is gone"
  else
    facts="${facts}"$'\n'"restore|FAIL|the scratch directory is STILL THERE, at ${SCRATCH}: it holds the access token file; remove it yourself"
  fi
  if [ "$REACHED_END" = yes ] && [ "$status" -eq 0 ]; then
    case "$(verdict "$facts")" in
    PASS) status=0 ;;
    FAIL) status=1 ;;
    *) status=3 ;;
    esac
  fi
  printf '\nCHECK 890 RESULT\n'
  [ -z "$BLOCK_BODY" ] || printf '%s\n' "$BLOCK_BODY"
  printf '%s\n' "$facts" | while IFS='|' read -r key state detail; do
    [ -n "$key" ] || continue
    printf '  %-10s %-11s %s\n' "$key" "$state" "$detail"
  done
  printf 'CHECK 890 RESULT: %s (exit %s)\n' "$(verdict "$facts")" "$status"
  exit "$status"
}
trap finish EXIT

# read_config prints name=value for the fields this check needs, out of the
# project's local config: the same file `fugaro` resolves the project from, and
# the one check-300 and check-314 read the database from. It is a scan of the
# file's indentation so it needs no YAML library.
read_config() {
  python3 - "$CONFIG" <<'PY'
import re, sys
try:
    lines = open(sys.argv[1]).read().splitlines()
except Exception:
    sys.exit(1)
sec = ""
child = None
want_top = {"gcp_project", "runs_bucket", "bucket_url"}
want_budget = {"rtdb_url", "firebase_project"}
for line in lines:
    if not line.strip() or line.lstrip().startswith("#"):
        continue
    indent = len(line) - len(line.lstrip())
    m = re.match(r"\s*([A-Za-z0-9_.-]+):\s*(.*?)\s*(?:#.*)?$", line)
    if not m:
        continue
    key, value = m.group(1), m.group(2).strip().strip("\"'")
    if indent == 0:
        sec, child = key, None
        if key in want_top and value:
            print("%s=%s" % (key, value))
        continue
    # fugaro writes the file with four-space indentation and a person may use
    # two: a section's direct children are at whatever indent its first child
    # has, and anything deeper belongs to a nested map or list.
    if child is None:
        child = indent
    if indent == child and sec == "budget" and key in want_budget and value:
        print("%s=%s" % (key, value))
PY
}

config_field() { printf '%s\n' "$CONFIG_FIELDS" | sed -n "s/^$1=//p" | head -1; }

step_config() {
  say "reading the project's local config ($CONFIG)"
  [ -r "$CONFIG" ] || {
    fact config FAIL "no readable local config at $CONFIG: give it with --config"
    stop "no local config for project $PROJECT"
  }
  CONFIG_FIELDS=$(read_config) || stop "$CONFIG could not be read"
  GCP=$(config_field gcp_project)
  BUCKET=$(config_field runs_bucket)
  if [ -z "$BUCKET" ]; then
    BUCKET=$(config_field bucket_url)
    BUCKET=${BUCKET#gs://}
    BUCKET=${BUCKET%%/*}
  fi
  RTDB=$(config_field rtdb_url)
  RTDB=${RTDB%/}
  FP=$(config_field firebase_project)
  local fp_from="budget.firebase_project"
  if [ -z "$FP" ]; then
    FP=$GCP
    fp_from="gcp_project (the file names no budget.firebase_project, the single-project layout)"
  fi
  printf '%s' "$GCP" | grep -Eq '^[a-z][a-z0-9-]{4,28}[a-z0-9]$' || stop "$CONFIG names no GCP project id (gcp_project '${GCP}')"
  printf '%s' "$FP" | grep -Eq '^[a-z][a-z0-9-]{4,28}[a-z0-9]$' || stop "$CONFIG names no Firebase project id ('${FP}')"
  printf '%s' "$BUCKET" | grep -Eq '^[a-z0-9][a-z0-9._-]{1,220}[a-z0-9]$' || stop "$CONFIG names no runs bucket (runs_bucket or a gs:// bucket_url)"
  printf '%s' "$RTDB" | grep -Eq '^https://[a-z0-9-]+(\.[a-z0-9-]+)*\.(firebaseio\.com|firebasedatabase\.app)$' ||
    stop "$CONFIG names no Realtime Database URL (budget.rtdb_url '${RTDB}'): run 'fugaro update' to write it"
  fact config PASS "$CONFIG: GCP project $GCP, runs bucket gs://$BUCKET, database $RTDB, Firestore in $FP (from $fp_from); today's day key $DAY"
}

mint_token() {
  local tok
  tok=$(gcloud auth print-access-token --quiet </dev/null) || return 1
  [ -n "$tok" ] || return 1
  (umask 077 && printf 'header = "Authorization: Bearer %s"\n' "$tok" >"$AUTH_CONF") || return 1
  chmod 600 "$AUTH_CONF" || return 1
  unset tok
}

step_token() {
  if ! mint_token; then
    fact token FAIL "no access token could be minted with gcloud: run 'gcloud auth login' and this check again"
    stop "gcloud auth print-access-token did not answer"
  fi
  fact token PASS "an access token was minted into a 0600 file in the 0700 scratch directory, never printed or put on a command line"
}

# http_get URL OUT [curl args...] prints the HTTP status, or 000 when curl got
# no answer at all. Only the status is ever shown: a body is a database answer.
http_get() {
  local url=$1 out=$2 code
  shift 2
  code=$(curl -sS -g --max-time "$CURL_SECONDS" --config "$AUTH_CONF" "$@" -o "$out" -w '%{http_code}' "$url" </dev/null 2>"$SCRATCH/curl.err") || code=000
  printf '%s' "$code"
}

# keys_of FILE prints an RTDB shallow answer's keys. A primitive leaf comes back
# verbatim from a shallow read, so it is never printed: a leaf has no keys.
keys_of() {
  python3 - "$1" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
if isinstance(doc, dict):
    for k in sorted(doc):
        print(k)
PY
}

url_seg() {
  case "$1" in
  *[!A-Za-z0-9_-]*) python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "$1" ;;
  *) printf '%s' "$1" ;;
  esac
}

url_path() {
  local IFS=/ seg out=""
  for seg in $1; do
    out="${out}${out:+/}$(url_seg "$seg")"
  done
  printf '%s' "$out"
}

RTDB_ERRORS=""
RTDB_READS=0
rtdb_list() { # rtdb_list <path, "" for the root>
  local path=$1 out="$SCRATCH/body" code
  if grep -qxF -- "=$path" "$SCRATCH/rtdb.read"; then return 0; fi
  printf '=%s\n' "$path" >>"$SCRATCH/rtdb.read"
  code=$(http_get "$RTDB/$(url_path "$path").json?shallow=true" "$out")
  RTDB_READS=$((RTDB_READS + 1))
  if [ "$code" != 200 ]; then
    RTDB_ERRORS="${RTDB_ERRORS}${RTDB_ERRORS:+; }${path:-/} (HTTP $code)"
    rm -f "$out"
    return 1
  fi
  keys_of "$out" | while IFS= read -r k; do printf 'rtdb\t%s\t%s\n' "$path" "$k"; done >>"$LISTING"
  printf 'rtdb\t%s\t\n' "$path" >>"$LISTING"
  rm -f "$out"
}

children() { # children STORE PARENT
  awk -F'\t' -v s="$1" -v p="$2" '$1 == s && $2 == p && $3 != "" { print $3 }' "$LISTING" | sort -u
}

keyed() {
  [[ $1 =~ $SLUG_RE ]] || [[ $1 =~ $RUN_RE ]] || [[ $1 =~ ^[0-9]+$ ]] || [[ $1 == *%2[Ff]* ]]
}

# sampled STORE PARENT prints the children to descend into: every literal child
# (up to LITERAL_CAP) and at most SAMPLE keyed ones.
sampled() {
  local k nk=0 nl=0
  while IFS= read -r k; do
    [ -n "$k" ] || continue
    if keyed "$k"; then
      [ "$nk" -lt "$SAMPLE" ] || continue
      nk=$((nk + 1))
    else
      [ "$nl" -lt "$LITERAL_CAP" ] || continue
      nl=$((nl + 1))
    fi
    printf '%s\n' "$k"
  done <<EOF
$(children "$1" "$2")
EOF
}

step_rtdb() {
  say "listing the Realtime Database, keys only"
  if ! rtdb_list ""; then
    fact rtdb UNVERIFIED "the database's root could not be read (${RTDB_ERRORS}), so nothing in it is measured"
    return 0
  fi
  local top child p
  for top in $(sampled rtdb ""); do
    rtdb_list "$top" || true
  done
  for top in $RTDB_DEEPER; do
    for child in $(sampled rtdb "$top"); do
      rtdb_list "$top/$child" || true
    done
  done
  for p in $RTDB_PARENTS; do
    rtdb_list "$p" || true
  done
  if [ -n "$RTDB_ERRORS" ]; then
    fact rtdb UNVERIFIED "$RTDB_READS shallow reads, and these did not answer: $RTDB_ERRORS"
  else
    fact rtdb PASS "$RTDB_READS shallow reads (keys only) of $RTDB"
  fi
}

GCS_ERRORS=""
GCS_READS=0
gcs_ls() { # gcs_ls <prefix, "" for the root>
  local prefix=$1 url rc=0 line rest
  url="gs://$BUCKET/${prefix:+$prefix/}"
  GCS_READS=$((GCS_READS + 1))
  gcloud storage ls "$url" --quiet </dev/null >"$SCRATCH/ls.out" 2>"$SCRATCH/ls.err" || rc=$?
  if [ "$rc" -ne 0 ]; then
    if grep -q 'matched no objects' "$SCRATCH/ls.err"; then
      printf 'gcs\t%s\t\n' "$prefix" >>"$LISTING"
      return 0
    fi
    GCS_ERRORS="${GCS_ERRORS}${GCS_ERRORS:+; }${url} (exit $rc)"
    return 1
  fi
  while IFS= read -r line; do
    case "$line" in "$url"?*) ;; *) continue ;; esac
    rest=${line#"$url"}
    case "$rest" in
    */) printf 'gcs\t%s\t%s\tprefix\n' "$prefix" "${rest%/}" ;;
    */*) ;;
    *) printf 'gcs\t%s\t%s\tobject\n' "$prefix" "$rest" ;;
    esac
  done <"$SCRATCH/ls.out" >>"$LISTING"
  printf 'gcs\t%s\t\n' "$prefix" >>"$LISTING"
}

gcs_prefixes() { # gcs_prefixes PARENT: the sampled children that are prefixes
  local k
  for k in $(sampled gcs "$1"); do
    if awk -F'\t' -v p="$1" -v k="$k" '$1 == "gcs" && $2 == p && $3 == k && $4 == "prefix" { f = 1 } END { exit !f }' "$LISTING"; then
      printf '%s\n' "$k"
    fi
  done
}

step_gcs() {
  say "listing the runs bucket gs://$BUCKET"
  if ! gcs_ls ""; then
    fact gcs UNVERIFIED "the bucket's root could not be listed (${GCS_ERRORS}), so nothing in it is measured"
    return 0
  fi
  local top child
  for top in $(gcs_prefixes ""); do
    gcs_ls "$top" || continue
    case " $GCS_PREFIXES " in
    *" $top "*)
      for child in $(gcs_prefixes "$top"); do
        gcs_ls "$top/$child" || true
      done
      ;;
    esac
  done
  if [ -n "$GCS_ERRORS" ]; then
    fact gcs UNVERIFIED "$GCS_READS listings, and these did not answer: $GCS_ERRORS"
  else
    fact gcs PASS "$GCS_READS listings of gs://$BUCKET"
  fi
}

ids_of() { # ids_of FILE collections|documents
  python3 - "$1" "$2" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
if sys.argv[2] == "collections":
    for c in doc.get("collectionIds") or []:
        print(c)
else:
    for d in doc.get("documents") or []:
        print((d.get("name") or "").rsplit("/", 1)[-1])
PY
}

step_firestore() {
  say "listing Firestore in $FP, ids only"
  local base="https://firestore.googleapis.com/v1/projects/$FP/databases/(default)/documents"
  local out="$SCRATCH/body" code errors=""
  # Firestore bills and quota-checks a call to the project named here, as
  # internal/firestore/client.go does; the RTDB reads carry no such header.
  (umask 077 && printf 'header = "X-Goog-User-Project: %s"\n' "$FP" >"$FS_CONF")
  code=$(http_get "$base:listCollectionIds" "$out" --config "$FS_CONF" -X POST -H 'Content-Type: application/json' --data '{"pageSize":300}')
  if [ "$code" = 200 ]; then
    ids_of "$out" collections | while IFS= read -r k; do printf 'firestore\t\t%s\n' "$k"; done >>"$LISTING"
    printf 'firestore\t\t\n' >>"$LISTING"
  else
    errors="listCollectionIds (HTTP $code)"
  fi
  rm -f "$out"
  code=$(http_get "$base/spendDaily?pageSize=$SAMPLE&mask.fieldPaths=$FS_NO_FIELD" "$out" --config "$FS_CONF")
  if [ "$code" = 200 ]; then
    ids_of "$out" documents | while IFS= read -r k; do printf 'firestore\tspendDaily\t%s\n' "$k"; done >>"$LISTING"
    printf 'firestore\tspendDaily\t\n' >>"$LISTING"
  else
    errors="${errors}${errors:+; }spendDaily list (HTTP $code)"
  fi
  rm -f "$out"
  if [ -n "$errors" ]; then
    fact firestore UNVERIFIED "these Firestore reads did not answer: $errors"
  else
    fact firestore PASS "the collection ids and the first $SAMPLE spendDaily document ids of $FP's (default) database, no field read"
  fi
}

# classify prints the block's listing and repo-keyed sections, then one line
# "UNNAMED <store> <path>" per repo-keyed path no pattern names, after a
# separator line.
classify() {
  python3 - "$LISTING" "$SAMPLE" <<'PY' 3<<<"$PATTERNS"
import os, re, sys
listing, sample = sys.argv[1], int(sys.argv[2])
SLUG = re.compile(r"^[a-z0-9]+(?:-[a-z0-9]+)*-[0-9a-f]{16}$")
DATE_SLUG = re.compile(r"^\d{4}-\d{2}-\d{2}_[a-z0-9]+(?:-[a-z0-9]+)*-[0-9a-f]{16}$")
RUN = re.compile(r"^\d{8}-\d{6}-[0-9a-f]{4}$")
REPO = {"<slug>", "<date>_<slug>", "<repo>"}
ANY = {"<wf>", "<id>", "<entry>"}

def norm(segs):
    out = []
    for i, s in enumerate(segs):
        if SLUG.match(s):
            out.append("<slug>")
        elif DATE_SLUG.match(s):
            out.append("<date>_<slug>")
        elif "%2f" in s.lower():
            out.append("<repo>")
        elif RUN.match(s):
            out.append("<run>")
        elif i == 1 and segs[0] in ("spend", "outcomes") and s.isdigit():
            out.append("<day>")
        else:
            out.append(s)
    return out

def matches(q, p):
    for i, ps in enumerate(p):
        if i >= len(q):
            return False
        if q[i] == "**":
            return True
        if q[i] in ANY:
            if ps in REPO:
                return False
        elif q[i] != ps:
            return False
    return True

patterns = []
for line in os.fdopen(3).read().splitlines():
    f = line.split()
    if len(f) == 3:
        patterns.append((f[0], f[1], f[2].split("/")))

groups, order = {}, []
for line in open(listing).read().splitlines():
    f = line.split("\t")
    if len(f) < 3:
        continue
    store, parent, key = f[0], f[1], f[2]
    g = (store, parent)
    if g not in groups:
        groups[g] = []
        order.append(g)
    if key and key not in groups[g]:
        groups[g].append(key)

print("  listing (keys only; at most %d shown per node):" % sample)
for store, parent in order:
    keys = sorted(groups[(store, parent)])
    shown = ", ".join(keys[:sample]) + (", ..." if len(keys) > sample else "")
    noun = "key" if len(keys) == 1 else "keys"
    print("    %-5s %s  %d %s%s" % (store, parent or "/", len(keys), noun, ": " + shown if keys else ""))

# A path that ends at its repo-keyed segment is the shared prefix of every row
# below it, so it is credited to all of them; a deeper path to the first row
# that names it.
seen, done, unnamed, hit = [], set(), [], set()
for store, parent in order:
    for key in groups[(store, parent)]:
        segs = (parent.split("/") if parent else []) + [key]
        p = norm(segs)
        if not REPO.intersection(p):
            continue
        path = "/".join(p)
        if (store, path) in done:
            continue
        done.add((store, path))
        rcs = []
        for i, (prc, pstore, q) in enumerate(patterns):
            if pstore == store and matches(q, p):
                hit.add(i)
                if prc not in rcs:
                    rcs.append(prc)
                if p[-1] not in REPO:
                    break
        if not rcs:
            unnamed.append((store, path))
        seen.append((store, path, "/".join(rcs) or "UNNAMED"))

print("  repo-keyed paths:")
if not seen:
    print("    (none listed)")
for store, path, rc in seen:
    print("    [slug] %s %s %s" % (rc, store, path))
missing = ["%s %s %s" % (prc, pstore, "/".join(q)) for i, (prc, pstore, q) in enumerate(patterns) if i not in hit]
if missing:
    print("  named by the report, not seen here (no data, or below the levels listed):")
    for m in missing:
        print("    %s" % m)
print("--")
for store, path in unnamed:
    print("UNNAMED %s %s" % (store, path))
PY
}

step_layout() {
  say "comparing every repo-keyed path with report.md's storage-path rows"
  local result body unnamed
  result=$(classify) || {
    fact layout UNVERIFIED "the listing could not be classified"
    return 0
  }
  body=${result%%$'\n'--*}
  unnamed=$(printf '%s\n' "$result" | sed -n '/^--$/,$p' | sed -n 's/^UNNAMED //p')
  BLOCK_BODY=$body
  if [ -n "$unnamed" ]; then
    fact layout FAIL "repo-keyed paths no storage-path row of report.md (RC40–RC50, RC52–RC56) names: $(printf '%s\n' "$unnamed" | paste -sd ';' - | sed 's/;/; /g')"
  else
    fact layout PASS "every repo-keyed path listed matches a storage-path row of report.md"
  fi
}

step_config
step_token
step_rtdb
step_gcs
step_firestore
step_layout
REACHED_END=yes
