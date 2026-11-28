#!/usr/bin/env bash
# Tests for scripts/live-checks/check-841.sh, against fake gcloud, fugaro and
# curl on PATH: nothing here reaches the network, there is no cloud project, no
# Realtime Database and no credential, and no step of the live check is run.
#
# The fakes carry the whole of the reading the live check makes, so that each of
# its rows can be driven to the state it exists for:
#
#   * the fake gcloud answers `auth print-identity-token` (with the account it
#     was told to impersonate, which is the caller a ticket ask is attributed
#     to), `auth print-access-token`, `config get-value account` and the job
#     read;
#   * the fake fugaro is the installation's CLI, its state under $FAKE841_STATE:
#     `version`, `doctor --json`, `project policy show --json` (the published
#     node's version), `runs ls --json` (the runs this test launched),
#     `run` (a launch that asks for its ticket and claims it, through the fake
#     curl), `update` (which prints the line about the runs in flight and raises
#     the node) and `pair --json`;
#   * the fake curl is the credentials service AND the Realtime Database: the
#     ticket route writes the issuance record the live check reads, the claim
#     route marks it claimed, and a GET or a DELETE is one node of the database.
#     It keeps the ticket, the claim and the pairing payload of a request in
#     files, and every request is recorded, so a case can say what travelled.
#
# Usage: bash scripts/live-checks/check-841_test.sh
set -euo pipefail
# A umask of the ordinary kind: a case that measured the mode of a file the
# check writes would hold whatever the check did under an ambient 077.
umask 022

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-841.sh
# The PHYSICAL path, so a path this test compares and a path a child reports are
# spelled the same way.
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK841_TEST_KEEP:-}" ]; then
    echo "kept: $work" >&2
  else
    rm -rf "$work"
  fi
}
trap cleanup_all EXIT

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}
expect_contains() { # haystack needle what
  case "$1" in
  *"$2"*) ;;
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 600)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1; last block: $(printf '%s' "${out:-}" | tail -c 400)"
}
expect_row() { # block key state what
  local row
  row=$(printf '%s\n' "$1" | grep -E "^$2 +[A-Z]" | head -1 || true)
  [ -n "$row" ] || {
    fail "$4: the block carries no row for '$2'"
    return 0
  }
  case "$row" in
  *" $3 "*) ;;
  *) fail "$4: row '$2' is '$(printf '%s' "$row" | cut -c1-300)', expected the state $3" ;;
  esac
}
expect_block_keys() { # block what
  printf '%s\n' "$1" | awk 'NF >= 2 && $1 ~ /^[a-z][a-z-]*$/ { print $1 }' >"$work/block.keys"
  local key
  for key in "${BLOCK_KEYS[@]}"; do
    grep -qx -- "$key" "$work/block.keys" || fail "$2: the block carries no '$key' row"
  done
}

# The tree is the authority for the two numbers every fake answers with, exactly
# as the live check reads them: the policy version this release writes, and the
# release that introduced it.
versionfile=$here/../../internal/projectpolicy/version.go
POLICY_VERSION=$(sed -n 's/^[[:space:]]*\(const[[:space:]][[:space:]]*\)\?Version[[:space:]]*=[[:space:]]*\([0-9][0-9]*\).*/\2/p' "$versionfile" 2>/dev/null | head -1)
PREV_VERSION=$((POLICY_VERSION - 1))
RELEASE=$(sed -n "s/^[[:space:]]*$POLICY_VERSION:[[:space:]]*\"\([^\"]*\)\".*/\1/p" "$versionfile" 2>/dev/null | head -1)
[ -n "$POLICY_VERSION" ] && [ -n "$RELEASE" ] || {
  echo "check-841_test: the tree's policy version and release could not be read from ${versionfile}: this test drives the check with the numbers it reads there" >&2
  exit 1
}
# The sentence an older CLI prints, spelled the way the tree spells it: the
# shared sentence with both versions, plus the CLI's own remedy.
SKEW_FMT=$(sed -n 's/^[[:space:]]*\(const[[:space:]][[:space:]]*\)\?Skew[[:space:]]*=[[:space:]]*"\(.*\)"[[:space:]]*$/\2/p' "$versionfile" 2>/dev/null | head -1)
SKEW_SUFFIX=$(sed -n 's/^[[:space:]]*\(const[[:space:]][[:space:]]*\)\?CLISkew[[:space:]]*=[[:space:]]*Skew[[:space:]]*+[[:space:]]*"\(.*\)"[[:space:]]*$/\2/p' "$versionfile" 2>/dev/null | head -1)
# shellcheck disable=SC2059
UPGRADE_SENTENCE=$(printf "$SKEW_FMT" "$POLICY_VERSION" "$PREV_VERSION")${SKEW_SUFFIX}

# The installation this test drives: the live check's own project and region,
# which are literals of the script and of no flag.
PROJECT=fugaro-dev
REGION=us-east5
SLUG=fugaro-dev
JOB=fugaro-dev-job
JOB_ACCOUNT="fugaro-job@${PROJECT}.iam.gserviceaccount.com"
OWNER=owner@example.com
SECOND=other@example.com
RTDB=https://fake-rtdb.example
SERVICE=https://fake-creds.example

# Four sentinels, and no credential is handled anywhere in this test: the ID
# token the fake gcloud mints, the access token, the ticket the fake service
# issues (which carries its run) and the custom token a claim answers, plus the
# pairing payload. Every case asserts at least the first four never reach the
# block the check prints, which is what their being here is for.
ID_SENTINEL='SENTINEL-841-id-token-not-a-credential'
ACCESS_SENTINEL='SENTINEL-841-access-token-not-a-credential'
TICKET_PREFIX='SENTINEL-841-ticket'
CUSTOM_SENTINEL='SENTINEL-841-custom-token-not-a-credential'
PAIR_SENTINEL='SENTINEL-841-pairing-payload-not-a-credential'

# Every fact key a complete run's block must carry, whatever it measured.
BLOCK_KEYS=(xtrace owner belong tree installation premise run update asked
  inflight session second copy old-cli pair restore verdict)

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake gcloud for check-841.sh's test. Every call is recorded under
# $FAKE841_DIR; the token it mints is a sentinel and never a credential; the
# account a mint was made as is written to $FAKE841_STATE/caller, which is what
# the fake service attributes an ask to.
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"$FAKE841_DIR/calls"
me=$(cat "$FAKE841_STATE/owner" 2>/dev/null || printf 'owner@example.com')
case "${1:-} ${2:-}" in
"config get-value")
  case "${3:-}" in
  account) printf '%s\n' "$me" ;;
  project) printf '%s\n' "$FAKE841_PROJECT" ;;
  *) printf '\n' ;;
  esac
  ;;
"auth print-identity-token")
  who=$me
  for arg in "$@"; do
    case "$arg" in
    --impersonate-service-account=*) who=${arg#--impersonate-service-account=} ;;
    esac
  done
  printf '%s\n' "$who" >"$FAKE841_STATE/caller"
  printf '%s\n' 'SENTINEL-841-id-token-not-a-credential'
  ;;
"auth print-access-token")
  printf '%s\n' 'SENTINEL-841-access-token-not-a-credential'
  ;;
"run jobs")
  case "${3:-}" in
  describe)
    if [ ! -f "$FAKE841_DIR/job.json" ]; then
      printf 'ERROR: (gcloud.run.jobs.describe) NOT_FOUND: job %s not found\n' "${4:-}" >&2
      exit 1
    fi
    cat "$FAKE841_DIR/job.json"
    ;;
  *)
    printf 'fake gcloud: unhandled run subcommand: %s\n' "$*" >&2
    exit 1
    ;;
  esac
  ;;
*)
  printf 'fake gcloud: unhandled call: %s\n' "$*" >&2
  exit 1
  ;;
esac
GCLOUD_EOF

  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check-841.sh's test: the installation's own CLI, driven by
# the files under $FAKE841_STATE. A launch here is the whole chain the live
# check reads -- it asks the credentials service for its ticket through the fake
# curl, claims it as the run, and opens the ledger -- so the record the check
# reads is the one a launcher and a run between them wrote.
set -euo pipefail
state=$FAKE841_STATE
knobs=$FAKE841_KNOBS
printf 'fugaro %s\n' "$*" >>"$FAKE841_DIR/calls"
knob() { [ -f "$knobs/$1" ]; }

ask_ticket() { # ask_ticket RUN_ID
  local rid=$1 body="$FAKE841_DIR/ticket-$1.body" conf="$FAKE841_DIR/ticket-$1.conf"
  printf '{"slug":"%s","run":"%s","account":"%s","launcher":"%s","max_runtime_seconds":600}' \
    "$FAKE841_SLUG" "$rid" "$FAKE841_JOB_ACCOUNT" "$(cat "$state/owner" 2>/dev/null || printf 'owner@example.com')" >"$body"
  printf 'header = "Authorization: Bearer %s"\n' 'SENTINEL-841-id-token-not-a-credential' >"$conf"
  printf '%s\n' "$(cat "$state/owner" 2>/dev/null || printf 'owner@example.com')" >"$state/caller"
  curl -sS -o "$FAKE841_DIR/ticket-$1.json" -w '%%{http_code}' --config "$conf" \
    -H 'Content-Type: application/json' --data-binary "@$body" \
    "$FAKE841_SERVICE/v1/identity/ticket" >"$FAKE841_DIR/ticket-$1.code"
}

ask_claim() { # ask_claim RUN_ID
  local rid=$1 conf="$FAKE841_DIR/claim-$1.conf" body="$FAKE841_DIR/claim-$1.body"
  python3 -c 'import json, sys
doc = json.load(open(sys.argv[1]))
open(sys.argv[2], "w").write("{\"ticket\": \"%s\", \"nonce\": \"fake-run-nonce\"}" % doc.get("ticket", ""))' \
    "$FAKE841_DIR/ticket-$1.json" "$body"
  printf 'header = "Authorization: Bearer %s"\n' 'SENTINEL-841-id-token-not-a-credential' >"$conf"
  curl -sS -o "$FAKE841_DIR/claim-$1.json" -w '%%{http_code}' --config "$conf" \
    -H 'Content-Type: application/json' --data-binary "@$body" \
    "$FAKE841_SERVICE/v1/identity/claim" >"$FAKE841_DIR/claim-$1.code"
}

case "${1:-}" in
version)
  cat "$state/version"
  ;;
doctor)
  gcp=$FAKE841_PROJECT
  region=$FAKE841_REGION
  if knob doctor-other-region; then region=europe-west1; fi
  if knob doctor-other-project; then gcp=other-project; fi
  printf '{"ok":true,"checks":[],"project":{"name":"%s","gcp_project":"%s","region":"%s"},"fugaro_yaml":{"path":"%s/fugaro.yaml","valid":true}}\n' \
    "$FAKE841_SLUG" "$gcp" "$region" "$FAKE841_REPO"
  ;;
project)
  if knob policy-fail; then
    printf 'fake fugaro: the published policy node could not be read\n' >&2
    exit 1
  fi
  printf '{"project":"%s","policy":{"version":%s}}\n' "$FAKE841_SLUG" "$(cat "$state/policy")"
  ;;
runs)
  if knob runs-fail; then
    printf 'fake fugaro: the runs bucket could not be read\n' >&2
    exit 1
  fi
  FAKE841_STATE="$state" python3 - <<'PY'
import glob
import json
import os
import sys

state = os.environ["FAKE841_STATE"]
rows = []
for path in sorted(glob.glob(os.path.join(state, "runs", "*.status"))):
    run = os.path.basename(path)[: -len(".status")]
    with open(path, encoding="utf-8") as fh:
        status = fh.read().strip()
    terminal = status not in ("running", "launched", "queued")
    rows.append({
        "run": "%s/%s" % (os.environ.get("FAKE841_SLUG", "fugaro-dev"), run),
        "run_id": run,
        "repo": os.environ.get("FAKE841_SLUG", "fugaro-dev"),
        "status": status,
        "terminal": terminal,
        "branch": "fugaro/%s" % run,
        "outcome": "ready" if status == "succeeded" else "",
        "pr": 4242 if status == "succeeded" else 0,
        "title": "Check 841: a run in flight across the update",
    })
print(json.dumps({"project": os.environ.get("FAKE841_SLUG", "fugaro-dev"), "runs": rows}))
PY
  ;;
run)
  if knob launch-fail; then
    printf "fake fugaro: the launch was refused: no job could be created\n" >&2
    exit 1
  fi
  mkdir -p "$state/runs"
  seq=$(cat "$state/runs/seq" 2>/dev/null || printf '0')
  seq=$((seq + 1))
  printf '%s' "$seq" >"$state/runs/seq"
  # A run id of the shape the tree validates (internal/task's ValidRunID).
  rid="$(date -u +%Y%m%d-%H%M%S)-$(printf '%04x' $((seq + 4096)))"
  printf 'running' >"$state/runs/$rid.status"
  if ! knob launch-no-record; then
    ask_ticket "$rid" >/dev/null
    ask_claim "$rid" >/dev/null
  fi
  if ! knob launch-no-ledger; then
    mkdir -p "$state/rtdb/runs/$FAKE841_SLUG"
    printf '{"reserved":1,"released":0,"spent":0,"tokens":0,"exp":0,"overrun":false}' \
      >"$state/rtdb/runs/$FAKE841_SLUG/$rid.json"
  fi
  printf '{"project":"%s","run":"%s/%s","run_id":"%s","repo":"%s","branch":"fugaro/%s","execution":"projects/%s/locations/%s/jobs/%s/executions/%s","status":"launched"}\n' \
    "$FAKE841_PROJECT" "$FAKE841_SLUG" "$rid" "$rid" "$FAKE841_SLUG" "$rid" \
    "$FAKE841_PROJECT" "$FAKE841_REGION" "$FAKE841_JOB" "$rid"
  ;;
update)
  running=0
  for f in "$state"/runs/*.status; do
    [ -f "$f" ] || continue
    if [ "$(cat "$f")" = running ]; then running=$((running + 1)); fi
  done
  printf 'update: applying terraform\n' >&2
  if knob update-prompt; then printf 'Continue? [y/N] ' >&2; fi
  if ! knob update-nosentence; then
    phrase=runs
    if [ "$running" = 1 ]; then phrase=run; fi
    printf '%s %s in flight: update waits for none and changes nothing they hold, and these steps can still reach them:\n' \
      "$running" "$phrase" >&2
    printf '  the budget mode raise to enforce, which a run meets at its next lease\n' >&2
  fi
  if knob update-refuse; then
    printf 'fake fugaro: the converge refused: the project is not selectable\n' >&2
    exit 1
  fi
  if knob update-steps; then
    printf 'the update is not finished: 1 step(s) left for you, listed above\n' >&2
    exit 1
  fi
  if ! knob update-leaves-node; then printf '%s' "$(cat "$state/raise")" >"$state/policy"; fi
  if ! knob update-leaves-run-running; then
    for f in "$state"/runs/*.status; do
      [ -f "$f" ] || continue
      if [ "$(cat "$f")" = running ]; then printf 'succeeded' >"$f"; fi
    done
  fi
  printf 'update: done\n' >&2
  ;;
pair)
  if knob pair-no-event; then
    printf 'fake fugaro: pair said nothing at all\n' >&2
    exit 1
  fi
  if knob pair-refused; then
    printf '{"event":"refused","code":"minter_role_missing","message":"the role that mints a pairing token is gone"}\n'
    exit 1
  fi
  printf '{"event":"qr","payload":"%s","uid":"pair-841"}\n' 'SENTINEL-841-pairing-payload-not-a-credential'
  printf '{"event":"waiting","timeout_ms":5000}\n'
  printf '{"event":"refused","code":"no_key_before_timeout","message":"no phone answered before the timeout"}\n'
  exit 1
  ;;
*)
  printf 'fake fugaro: unhandled call: %s\n' "$*" >&2
  exit 1
  ;;
esac
FUGARO_EOF

  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A fake curl for check-841.sh's test: the credentials service's two identity
# routes and the installation's Realtime Database. The ticket route writes the
# issuance record the live check reads; the claim route marks it claimed and
# answers a custom token; a GET or a DELETE is one node. The request's ticket,
# claim body and the caller it was attributed to are kept, so a case can say
# what travelled and where it did not.
set -euo pipefail
printf 'curl %s\n' "$*" >>"$FAKE841_DIR/calls"
conf=""
out=""
url=""
data=""
method=GET
while [ $# -gt 0 ]; do
  case "$1" in
  --config)
    conf=${2:-}
    shift 2
    ;;
  -o)
    out=${2:-}
    shift 2
    ;;
  -d | --data-binary)
    data=${2#@}
    shift 2
    ;;
  -X)
    method=${2:-}
    shift 2
    ;;
  -w | --max-time | -H) shift 2 ;;
  -*) shift ;;
  *)
    url=$1
    shift
    ;;
  esac
done
if [ -n "$conf" ]; then
  sed -n 's/^header = "Authorization: Bearer \(.*\)"$/\1/p' "$conf" >>"$FAKE841_DIR/curl.tokens" 2>/dev/null || true
  python3 -c 'import os, sys; print("%o" % (os.stat(sys.argv[1]).st_mode & 0o777))' "$conf" \
    >"$FAKE841_DIR/conf.mode" 2>/dev/null || printf 'unknown\n' >"$FAKE841_DIR/conf.mode"
fi
knob() { [ -f "$FAKE841_KNOBS/$1" ]; }
caller=$(cat "$FAKE841_STATE/caller" 2>/dev/null || printf 'owner@example.com')
field_of() { # field_of BODYFILE KEY
  python3 -c 'import json, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    doc = {}
print(doc.get(sys.argv[2], ""))' "$1" "$2"
}
status=200
body=null
case "$url" in
*/v1/identity/ticket)
  slug=$(field_of "$data" slug)
  run=$(field_of "$data" run)
  account=$(field_of "$data" account)
  rec="$FAKE841_STATE/rtdb/creds/issuance/$slug/$run.json"
  printf 'ticket caller=%s slug=%s run=%s account=%s\n' "$caller" "$slug" "$run" "$account" >>"$FAKE841_DIR/service.log"
  cp "$data" "$FAKE841_DIR/ticket-ask-$run.body" 2>/dev/null || true
  exist_caller=""
  if [ -f "$rec" ]; then exist_caller=$(python3 -c 'import json, sys
print(json.load(open(sys.argv[1])).get("caller", ""))' "$rec"); fi
  if [ -f "$rec" ] && [ "$exist_caller" != "$caller" ]; then
    if knob second-not-admitted; then
      status=403
      body=$(printf '{"error":"%s","message":"%s"}' caller_not_admitted "this caller is not one this installation's roster admits")
    elif knob second-served; then
      # The finding the `second` row exists for: the service hands another caller
      # a ticket for a run that is not theirs. The record is left as it was.
      status=200
      body=$(printf '{"ticket":"%s:%s:%s","expires_at":"2026-11-27T20:00:00Z"}' 'SENTINEL-841-ticket' "$slug" "$run")
    else
      status=403
      body=$(printf '{"error":"%s","message":"%s"}' identity_refused "this run is another caller's record, and a run's ticket is re-issued to the caller on the record alone (R5): a new attempt takes a new run id")
    fi
  else
    mkdir -p "$(dirname "$rec")"
    printf '{"caller":"%s","class":"launcher","at":"2026-11-27T19:00:00Z","claims":0,"expected_account":"%s","ticket_hash":"hash-of-a-ticket","claimed":false,"attempt":0}' \
      "$caller" "$account" >"$rec"
    status=200
    body=$(printf '{"ticket":"%s:%s:%s","expires_at":"2026-11-27T20:00:00Z"}' 'SENTINEL-841-ticket' "$slug" "$run")
  fi
  ;;
*/v1/identity/claim)
  ticket=$(field_of "$data" ticket)
  nonce=$(field_of "$data" nonce)
  printf 'claim caller=%s ticket=%s nonce=%s\n' "$caller" "$ticket" "$nonce" >>"$FAKE841_DIR/service.log"
  rest=${ticket#SENTINEL-841-ticket:}
  slug=${rest%%:*}
  run=${rest#*:}
  rec="$FAKE841_STATE/rtdb/creds/issuance/$slug/$run.json"
  claimed=false
  if [ -f "$rec" ]; then claimed=$(python3 -c 'import json, sys
print("true" if json.load(open(sys.argv[1])).get("claimed") else "false")' "$rec"); fi
  if [ ! -f "$rec" ]; then
    status=403
    body=$(printf '{"error":"%s","message":"%s"}' identity_refused "no ticket was issued for this run")
  elif [ "$claimed" = true ] && ! knob claim-always-ok; then
    status=403
    body=$(printf '{"error":"%s","message":"%s"}' identity_refused "this ticket was already claimed, and a claimed ticket is never re-issued: a second attempt takes a new run id (R6)")
  else
    python3 -c 'import json, sys
path = sys.argv[1]
doc = json.load(open(path))
doc["claimed"] = True
json.dump(doc, open(path, "w"))' "$rec"
    status=200
    body=$(printf '{"token":"%s","expires_at":"2026-11-27T20:00:00Z"}' 'SENTINEL-841-custom-token-not-a-credential')
  fi
  ;;
*)
  rest=${url#"$FAKE841_RTDB"/}
  rest=${rest%.json}
  node="$FAKE841_STATE/rtdb/$rest.json"
  case "$method" in
  DELETE)
    if [ -f "$node" ]; then rm -f "$node"; fi
    status=200
    body=null
    printf 'delete %s\n' "$rest" >>"$FAKE841_DIR/service.log"
    ;;
  *)
    if [ -f "$node" ]; then
      body=$(cat "$node")
    else
      status=404
      body=null
    fi
    ;;
  esac
  ;;
esac
if [ -n "$out" ]; then printf '%s' "$body" >"$out"; fi
printf '%s' "$status"
CURL_EOF

  # A fugaro of the release behind, for the one row that reads its answer: it
  # prints the sentence the tree itself spells, computed at test time from the
  # tree's own constants, exactly as the live check computes it.
  cat >"$1/bin/old-fugaro" <<OLD_EOF
#!/usr/bin/env bash
set -euo pipefail
printf 'old-fugaro %s\n' "\$*" >>"\$FAKE841_DIR/calls"
if [ -f "\$FAKE841_KNOBS/old-cli-mute" ]; then
  printf '{"ok":true,"checks":[]}\n'
  exit 0
fi
printf '{"ok":false,"checks":[{"id":"policy-newer-than-binary","ok":false,"problem":"%s: config/policy/shared","fix":"upgrade Fugaro"}]}\n' \
  "${UPGRADE_SENTENCE}"
exit 1
OLD_EOF
  chmod +x "$1/bin/gcloud" "$1/bin/fugaro" "$1/bin/curl" "$1/bin/old-fugaro"
}

# ------------------------------------------------------------ the installation
# bind_installation writes the state every case starts from: the installation at
# the PREVIOUS release, a launch that has not happened, and the job the launch
# runs as.
bind_installation() {
  rm -rf "$work/bin" "$work/state" "$work/knobs" "$work/dir" "$work/repo" "$work/home"
  mkdir -p "$work/state/runs" "$work/knobs" "$work/dir" "$work/repo" "$work/home" "$work/install"
  printf '%s' "$PREV_VERSION" >"$work/state/policy"
  printf '%s' "$POLICY_VERSION" >"$work/state/raise"
  printf '%s\n' "$RELEASE" >"$work/state/version"
  printf '%s\n' "$OWNER" >"$work/state/owner"
  printf '%s\n' "$JOB_ACCOUNT" >"$work/state/job-account"
  : >"$work/dir/calls"
  : >"$work/dir/service.log"
  cat >"$work/dir/job.json" <<JSON
{"spec":{"template":{"spec":{"serviceAccountName":"${JOB_ACCOUNT}","containers":[{"env":[{"name":"FUGARO_CREDENTIALS_SERVICE_URL","value":"${SERVICE}"}]}]}}}}
JSON
  cat >"$work/install/config.yaml" <<YAML
name: ${SLUG}
gcp_project: ${PROJECT}
region: ${REGION}
backend: gcp
rtdb_url: ${RTDB}
credentials_service:
  url: ${SERVICE}
YAML
  printf 'name: %s\n' "$SLUG" >"$work/repo/fugaro.yaml"
  write_fakes "$work"
}

export FAKE841_DIR
export FAKE841_STATE
export FAKE841_KNOBS
export FAKE841_PROJECT=$PROJECT
export FAKE841_REGION=$REGION
export FAKE841_SLUG=$SLUG
export FAKE841_JOB=$JOB
export FAKE841_JOB_ACCOUNT=$JOB_ACCOUNT
export FAKE841_SERVICE=$SERVICE
export FAKE841_RTDB=$RTDB
export FAKE841_REPO=$work/repo
FAKE841_DIR=$work/dir
FAKE841_STATE=$work/state
FAKE841_KNOBS=$work/knobs

# ------------------------------------------------------------------ the runner
# out, status and last_error are what a case reads: the block the check printed
# on stdout, its exit code, and what it said on stderr.
out=""
status=0
EXTRA_ENV=()
run_check() { # run_check [args...]
  status=0
  out=$(cd "$work" && env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT -u BASH_ENV \
    CHK841_OWNER_RUN=yes \
    CHK841_CONFIG="$work/install/config.yaml" \
    CHK841_REPO="$work/repo" \
    CHK841_SECOND_LAUNCHER="$SECOND" \
    CHK841_OLD_CLI="${OLD_CLI-$work/bin/old-fugaro}" \
    CHK841_POLL_SECONDS=1 \
    CHK841_START_SECONDS=5 \
    CHK841_CLAIM_SECONDS=5 \
    CHK841_WAIT_SECONDS=4 \
    CHK841_DEADLINE=60 \
    CHK841_PAIR_SECONDS=1 \
    CHK841_MAX_USD=1 \
    HOME="$work/home" \
    PATH="$work/bin:$PATH" \
    ${EXTRA_ENV[@]+"${EXTRA_ENV[@]}"} \
    bash "$script" "$@" 2>"$work/last.err") || status=$?
  printf '%s\n' "$out" >"$work/last.out"
}

# expect_no_credential_leaks is the assertion every case carries: no token, no
# ticket, no custom token and no pairing payload reaches the block or the
# progress lines, whatever else the run did.
expect_no_credential_leaks() { # what
  local where
  for where in "block:$out" "stderr:$(cat "$work/last.err" 2>/dev/null || true)"; do
    local label=${where%%:*}
    local text=${where#*:}
    expect_absent "$text" "$ID_SENTINEL" "$1: the identity token reached the $label"
    expect_absent "$text" "$ACCESS_SENTINEL" "$1: the access token reached the $label"
    expect_absent "$text" "$TICKET_PREFIX" "$1: the ticket reached the $label"
    expect_absent "$text" "$CUSTOM_SENTINEL" "$1: the claim's custom token reached the $label"
    expect_absent "$text" "$PAIR_SENTINEL" "$1: the pairing payload reached the $label"
  done
}

# --------------------------------------------------------------------- cases
test_the_readings_hold() {
  bind_installation
  run_check
  expect_status "$status" 0 "the six readings"
  expect_no_credential_leaks "the six readings"
  expect_contains "$out" "=== CHECK 841 RESULT ===" "the block is headed"
  expect_block_keys "$out" "the six readings"
  expect_row "$out" xtrace PASS "the six readings"
  expect_row "$out" owner PASS "the six readings"
  expect_row "$out" belong PASS "the six readings"
  expect_row "$out" tree PASS "the six readings"
  expect_contains "$out" "policy version ${POLICY_VERSION} — written by ${RELEASE}" "the tree row names the release this check is about"
  expect_row "$out" installation PASS "the six readings"
  expect_row "$out" premise PASS "the six readings"
  expect_row "$out" run PASS "the six readings"
  expect_row "$out" update PASS "the six readings"
  expect_contains "$out" "1 run in flight: update waits for none and changes nothing they hold" "the update row quotes the update's own line about the run in flight"
  expect_contains "$out" "the published node now reads version ${POLICY_VERSION}" "the update row reads the raised node"
  expect_row "$out" asked PASS "the six readings"
  expect_row "$out" inflight PASS "the six readings"
  expect_row "$out" session PASS "the six readings"
  expect_contains "$out" "reads claimed" "the session row reads the ticket's claim"
  expect_row "$out" second PASS "the six readings"
  expect_contains "$out" "identity_refused" "the second row reads the service's own refusal"
  expect_row "$out" copy PASS "the six readings"
  expect_contains "$out" "the copy lost" "the copy row reads the second claim as refused"
  expect_contains "$out" "was removed (HTTP 200)" "the copy row removed the record it made"
  expect_row "$out" old-cli PASS "the six readings"
  expect_contains "$out" "the project policy is version ${POLICY_VERSION}; this Fugaro understands version ${PREV_VERSION}" "the old CLI's own answer is the sentence the tree spells"
  expect_row "$out" pair PASS "the six readings"
  expect_row "$out" restore INFO "the six readings"
  [ "$(cat "$work/state/policy")" = "$POLICY_VERSION" ] || fail "the update raised no node: the fake's policy is $(cat "$work/state/policy")"
  # Both launches asked the credentials service and claimed what it answered:
  # that the check read a claimed record is not a coincidence of the fake
  # writing one, it is the chain a launch is.
  grep -q 'ticket caller=' "$work/dir/service.log" || fail "no ticket was ever asked for through the service"
  grep -q 'claim caller=' "$work/dir/service.log" || fail "no ticket was ever claimed through the service"
  # And the ticket the check itself asked for travelled in a body file, not on a
  # command line: the assertion that it never reaches the block is about a value
  # that really existed.
  ls "$work/dir"/ticket-ask-*.body >/dev/null 2>&1 || fail "the copy row asked for no ticket, so its 'never printed' assertions are vacuous"
  grep -q "$TICKET_PREFIX" "$work/dir"/ticket-ask-*.body 2>/dev/null \
    || grep -q "$TICKET_PREFIX" "$work/dir"/claim-*.body 2>/dev/null \
    || fail "no ticket sentinel ever travelled, so the assertions that it never reaches the block are vacuous"
}

test_the_update_waits_for_the_run_in_flight() {
  bind_installation
  run_check
  expect_status "$status" 0 "the update with a run in flight"
  # The run launched BEFORE the update is the one the update's own line counted:
  # the fake's line counts the runs its own state holds as running, and the
  # check read the count out of that line.
  expect_contains "$out" "its own line named the runs in flight — 1 run in flight" "the update row"
  # Both runs of the check are on the repository: it says so rather than
  # pretending to undo them.
  expect_contains "$out" "are NOT undone" "the restore row names what it left behind"
}

test_a_premise_already_at_this_release_stops() {
  bind_installation
  printf '%s' "$POLICY_VERSION" >"$work/state/policy"
  run_check
  expect_status "$status" 2 "an installation already at this release"
  expect_row "$out" premise FAIL "an installation already at this release"
  expect_contains "$out" "publishes this release's policy version already" "the stop names the state"
  expect_row "$out" restore INFO "an installation already at this release"
  expect_no_credential_leaks "an installation already at this release"
}

test_a_premise_at_another_version_stops() {
  bind_installation
  printf '%s' "$((PREV_VERSION - 1))" >"$work/state/policy"
  run_check
  expect_status "$status" 2 "an installation at another release"
  expect_row "$out" premise FAIL "an installation at another release"
  expect_contains "$out" "publishes policy version $((PREV_VERSION - 1))" "the stop names the version it read"
}

test_a_cli_of_the_release_behind_stops() {
  bind_installation
  printf '0.1.0\n' >"$work/state/version"
  run_check
  expect_status "$status" 2 "a CLI of the release behind"
  expect_row "$out" premise FAIL "a CLI of the release behind"
  expect_contains "$out" "brew upgrade" "the stop names the one command that fixes it"
}

test_an_installation_of_another_project_stops() {
  bind_installation
  sed -i.bak "s/^gcp_project: .*/gcp_project: some-other-project/" "$work/install/config.yaml" && rm -f "$work/install/config.yaml.bak"
  run_check
  expect_status "$status" 2 "another project's configuration"
  expect_row "$out" installation FAIL "another project's configuration"
  expect_contains "$out" "some-other-project" "the stop names the project it read"
}

test_a_configuration_without_a_credentials_service_stops() {
  bind_installation
  grep -v 'credentials_service\|^  url:' "$work/install/config.yaml" >"$work/install/config.tmp"
  mv "$work/install/config.tmp" "$work/install/config.yaml"
  run_check
  expect_status "$status" 2 "a configuration with no credentials service"
  expect_row "$out" installation FAIL "a configuration with no credentials service"
  expect_contains "$out" "credentials_service" "the stop names the missing block"
}

test_a_checkout_that_disagrees_with_the_configuration_stops() {
  bind_installation
  : >"$work/knobs/doctor-other-region"
  run_check
  expect_status "$status" 2 "a checkout in another region"
  expect_row "$out" installation FAIL "a checkout in another region"
  expect_contains "$out" "europe-west1" "the stop names the region the checkout reads"
}

test_belong_is_refused() {
  bind_installation
  EXTRA_ENV=(CLOUDSDK_CORE_PROJECT=belong-prod)
  run_check
  EXTRA_ENV=()
  expect_status "$status" 2 "an ambient belong project"
  expect_row "$out" belong FAIL "an ambient belong project"
  expect_contains "$out" "never runs against 'belong'" "the stop names the fence"
}

test_the_second_launcher_must_be_another_account() {
  bind_installation
  run_check --second-launcher "$OWNER"
  expect_status "$status" 2 "a second launcher that is gcloud's own account"
  expect_row "$out" belong FAIL "a second launcher that is gcloud's own account"
  expect_contains "$out" "another account than the one gcloud is authenticated as" "the stop names why"
}

test_an_agent_session_is_refused() {
  bind_installation
  EXTRA_ENV=(CLAUDECODE=1)
  run_check
  EXTRA_ENV=()
  expect_status "$status" 2 "a coding agent's session"
  expect_contains "$out" "no agent runs any step of Check 841" "the stop names the rule"
}

test_no_owner_marker_stops() {
  bind_installation
  EXTRA_ENV=(CHK841_OWNER_RUN=)
  run_check
  EXTRA_ENV=()
  expect_status "$status" 2 "no owner-run marker"
  expect_row "$out" owner FAIL "no owner-run marker"
  expect_contains "$out" "owner-run check" "the stop names the marker"
}

test_xtrace_is_refused() {
  bind_installation
  status=0
  out=$(cd "$work" && env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CURSOR_AGENT -u AI_AGENT \
    CHK841_OWNER_RUN=yes CHK841_CONFIG="$work/install/config.yaml" CHK841_REPO="$work/repo" \
    CHK841_SECOND_LAUNCHER="$SECOND" PATH="$work/bin:$PATH" HOME="$work/home" \
    bash -x "$script" 2>"$work/last.err") || status=$?
  expect_status "$status" 2 "a shell started with -x"
  expect_row "$out" xtrace FAIL "a shell started with -x"
  expect_contains "$out" "Run it without -x" "the stop names the fix"
}

test_the_launch_that_never_reads_running_is_unverified() {
  bind_installation
  # The update leaves the run alone and nothing else finishes it: the check waits
  # --wait seconds for a terminal status that never comes.
  : >"$work/knobs/update-leaves-run-running"
  run_check
  expect_status "$status" 3 "a run that never finishes"
  expect_row "$out" run PASS "a run that never finishes"
  expect_row "$out" inflight UNVERIFIED "a run that never finishes"
  expect_contains "$out" "raise --wait" "the row names what to raise"
}

test_a_launch_the_installation_refuses_fails() {
  bind_installation
  : >"$work/knobs/launch-fail"
  run_check
  expect_status "$status" 1 "a launch that was refused"
  expect_row "$out" run FAIL "a launch that was refused"
  expect_row "$out" session FAIL "a launch that was refused"
  expect_contains "$out" "the launch was refused" "the row quotes the refusal"
}

test_a_run_that_gets_no_record_fails_session() {
  bind_installation
  # The launcher of the run launched after the update signed its identity
  # itself: no ticket was asked for and no record was written.
  : >"$work/knobs/launch-no-record"
  run_check
  expect_status "$status" 1 "a run with no issuance record"
  expect_row "$out" session FAIL "a run with no issuance record"
  expect_contains "$out" "signed the run's identity ITSELF" "the row names the bridge"
}

test_a_missing_ledger_is_unverified() {
  bind_installation
  : >"$work/knobs/launch-no-ledger"
  run_check
  expect_status "$status" 3 "a run whose ledger node is missing"
  expect_row "$out" session UNVERIFIED "a run whose ledger node is missing"
}

test_the_update_without_the_run_in_flight_line_fails() {
  bind_installation
  : >"$work/knobs/update-nosentence"
  run_check
  expect_status "$status" 1 "an update that says nothing about the runs in flight"
  expect_row "$out" update FAIL "an update that says nothing about the runs in flight"
  expect_contains "$out" "no line about the runs in flight" "the row names the missing line"
}

test_an_update_that_raises_no_node_fails() {
  bind_installation
  : >"$work/knobs/update-leaves-node"
  run_check
  expect_status "$status" 1 "an update that raised no node"
  expect_row "$out" update FAIL "an update that raised no node"
  expect_contains "$out" "did not" "the row says the node was not raised"
}

test_a_refused_update_fails() {
  bind_installation
  : >"$work/knobs/update-refuse"
  run_check
  expect_status "$status" 1 "a converge that refused"
  expect_row "$out" update FAIL "a converge that refused"
  expect_contains "$out" "the converge refused" "the row quotes the refusal"
}

test_a_step_left_for_you_is_unverified() {
  bind_installation
  : >"$work/knobs/update-steps"
  run_check
  expect_status "$status" 3 "a converge with a step left for you"
  expect_row "$out" update UNVERIFIED "a converge with a step left for you"
  expect_contains "$out" "1 step(s) left for you" "the row quotes the CLI's own sentence"
}

test_a_question_in_the_transcript_fails() {
  bind_installation
  : >"$work/knobs/update-prompt"
  run_check
  expect_status "$status" 1 "an update that asked a question"
  expect_row "$out" asked FAIL "an update that asked a question"
  expect_contains "$out" "[y/N]" "the row names the shape it found"
}

test_a_second_account_the_roster_does_not_admit_is_unverified() {
  bind_installation
  : >"$work/knobs/second-not-admitted"
  run_check
  expect_status "$status" 3 "a second account the roster does not admit"
  expect_row "$out" second UNVERIFIED "a second account the roster does not admit"
  expect_contains "$out" "caller_not_admitted" "the row names the code it read"
}

test_a_second_account_served_a_ticket_fails() {
  bind_installation
  : >"$work/knobs/second-served"
  run_check
  expect_status "$status" 1 "a second account served the run's ticket"
  expect_row "$out" second FAIL "a second account served the run's ticket"
  expect_contains "$out" "was SERVED a ticket" "the row names what it saw"
}

test_a_ticket_served_twice_fails_the_copy_row() {
  bind_installation
  : >"$work/knobs/claim-always-ok"
  run_check
  expect_status "$status" 1 "a ticket claimed twice"
  expect_row "$out" copy FAIL "a ticket claimed twice"
  expect_contains "$out" "claimed TWICE" "the row names what it saw"
}

test_an_old_cli_that_reads_the_node_is_a_finding() {
  bind_installation
  : >"$work/knobs/old-cli-mute"
  run_check
  expect_status "$status" 1 "an older CLI that did not refuse the node"
  expect_row "$out" old-cli FAIL "an older CLI that did not refuse the node"
  expect_contains "$out" "did not print the upgrade sentence" "the row names the missing sentence"
}

test_no_old_cli_is_unverified() {
  bind_installation
  EXTRA_ENV=(CHK841_OLD_CLI=)
  run_check
  EXTRA_ENV=()
  expect_status "$status" 3 "no older CLI named"
  expect_row "$out" old-cli UNVERIFIED "no older CLI named"
  expect_contains "$out" "--old-cli" "the row names how to get one"
}

test_a_pairing_that_falls_back_to_the_missing_role_fails() {
  bind_installation
  : >"$work/knobs/pair-refused"
  run_check
  expect_status "$status" 1 "a pairing that fell back to the missing role"
  expect_row "$out" pair FAIL "a pairing that fell back to the missing role"
  expect_contains "$out" "minter_role_missing" "the row names the code it read"
}

test_a_pairing_that_says_nothing_fails() {
  bind_installation
  : >"$work/knobs/pair-no-event"
  run_check
  expect_status "$status" 1 "a pairing with no event at all"
  expect_row "$out" pair FAIL "a pairing with no event at all"
}

test_a_run_that_is_not_read_back_fails_inflight() {
  bind_installation
  # The runs bucket cannot be read at all while the run in flight is followed.
  : >"$work/knobs/runs-fail"
  run_check
  expect_status "$status" 1 "a runs bucket that cannot be read"
  expect_row "$out" run FAIL "a runs bucket that cannot be read"
  expect_contains "$out" "could not be read back" "the row says the record could not be read"
}

test_the_policy_that_cannot_be_read_stops() {
  bind_installation
  : >"$work/knobs/policy-fail"
  run_check
  expect_status "$status" 2 "a published node that cannot be read"
  expect_row "$out" premise FAIL "a published node that cannot be read"
}

test_a_dry_run_makes_no_call() {
  bind_installation
  rm -f "$work/dir/calls"
  run_check --dry-run
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "CHECK 841 DRY RUN" "a dry run prints the plan"
  expect_contains "$out" "--second-launcher" "the plan lists the flags it would use"
  expect_absent "$out" "=== CHECK 841 RESULT ===" "a dry run prints no block"
  if [ -f "$work/dir/calls" ]; then fail "a dry run called a tool: $(head -3 "$work/dir/calls")"; fi
  if [ -n "$(ls -d "${TMPDIR:-/tmp}"/chk841.* 2>/dev/null || true)" ]; then fail "a dry run left a scratch directory behind"; fi
}

test_usage_is_printed_and_exits_zero() {
  bind_installation
  rm -f "$work/dir/calls"
  run_check --help
  expect_status "$status" 0 "the usage"
  expect_contains "$out" "Check 841 — the grant moves and nothing stops" "the usage is headed"
  expect_contains "$out" "--old-cli" "the usage lists the flags"
  expect_absent "$out" "=== CHECK 841 RESULT ===" "the usage prints no block"
  if [ -f "$work/dir/calls" ]; then fail "the usage called a tool"; fi
}

test_an_unknown_flag_stops() {
  bind_installation
  run_check --not-a-flag
  expect_status "$status" 2 "an unknown flag"
  expect_row "$out" aborted FAIL "an unknown flag"
  expect_contains "$out" "unknown flag --not-a-flag" "the stop names the flag"
}

test_the_scratch_directory_is_removed() {
  bind_installation
  run_check
  local dir
  dir=$(printf '%s\n' "$out" | grep -o -E '/[^ ]*chk841\.[A-Za-z0-9]+' | head -1 || true)
  [ -n "$dir" ] || fail "the restore row names no scratch directory"
  if [ -d "$dir" ]; then fail "the scratch directory $dir was left behind"; fi
}

test_a_kept_scratch_directory_is_kept() {
  bind_installation
  EXTRA_ENV=(CHK841_KEEP_SCRATCH=1)
  run_check
  EXTRA_ENV=()
  local dir
  dir=$(printf '%s\n' "$out" | grep -o -E '/[^ ]*chk841\.[A-Za-z0-9]+' | head -1 || true)
  [ -n "$dir" ] || fail "the restore row names no scratch directory"
  [ -d "$dir" ] || fail "CHK841_KEEP_SCRATCH=1 did not keep $dir"
  expect_contains "$out" "is KEPT" "the restore row says the directory was kept"
  if [ -n "$dir" ]; then rm -rf "$dir"; fi
}

# Sharding, so the package's Go test can run the tests of this file side by
# side: CHECK_TEST_SHARD="i/n" runs every n-th test starting at i, and
# CHECK_TEST_ONLY=name runs that one test alone. Neither set: all run.
test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1" || true
}

t test_the_readings_hold
t test_the_update_waits_for_the_run_in_flight
t test_a_premise_already_at_this_release_stops
t test_a_premise_at_another_version_stops
t test_a_cli_of_the_release_behind_stops
t test_an_installation_of_another_project_stops
t test_a_configuration_without_a_credentials_service_stops
t test_a_checkout_that_disagrees_with_the_configuration_stops
t test_belong_is_refused
t test_the_second_launcher_must_be_another_account
t test_an_agent_session_is_refused
t test_no_owner_marker_stops
t test_xtrace_is_refused
t test_the_launch_that_never_reads_running_is_unverified
t test_a_launch_the_installation_refuses_fails
t test_a_run_that_gets_no_record_fails_session
t test_a_missing_ledger_is_unverified
t test_the_update_without_the_run_in_flight_line_fails
t test_an_update_that_raises_no_node_fails
t test_a_refused_update_fails
t test_a_step_left_for_you_is_unverified
t test_a_question_in_the_transcript_fails
t test_a_second_account_the_roster_does_not_admit_is_unverified
t test_a_second_account_served_a_ticket_fails
t test_a_ticket_served_twice_fails_the_copy_row
t test_an_old_cli_that_reads_the_node_is_a_finding
t test_no_old_cli_is_unverified
t test_a_pairing_that_falls_back_to_the_missing_role_fails
t test_a_pairing_that_says_nothing_fails
t test_a_run_that_is_not_read_back_fails_inflight
t test_the_policy_that_cannot_be_read_stops
t test_a_dry_run_makes_no_call
t test_usage_is_printed_and_exits_zero
t test_an_unknown_flag_stops
t test_the_scratch_directory_is_removed
t test_a_kept_scratch_directory_is_kept

if [ "$failures" -ne 0 ]; then
  echo "check-841_test: $failures failure(s)" >&2
  exit 1
fi
echo "check-841_test: all tests passed" >&2
