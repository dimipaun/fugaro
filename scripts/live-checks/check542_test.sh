#!/usr/bin/env bash
# Tests for scripts/live-checks/check542.sh, with fake `gh`, `gcloud`, `fugaro`
# and `curl` on PATH: NOTHING here reaches a cloud, a project, an installation
# or github.com, no agent runs a live command, and the fakes record every call
# they are given — with the directory they were called FROM — so a case can
# assert what the check did, and did not, do.
#
# The fakes are a small model of one installation: the fake `gh` answers GitHub's
# own documents (the App's installation and its granted permissions, the rulesets
# covering the base and each one again by id, the base's classic branch
# protection), the fake `gcloud` answers Cloud Run's (the service, its
# environment NAMES, the invoker policy, the daemon's account, the log) and mints
# an ID token per caller, and the fake `curl` is the merge mint itself — it reads
# the caller out of the curl config file the check wrote, compares the head in
# the body against the pull request's own head, answers a token or the route's
# typed refusal, and writes an audit entry for each call a verified caller made,
# which the fake `gcloud logging read` then serves back. So the check really does
# go round its own loop, and the cases that matter are the ones where that model
# is bent: a permission the App does not hold, the App named on a bypass list, a
# ruleset whose list cannot be read, a mint answered for a head the pull request
# is not at, a run identity that is minted anyway, a person's token that is, an
# audit line written for a caller that was never verified.
#
# Three of the cases below are MUTATION checks (the shape check195_test.sh uses):
# the script is copied with one line taken out, and the case asserts the run does
# NOT pass — the guard is load-bearing, and a PASS with the guard removed would
# be this test's own failure. The line taken out is always a guard the check
# depends on, never a row: (1) the agent-session guard, whose removal puts the
# service's own merge mint in front of a coding agent's session, (2) the belong
# guard, whose removal reads a project this check must never touch, and (3) the
# credential scrub, whose removal leaves the installation token a successful mint
# answered with on the owner's disk.
#
# Usage: bash scripts/live-checks/check542_test.sh
set -Eeuo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check542.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK542_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK542_TEST_HEAD:-1200}")" ;;
  esac
}
expect_absent() {
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1"
}

project=chk542-test
slug=chk542-owner/sandbox
app_id=777
app_slug=fugaro-bot
installation=4242
head_one=1111111111111111111111111111111111111111
head_moved=1111111111111111111111111111111111111110
service_url=https://fugaro-credentials-chk542.a.run.app
daemon=fugaro-orch@${project}.iam.gserviceaccount.com
# The credentials the fakes mint and answer with. None of them is shaped like a
# real one — a fixture that looked like a credential is the thing this check's own
# hygiene rules forbid — and each is distinctive enough that a case can assert it
# never reaches the block, a log or the disk. The two ID tokens differ past the
# middle, because the fake route reads the CALLER out of the curl config file and
# a person's token that contained the daemon's whole would be read as the daemon's.
id_daemon=fixture-daemon-id-token
id_person=fixture-person-id-token
mint_token=fixture-installation-token

# --------------------------------------------------------------------- fakes
write_fake_gh() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gh" <<'GH_EOF'
#!/usr/bin/env bash
# A stand-in for `gh`, for scripts/live-checks/check542.sh's test only. It
# answers GitHub's documents out of the fixture directory it was given and
# reaches no host at all: the App's installation and the permissions it has been
# granted, the rulesets covering the base, each of them again by id, and the
# base's classic branch protection. It refuses every argument list this check
# does not make, so a write or a second endpoint would end the case loudly.
set -euo pipefail
printf '%s\t%s\n' "$PWD" "$*" >>"$FAKE_LOG_DIR/gh.calls"
mode=${FAKE_MODE:-wired}
json=$FAKE_JSON_DIR
slug=chk542-owner/sandbox
installation=4242
case "${1:-}" in
repo)
  cat "$json/name.json"
  ;;
pr)
  [ "${2:-}" = view ] || {
    echo "gh: unexpected pr ${2:-}" >&2
    exit 1
  }
  case "$mode" in
  pr-closed) cat "$json/pr-closed.json" ;;
  *) cat "$json/pr.json" ;;
  esac
  ;;
api)
  shift
  include=no
  if [ "${1:-}" = --include ]; then
    include=yes
    shift
  fi
  [ "${1:-}" = --silent ] && shift
  endpoint=${1:-}
  case "$endpoint" in
  "/user/installations?per_page=100")
    case "$mode" in
    installations-full)
      printf '{"installations":['
      i=1
      while [ "$i" -le 100 ]; do
        [ "$i" -eq 1 ] || printf ','
        printf '{"id":%s,"app_id":%s,"app_slug":"app-%s","repository_selection":"all","permissions":{"contents":"read"}}' \
          "$((1000 + i))" "$((100 + i))" "$i"
        i=$((i + 1))
      done
      printf ']}\n'
      ;;
    app-not-installed) cat "$json/installations-other.json" ;;
    *) cat "$json/installations.json" ;;
    esac
    ;;
  "/user/installations/"*"/repositories?per_page=100")
    case "$mode" in
    app-not-installed) cat "$json/repos-other.json" ;;
    *) cat "$json/repos.json" ;;
    esac
    ;;
  "/repos/${slug}/rulesets?per_page=100")
    case "$mode" in
    rulesets-full)
      printf '['
      i=1
      while [ "$i" -le 100 ]; do
        [ "$i" -eq 1 ] || printf ','
        printf '{"id":%s}' "$i"
        i=$((i + 1))
      done
      printf ']\n'
      ;;
    *) cat "$json/rulesets.json" ;;
    esac
    ;;
  "/repos/${slug}/rulesets/11")
    case "$mode" in
    ruleset-unreadable)
      printf 'gh: the ruleset could not be read again\n' >&2
      exit 1
      ;;
    ruleset-no-list) cat "$json/ruleset-no-list.json" ;;
    bypass-ruleset) cat "$json/ruleset-bypass.json" ;;
    *) cat "$json/ruleset.json" ;;
    esac
    ;;
  "/repos/${slug}/branches/main/protection")
    if [ "$include" = yes ]; then
      case "$mode" in
      classic-bypass) printf 'HTTP/2.0 200 OK\r\n\r\n' ;;
      protection-error) printf 'HTTP/2.0 500 Internal Server Error\r\n\r\n' ;;
      protection-nostatus) printf '' ;;
      *) printf 'HTTP/2.0 404 Not Found\r\n\r\n' ;;
      esac
      exit 0
    fi
    case "$mode" in
    classic-bypass) cat "$json/protection-bypass.json" ;;
    *) cat "$json/protection.json" ;;
    esac
    ;;
  *)
    echo "gh: unexpected ${endpoint:-}" >&2
    exit 1
    ;;
  esac
  ;;
*)
  echo "gh: unexpected ${1:-}" >&2
  exit 1
  ;;
esac
GH_EOF
  chmod +x "$1/bin/gh"
}

write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check542.sh's test only: the
# account the check reads at start-up, the service and its environment NAMES, the
# invoker policy, the daemon's account, the log read, and an ID token per caller
# (the daemon's when the call is an impersonation, the person's otherwise).
# Nothing here reaches a real project, and no document it serves carries a value.
set -euo pipefail
printf '%s\t%s\n' "$PWD" "$*" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-wired}
json=$FAKE_JSON_DIR
daemon=fugaro-orch@chk542-test.iam.gserviceaccount.com
# no-invoker is half-wired on purpose: the account exists and the service's
# revision names the class, and the grant that lets the account call the service
# does not — the one half the row below is about.
case "$mode" in
inert | no-class-env | account-missing) class_wired=no ;;
*) class_wired=yes ;;
esac
case "${1:-}" in
config)
  case "$mode" in
  belong-account) printf 'chk542-owner@belong-prod.iam.gserviceaccount.com\n' ;;
  *) printf 'chk542-owner@example.com\n' ;;
  esac
  ;;
auth)
  [ "${2:-}" = print-identity-token ] || {
    echo "gcloud: unexpected auth ${2:-}" >&2
    exit 1
  }
  if [ "$mode" = token-fails ]; then
    printf 'gcloud: no credential is available for this account\n' >&2
    exit 1
  fi
  case "$*" in
  *"--impersonate-service-account=${daemon}"*) cat "$json/daemon-token" ;;
  *) cat "$json/person-token" ;;
  esac
  ;;
iam)
  [ "${2:-}" = service-accounts ] || {
    echo "gcloud: unexpected iam ${2:-}" >&2
    exit 1
  }
  if [ "$class_wired" = no ]; then
    printf 'ERROR: NOT_FOUND: service account %s not found\n' "$daemon" >&2
    exit 1
  fi
  printf '{"email":"%s"}\n' "$daemon"
  ;;
run)
  case "${2:-}" in
  services)
    case "${3:-}" in
    describe)
      case "$mode" in
      service-missing)
        printf 'ERROR: the service fugaro-credentials was not found in this project\n' >&2
        exit 1
        ;;
      inert) cat "$json/service-inert.json" ;;
      no-class-env) cat "$json/service-no-class.json" ;;
      *) cat "$json/service.json" ;;
      esac
      ;;
    get-iam-policy)
      case "$mode" in
      inert | no-invoker) cat "$json/iam-no-invoker.json" ;;
      policy-unreadable)
        printf 'gcloud: the policy could not be read\n' >&2
        exit 1
        ;;
      *) cat "$json/iam.json" ;;
      esac
      ;;
    *)
      echo "gcloud: unexpected run services ${3:-}" >&2
      exit 1
      ;;
    esac
    ;;
  *)
    echo "gcloud: unexpected run ${2:-}" >&2
    exit 1
    ;;
  esac
  ;;
logging)
  [ "${2:-}" = read ] || {
    echo "gcloud: unexpected logging ${2:-}" >&2
    exit 1
  }
  case "$mode" in
  audit-unverified) cat "$json/audit-before-verification.json" ;;
  audit-drop) printf '[]\n' ;;
  audit-stale) cat "$json/audit-stale.json" ;;
  *)
    {
      printf '['
      first=1
      while IFS= read -r line; do
        [ -n "$line" ] || continue
        [ "$first" = 1 ] || printf ','
        first=0
        printf '%s' "$line"
      done <"$FAKE_LOG_DIR/audit.entries"
      printf ']\n'
    }
    ;;
  esac
  ;;
*)
  echo "gcloud: unexpected ${1:-}" >&2
  exit 1
  ;;
esac
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check542.sh's test only: the
# version, and the installation the sandbox checkout belongs to. Nothing here
# reaches a project.
set -euo pipefail
printf '%s\t%s\n' "$PWD" "$*" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-wired}
json=$FAKE_JSON_DIR
case "${1:-}" in
version) printf 'fugaro 0.26.0\n' ;;
doctor)
  case "$mode" in
  belong) cat "$json/doctor-belong.json" ;;
  *) cat "$json/doctor.json" ;;
  esac
  ;;
*)
  echo "fugaro: unexpected ${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

write_fake_curl() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A stand-in for `curl`, for scripts/live-checks/check542.sh's test only: the
# merge mint itself, over a model of its own contract. It reads the CALLER out of
# the curl config file the check wrote (the only place a token ever is), compares
# the head named in the body against the pull request's own head, and answers a
# token or one of the route's typed refusals — and, like the service, appends an
# audit entry for every call a verified caller made and none for a refusal taken
# before the caller is verified. It refuses an argument list this check does not
# make, so a write or a second route would end the case loudly.
set -euo pipefail
out=""
data=""
url=""
runid=""
config=""
while [ $# -gt 0 ]; do
  case "$1" in
  -sS | -s | -S) shift ;;
  -o)
    out=$2
    shift 2
    ;;
  -w)
    shift 2
    ;;
  --config)
    config=$2
    shift 2
    ;;
  --data)
    data=${2#@}
    shift 2
    ;;
  -H)
    case "$2" in
    *Run-Identity*) runid=$2 ;;
    *Content-Type*) ;;
    *)
      printf 'curl: unexpected header %s\n' "$2" >&2
      exit 1
      ;;
    esac
    shift 2
    ;;
  http*)
    url=$1
    shift
    ;;
  *)
    printf 'curl: unexpected %s\n' "$1" >&2
    exit 1
    ;;
  esac
done
mode=${FAKE_MODE:-wired}
json=$FAKE_JSON_DIR
head=$(cat "$json/head")
caller=unknown
if grep -qF "$(cat "$json/daemon-token")" "$config" 2>/dev/null; then caller=daemon; fi
if grep -qF "$(cat "$json/person-token")" "$config" 2>/dev/null; then caller=person; fi
asked=$(sed -n 's/.*"head_sha":"\([^"]*\)".*/\1/p' "$data")
printf '%s\t%s\t%s\t%s\n' "$caller" "${runid:-none}" "$(cat "$data")" "$url" >>"$FAKE_LOG_DIR/curl.calls"

minted=$(printf '{"token":"%s","expires_at":"2026-11-18T01:30:00Z","repository":"chk542-owner/sandbox"}' "$(cat "$json/mint-token")")
status=200
body=$minted
audited=yes
case "$mode" in
inert | no-class-env | account-missing)
  # The class refusal: answered before the head is compared, before the App key is
  # read, and before the caller has an identity for a line to name.
  status=403
  body='{"error":"unverified_job_identity","message":"the caller is not an account this installation names as an orchestrator"}'
  audited=no
  ;;
front-end | no-invoker)
  # Cloud Run's own front end, answering before the container ran: without the
  # invoker grant that is where a call from the daemon's account stops.
  status=403
  body=""
  audited=no
  ;;
route-500)
  status=503
  body='{"error":"state_unavailable","message":"the merge mint is not serving this repository right now"}'
  ;;
esac
if [ "$audited" = yes ]; then
  if [ "$caller" = person ]; then
    if [ "$mode" = mint-person ]; then
      status=200
      body=$minted
    else
      status=403
      body='{"error":"unverified_job_identity","message":"the caller is not this installation'"'"'s orchestrator daemon"}'
      audited=no
    fi
  elif [ "$runid" != "" ] && [ "$runid" != none ]; then
    if [ "$mode" = mint-run-identity ]; then
      status=200
      body=$minted
    else
      status=403
      body='{"error":"unverified_run_identity","message":"this route mints for a caller that holds no run, and this call presented a run identity"}'
    fi
  elif [ "$asked" != "$head" ]; then
    if [ "$mode" = mint-moved-head ]; then
      status=200
      body=$minted
    else
      status=403
      body='{"error":"request_refused","message":"the merge mint is bound to one head, and this pull request is not at the head this call named"}'
    fi
  fi
fi
# A caller the service answered but never audited: the calls of this run are
# admitted and write nothing, which is the one reading the audit row forbids —
# and the log still holds another caller's line with the same status, so a row
# that matched on the status alone would call it this run's.
if [ "$mode" = audit-stale ]; then audited=no; fi
if [ "$audited" = yes ]; then
  audit_status=ok
  [ "$status" = 200 ] || audit_status=$(sed -n 's/.*"error":"\([^"]*\)".*/\1/p' <<<"$body")
  # Cloud Logging stamps a line with the instant it receives it, and the check
  # binds a line to this run by that instant against the instant the call went
  # out: a fake that stamped every line with one fixed moment would make the row
  # unmeasurable rather than measured.
  printf '{"jsonPayload":{"message":"credsvc: audit","purpose":"merge_mint","caller":"fugaro-orch@chk542-test.iam.gserviceaccount.com","repository":"chk542-owner/sandbox","job":"","credential":"fugaro-merge-mint","tier":"","status":"%s","slip":"","owner":"","run":""},"timestamp":"%s"}\n' \
    "$audit_status" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >>"$FAKE_LOG_DIR/audit.entries"
fi
if [ -n "$out" ]; then
  # `curl -o` creates the file whatever comes back, and the check reads an empty
  # body as a different answer from a body it cannot parse: a front end's refusal
  # has to leave nothing at all here.
  : >"$out"
  if [ -n "$body" ]; then printf '%s\n' "$body" >"$out"; fi
fi
printf '%s' "$status"
CURL_EOF
  chmod +x "$1/bin/curl"
}

# The documents the fakes serve: one file per shape, so a case bends exactly the
# one it is about. Every one of them is a document the real tool answers with —
# the check reads them with its own reader, which knows these shapes and no
# others.
write_json() { # dir
  mkdir -p "$1/json"
  printf '%s\n' "$head_one" >"$1/json/head"
  printf '%s\n' "$id_person" >"$1/json/person-token"
  printf '%s\n' "$id_daemon" >"$1/json/daemon-token"
  printf '%s\n' "$mint_token" >"$1/json/mint-token"
  cat >"$1/json/name.json" <<JSON
{"nameWithOwner":"${slug}"}
JSON
  cat >"$1/json/doctor.json" <<JSON
{"ok":true,"checks":[{"name":"credentials","status":"ok"}],"project":{"name":"chk542","gcp_project":"${project}","region":"europe-west1"}}
JSON
  cat >"$1/json/doctor-belong.json" <<JSON
{"ok":true,"project":{"name":"belong","gcp_project":"belong-prod","region":"europe-west1"}}
JSON
  cat >"$1/json/pr.json" <<JSON
{"number":7,"state":"OPEN","isDraft":false,"headRefOid":"${head_one}","baseRefName":"main","url":"https://github.com/${slug}/pull/7"}
JSON
  cat >"$1/json/pr-closed.json" <<JSON
{"number":7,"state":"CLOSED","isDraft":false,"headRefOid":"${head_one}","baseRefName":"main","url":"https://github.com/${slug}/pull/7"}
JSON
  # The App's own installation and the permissions it has been granted on this
  # repository: the merge set the mint hands out, and the read its gate makes.
  cat >"$1/json/installations.json" <<JSON
{"installations":[{"id":${installation},"app_id":${app_id},"app_slug":"${app_slug}","repository_selection":"selected","permissions":{"contents":"write","pull_requests":"write","administration":"read","metadata":"read"}},{"id":5151,"app_id":888,"app_slug":"another-app","repository_selection":"all","permissions":{"contents":"read"}}]}
JSON
  cat >"$1/json/installations-other.json" <<JSON
{"installations":[{"id":5151,"app_id":888,"app_slug":"another-app","repository_selection":"all","permissions":{"contents":"read"}}]}
JSON
  cat >"$1/json/repos.json" <<JSON
{"repositories":[{"full_name":"${slug}"},{"full_name":"chk542-owner/another"}]}
JSON
  cat >"$1/json/repos-other.json" <<JSON
{"repositories":[{"full_name":"chk542-owner/another"}]}
JSON
  cat >"$1/json/rulesets.json" <<JSON
[{"id":11}]
JSON
  cat >"$1/json/ruleset.json" <<JSON
{"id":11,"name":"protect-main","target":"branch","enforcement":"active","bypass_actors":[],"current_user_can_bypass":"never","conditions":{"ref_name":{"include":["refs/heads/main"],"exclude":[]}}}
JSON
  cat >"$1/json/ruleset-bypass.json" <<JSON
{"id":11,"name":"protect-main","target":"branch","enforcement":"active","bypass_actors":[{"actor_id":${app_id},"actor_type":"Integration","bypass_mode":"always"}],"current_user_can_bypass":"never","conditions":{"ref_name":{"include":["refs/heads/main"],"exclude":[]}}}
JSON
  cat >"$1/json/ruleset-no-list.json" <<JSON
{"id":11,"name":"protect-main","target":"branch","enforcement":"active","conditions":{"ref_name":{"include":["refs/heads/main"],"exclude":[]}}}
JSON
  cat >"$1/json/protection.json" <<JSON
{"required_pull_request_reviews":{"dismissal_restrictions":{},"require_code_owner_reviews":false,"required_approving_review_count":1,"bypass_pull_request_allowances":{"users":[],"teams":[],"apps":[]}},"enforce_admins":{"enabled":true}}
JSON
  cat >"$1/json/protection-bypass.json" <<JSON
{"required_pull_request_reviews":{"dismissal_restrictions":{},"require_code_owner_reviews":false,"required_approving_review_count":1,"bypass_pull_request_allowances":{"users":[],"teams":[],"apps":[{"id":${app_id},"slug":"${app_slug}"}]}},"enforce_admins":{"enabled":true}}
JSON
  cat >"$1/json/service.json" <<JSON
{"status":{"url":"${service_url}"},"spec":{"template":{"spec":{"containers":[{"env":[{"name":"FUGARO_PROJECT","value":"${project}"},{"name":"FUGARO_ORCHESTRATOR_ACCOUNTS","valueSource":{"secretKeyRef":{"name":"orchestrator","key":"latest"}}},{"name":"FUGARO_JOB_ACCOUNTS","value":"jobs@${project}.iam.gserviceaccount.com"}]}]}}}}
JSON
  cat >"$1/json/service-inert.json" <<JSON
{"status":{"url":"${service_url}"},"spec":{"template":{"spec":{"containers":[{"env":[{"name":"FUGARO_PROJECT","value":"${project}"},{"name":"FUGARO_JOB_ACCOUNTS","value":"jobs@${project}.iam.gserviceaccount.com"}]}]}}}}
JSON
  cat >"$1/json/service-no-class.json" <<JSON
{"status":{"url":"${service_url}"},"spec":{"template":{"spec":{"containers":[{"env":[{"name":"FUGARO_PROJECT","value":"${project}"},{"name":"FUGARO_ORCHESTRATOR_ACCOUNTS","value":""}]}]}}}}
JSON
  cat >"$1/json/iam.json" <<JSON
{"bindings":[{"role":"roles/run.invoker","members":["serviceAccount:${daemon}"]},{"role":"roles/run.developer","members":["user:chk542-owner@example.com"]}]}
JSON
  cat >"$1/json/iam-no-invoker.json" <<JSON
{"bindings":[{"role":"roles/run.invoker","members":["group:everyone@example.com"]}]}
JSON
  # The one line the audit rule forbids: a status the service answers BEFORE the
  # caller is verified, which has no identity for a line to name.
  cat >"$1/json/audit-before-verification.json" <<JSON
[{"jsonPayload":{"message":"credsvc: audit","purpose":"merge_mint","caller":"nobody@example.com","repository":"${slug}","status":"unverified_job_identity","credential":"fugaro-merge-mint"},"timestamp":"2026-11-18T00:30:00Z"}]
JSON
  # Another installation's caller, on another repository, with the three statuses
  # this run's admitted calls would carry — each written an hour before those
  # calls. The instant is relative to now rather than fixed, because what the case
  # needs is lines that are OLDER than the calls the check is about to make,
  # whatever the clock says.
  stale_stamp=$(python3 -c 'import datetime
print((datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(hours=1)).strftime("%Y-%m-%dT%H:%M:%SZ"))')
  cat >"$1/json/audit-stale.json" <<JSON
[{"jsonPayload":{"message":"credsvc: audit","purpose":"merge_mint","caller":"fugaro-orch@chk542-other.iam.gserviceaccount.com","repository":"chk542-other/other","job":"","credential":"fugaro-merge-mint","status":"ok"},"timestamp":"${stale_stamp}"},
 {"jsonPayload":{"message":"credsvc: audit","purpose":"merge_mint","caller":"fugaro-orch@chk542-other.iam.gserviceaccount.com","repository":"chk542-other/other","job":"","credential":"fugaro-merge-mint","status":"request_refused"},"timestamp":"${stale_stamp}"},
 {"jsonPayload":{"message":"credsvc: audit","purpose":"merge_mint","caller":"fugaro-orch@chk542-other.iam.gserviceaccount.com","repository":"chk542-other/other","job":"","credential":"fugaro-merge-mint","status":"unverified_run_identity"},"timestamp":"${stale_stamp}"}]
JSON
}

prepare() { # dir
  mkdir -p "$1/home" "$1/tmp" "$1/sandbox"
  printf 'name: sandbox\n' >"$1/sandbox/fugaro.yaml"
  write_fake_gh "$1"
  write_fake_gcloud "$1"
  write_fake_fugaro "$1"
  write_fake_curl "$1"
  write_json "$1"
}

# ---------------------------------------------------------------- the harness
fresh_logs() { # dir
  mkdir -p "$1/logs"
  local f
  for f in gh gcloud fugaro curl; do : >"$1/logs/$f.calls"; done
  : >"$1/logs/audit.entries"
}

run_check() { # run_check DIR ENV... [-- ARGS...]
  local dir=$1
  shift
  local args=() cli=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    args+=("$1")
    shift
  done
  if [ "${1:-}" = "--" ]; then
    shift
    cli=("$@")
  fi
  mkdir -p "$dir/state" "$dir/home" "$dir/tmp"
  fresh_logs "$dir"
  status=0
  # `-x` is bash's own flag, so it goes BEFORE the script: the check refuses an
  # argument it does not know, and the case that measures that refusal is the
  # only way to trace a run that is refusing to be traced.
  local bashargs=()
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  # The five markers a coding agent's session carries are stripped first, so the
  # empty environment is this harness's own default: a case that means to be
  # refused puts one back by name, and a case that does not must not inherit this
  # container's. They are unset BEFORE the assignments, so an assignment for one
  # of them still lands.
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_MODE="${FAKE_MODE:-wired}" FAKE_JSON_DIR="$dir/json" \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" \
    --sandbox "$dir/sandbox" --pr 7 ${cli[@]+"${cli[@]}"} \
    >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

run_copy() { # run_copy DIR SCRIPT ENV...
  local dir=$1 copy=$2
  shift 2
  mkdir -p "$dir/state" "$dir/home" "$dir/tmp"
  fresh_logs "$dir"
  status=0
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_MODE="${FAKE_MODE:-wired}" FAKE_JSON_DIR="$dir/json" \
    ${1+"$@"} \
    bash "$copy" --sandbox "$dir/sandbox" --pr 7 \
    >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

# ------------------------------------------------------------------ assertions
assert_scratch_removed() { # what
  [ -n "$scratch" ] || {
    fail "$1: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$1: the scratch directory $scratch is still there"
}

assert_ran_in_the_sandbox() { # dir what
  local line
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    case "$line" in
    # The one call the check makes before it has a directory to run in: it reads
    # the account it runs as, which is not a step of the installation.
    *$'\t'config\ get-value\ account) continue ;;
    "$1/sandbox"$'\t'*) ;;
    *) fail "$2: a command the check ran was called from ${line%%$'\t'*} and not from the sandbox checkout" ;;
    esac
  done < <(cat "$1/logs/gcloud.calls" "$1/logs/gh.calls" 2>/dev/null || true)
}

# The two things this check must never do to the host: merge, and write anything.
assert_wrote_nothing_to_github() { # dir what
  local calls
  calls=$(cat "$1/logs/gh.calls" 2>/dev/null || true)
  case "$calls" in
  *$'\t'pr\ merge*) fail "$2: the check merged a pull request" ;;
  esac
  case "$calls" in
  *'-X '* | *'--method '* | *$'\t'api\ -f* | *$'\t'api\ --field* | *$'\t'repo\ edit*)
    fail "$2: the check wrote to GitHub: $calls"
    ;;
  esac
}

# gcloud's own writes: the credential bundle, an account, a binding — none of them
# this check's to make.
assert_wrote_nothing_to_gcloud() { # dir what
  local calls
  calls=$(cat "$1/logs/gcloud.calls" 2>/dev/null || true)
  case "$calls" in
  *"creds set"* | *" service-accounts create"* | *" service-accounts delete"* | *" add-iam-policy-binding"* | *"set-iam-policy"* | *" services update"* | *" services delete"*)
    fail "$2: the check wrote to the project: $calls"
    ;;
  esac
}

# Only the argument lists this check means to make, and nothing else: a row that
# writes `gh api …` inside a double-quoted string runs that command, and the hint
# it meant to print becomes a call the owner never asked for.
assert_only_known_calls() { # dir what
  local line n=0
  while IFS= read -r line; do
    n=$((n + 1))
    case "${line#*$'\t'}" in
    version | "doctor --json") ;;
    *) fail "$2: the check ran 'fugaro ${line#*$'\t'}' from ${line%%$'\t'*}, an argument list this check does not make" ;;
    esac
  done <"$1/logs/fugaro.calls"
  [ "$n" -gt 0 ] || fail "$2: no fugaro call was recorded at all"
  n=0
  while IFS= read -r line; do
    n=$((n + 1))
    case "${line#*$'\t'}" in
    "repo view --json nameWithOwner" | "pr view 7 --repo ${slug} --json state,isDraft,headRefOid,baseRefName,url,number") ;;
    "api /user/installations?per_page=100" | "api /user/installations/"*"/repositories?per_page=100") ;;
    "api /repos/${slug}/rulesets?per_page=100" | "api /repos/${slug}/rulesets/"*) ;;
    "api --include --silent /repos/${slug}/branches/main/protection" | "api /repos/${slug}/branches/main/protection") ;;
    *) fail "$2: the check ran 'gh ${line#*$'\t'}' from ${line%%$'\t'*}, an argument list this check does not make" ;;
    esac
  done <"$1/logs/gh.calls"
  [ "$n" -gt 0 ] || fail "$2: no gh call was recorded at all"
  n=0
  while IFS= read -r line; do
    n=$((n + 1))
    case "${line#*$'\t'}" in
    "config get-value account") ;;
    "iam service-accounts describe ${daemon} --project=${project} --format=json") ;;
    "run services describe fugaro-credentials --project=${project} --region=europe-west1 --format=json") ;;
    "run services get-iam-policy fugaro-credentials --project=${project} --region=europe-west1 --format=json") ;;
    "auth print-identity-token --impersonate-service-account=${daemon} --audiences=${service_url} --include-email" | "auth print-identity-token") ;;
    "logging read "*" --project=${project} --format=json --limit=100 --freshness=30m --order=desc") ;;
    *) fail "$2: the check ran 'gcloud ${line#*$'\t'}' from ${line%%$'\t'*}, an argument list this check does not make" ;;
    esac
  done <"$1/logs/gcloud.calls"
  [ "$n" -gt 0 ] || fail "$2: no gcloud call was recorded at all"
  n=0
  while IFS= read -r line; do
    n=$((n + 1))
    case "${line##*$'\t'}" in
    "${service_url}/v1/github/merge-token") ;;
    *) fail "$2: the check called '${line##*$'\t'}' and the merge mint is the only route it may call" ;;
    esac
  done <"$1/logs/curl.calls"
}

# The check's own hygiene: no credential a tool handed it reaches the block, the
# log or a file this test can read afterwards.
assert_no_token_printed() { # what
  expect_absent "$out" "$mint_token" "$1: the mint's token in the block"
  expect_absent "$err" "$mint_token" "$1: the mint's token on stderr"
  expect_absent "$out" "$id_daemon" "$1: the daemon's ID token in the block"
  expect_absent "$err" "$id_daemon" "$1: the daemon's ID token on stderr"
  expect_absent "$out" "$id_person" "$1: the person's ID token in the block"
  expect_absent "$err" "$id_person" "$1: the person's ID token on stderr"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "check542.sh --sandbox" "--help: the form you type"
  expect_contains "$out" "merge-token" "--help: the route it asks is named"
  expect_absent "$out" "FACT " "--help: no block is printed"
  expect_absent "$(cat "$dir/logs/gh.calls")" "api" "--help: nothing is read"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK542_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 542 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version row"
  expect_contains "$out" "FACT belong: PASS" "dry run: the belong guard"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore row"
  expect_contains "$out" "CHECK 542 RESULT END (exit 0)" "dry run: the end line"
  # A row the plan did not measure reads INFO, never PASS.
  expect_contains "$out" "FACT mint: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$err" "the token that comes back is written into the scratch directory" "dry run: the plan says what happens to the token"
  expect_absent "$(cat "$dir/logs/gh.calls")" "installations" "dry run: no GitHub call is made"
  expect_absent "$(cat "$dir/logs/curl.calls")" "merge-token" "dry run: no mint is asked for"
  assert_scratch_removed "dry run"
}

test_happy_path() {
  # The wired installation: the App holds the merge set and the gate's read, it is
  # on neither bypass list, the daemon is named by the service's own class list and
  # may invoke it, the mint answers at the head and refuses a moved one and a run
  # identity, the person's token is refused before anything is read, and every call
  # an admitted caller made has its audit line.
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "FACT installation: PASS" "happy path: the installation row"
  expect_contains "$out" "FACT pull-request: PASS" "happy path: the pull request row"
  expect_contains "$out" "FACT app-installation: PASS" "happy path: the App's permissions"
  expect_contains "$out" "contents: write, pull_requests: write, administration: read" "happy path: the three permissions, named"
  expect_contains "$out" "FACT bypass: PASS" "happy path: neither bypass list names the App"
  expect_contains "$out" "FACT service: PASS" "happy path: the service row"
  expect_contains "$out" "FACT daemon-identity: PASS" "happy path: the daemon is wired"
  expect_contains "$out" "FACT mint: PASS" "happy path: the mint"
  expect_contains "$out" "expires_at 2026-11-18T01:30:00Z" "happy path: the expiry the answer carried"
  expect_contains "$out" "FACT mint-moved-head: PASS" "happy path: the moved head is refused"
  expect_contains "$out" "request_refused" "happy path: with the route's own refusal"
  expect_contains "$out" "FACT mint-person-token: PASS" "happy path: the person's token is refused"
  expect_contains "$out" "FACT mint-run-identity: PASS" "happy path: a run identity is refused by name"
  expect_contains "$out" "unverified_run_identity" "happy path: the by-name refusal"
  expect_contains "$out" "FACT audit: PASS" "happy path: the audit lines"
  expect_contains "$out" "FACT scratch-removed: PASS" "happy path: the scratch is gone"
  expect_contains "$out" "CHECK 542 RESULT END (exit 0)" "happy path: the end line"
  # The mint's body is the contract's own four fields, and the head it names is the
  # pull request's — which is the whole binding.
  expect_contains "$(cat "$dir/logs/curl.calls")" "\"repository\":\"${slug}\",\"number\":7,\"head_sha\":\"${head_one}\",\"app_id\":\"${app_id}\"" "happy path: the mint's body"
  expect_contains "$(cat "$dir/logs/curl.calls")" "head_sha\":\"${head_moved}\"" "happy path: the moved-head call names a head the pull request is not at"
  expect_contains "$(cat "$dir/logs/curl.calls")" "X-Fugaro-Run-Identity: chk542-probe" "happy path: the run-identity call presents the header"
  expect_contains "$(cat "$dir/logs/curl.calls")" $'person\tnone' "happy path: the person's own credential is used for their call"
  assert_no_token_printed "happy path"
  assert_wrote_nothing_to_github "$dir" "happy path"
  assert_wrote_nothing_to_gcloud "$dir" "happy path"
  assert_only_known_calls "$dir" "happy path"
  assert_ran_in_the_sandbox "$dir" "happy path"
  assert_scratch_removed "happy path"
}

test_a_permission_the_app_does_not_hold_is_a_finding() {
  # administration read is the gate's own read: without it the gate fails closed
  # and no token is ever minted, which is why the row asks for it.
  local dir=$work/noadmin
  prepare "$dir"
  perl -pi -e 's/"administration":"read",//' "$dir/json/installations.json"
  run_check "$dir"
  expect_status "$status" 1 "a permission the App does not hold"
  expect_contains "$out" "FACT app-installation: FAIL" "a permission the App does not hold: the row"
  expect_contains "$out" "administration=none" "a permission the App does not hold: which one"
  expect_contains "$out" "mergeGateReadPermissions" "a permission the App does not hold: why the gate needs it"
  expect_contains "$out" "D2048" "a permission the App does not hold: whose step it is"
  # The rows are independent: the permission is the finding, and the mint is still
  # asked (and refused on the service's own terms) so the owner reads both.
  expect_contains "$out" "FACT bypass: PASS" "a permission the App does not hold: the bypass lists are read anyway"
  expect_contains "$(cat "$dir/logs/curl.calls")" "/v1/github/merge-token" "a permission the App does not hold: the mint is still measured"
}

test_the_app_not_installed_on_the_repository_is_a_finding() {
  local dir=$work/notinstalled
  prepare "$dir"
  run_check "$dir" FAKE_MODE=app-not-installed
  expect_status "$status" 1 "the App not installed here"
  expect_contains "$out" "FACT app-installation: FAIL" "the App not installed here: the row"
  expect_contains "$out" "no App installation the token your gh carries sees covers ${slug}" "the App not installed here: what it found"
  expect_contains "$out" "github.com/settings/apps" "the App not installed here: where the owner installs it"
  expect_absent "$(cat "$dir/logs/curl.calls")" "merge-token" "the App not installed here: no mint without an App ID"
}

test_a_full_installations_page_is_a_finding() {
  # A page cannot be told from a truncated list, and this check will not pick the
  # App's installation out of a list it cannot see the end of (the rule the mint's
  # own gate reads a ruleset page by, G14).
  local dir=$work/fullinst
  prepare "$dir"
  run_check "$dir" FAKE_MODE=installations-full
  expect_status "$status" 1 "a full installations page"
  expect_contains "$out" "FACT app-installation: FAIL" "a full installations page: the row"
  expect_contains "$out" "is full (100 installations)" "a full installations page: why"
  expect_contains "$out" "--app-id" "a full installations page: the way round it"
}

test_the_app_on_a_ruleset_bypass_list_is_a_finding() {
  local dir=$work/bypassruleset
  prepare "$dir"
  run_check "$dir" FAKE_MODE=bypass-ruleset
  expect_status "$status" 1 "the App on a ruleset's bypass list"
  expect_contains "$out" "FACT bypass: FAIL" "the App on a ruleset's bypass list: the row"
  expect_contains "$out" "Integration:${app_id}" "the App on a ruleset's bypass list: it is named"
  expect_contains "$out" "G14" "the App on a ruleset's bypass list: the ruling"
}

test_a_ruleset_with_no_bypass_list_is_a_finding() {
  # A covering ruleset that answers no bypass list at all is a HIDDEN list, and a
  # list this read cannot see is not one that names nobody (G14).
  local dir=$work/nolist
  prepare "$dir"
  run_check "$dir" FAKE_MODE=ruleset-no-list
  expect_status "$status" 1 "a ruleset with no bypass list"
  expect_contains "$out" "FACT bypass: FAIL" "a ruleset with no bypass list: the row"
  expect_contains "$out" "NO bypass list" "a ruleset with no bypass list: what it found"
}

test_a_full_ruleset_page_is_a_finding() {
  local dir=$work/fullrules
  prepare "$dir"
  run_check "$dir" FAKE_MODE=rulesets-full
  expect_status "$status" 1 "a full ruleset page"
  expect_contains "$out" "FACT bypass: FAIL" "a full ruleset page: the row"
  expect_contains "$out" "cannot be told from a truncated one" "a full ruleset page: why"
}

test_a_ruleset_that_cannot_be_read_again_is_unmeasured() {
  # GitHub's list answers a summary and the bypass list is only in the per-id
  # answer: a list this check did not read is not one it may call absent.
  local dir=$work/rsunreadable
  prepare "$dir"
  run_check "$dir" FAKE_MODE=ruleset-unreadable
  expect_status "$status" 2 "a ruleset that cannot be read again"
  expect_contains "$out" "FACT bypass: FAIL" "a ruleset that cannot be read again: the row"
  expect_contains "$out" "could not be read again by id" "a ruleset that cannot be read again: what happened"
}

test_the_app_on_the_classic_protections_bypass_list_is_a_finding() {
  local dir=$work/classicbypass
  prepare "$dir"
  run_check "$dir" FAKE_MODE=classic-bypass
  expect_status "$status" 1 "the App on the classic bypass list"
  expect_contains "$out" "FACT bypass: FAIL" "the App on the classic bypass list: the row"
  expect_contains "$out" "classic branch protection of main names the App (app id ${app_id})" "the App on the classic bypass list: where"
}

test_a_classic_protection_that_answers_another_status_is_a_finding() {
  local dir=$work/proterror
  prepare "$dir"
  run_check "$dir" FAKE_MODE=protection-error
  expect_status "$status" 1 "a protection read that answered 500"
  expect_contains "$out" "FACT bypass: FAIL" "a protection read that answered 500: the row"
  expect_contains "$out" "answered HTTP 500" "a protection read that answered 500: the status"
}

test_a_protection_read_that_answers_no_status_is_unmeasured() {
  local dir=$work/protnostatus
  prepare "$dir"
  run_check "$dir" FAKE_MODE=protection-nostatus
  expect_status "$status" 2 "a protection read with no status"
  expect_contains "$out" "FACT bypass: FAIL" "a protection read with no status: the row"
  expect_contains "$out" "did not answer a status" "a protection read with no status: what happened"
}

test_a_closed_pull_request_is_a_finding() {
  local dir=$work/closed
  prepare "$dir"
  run_check "$dir" FAKE_MODE=pr-closed
  expect_status "$status" 1 "a closed pull request"
  expect_contains "$out" "FACT pull-request: FAIL" "a closed pull request: the row"
  expect_contains "$out" "is CLOSED" "a closed pull request: its state"
  # A mint on a pull request the gate refuses before it compares a head would be
  # four calls that measure nothing: the rows read `not measured` instead, and
  # none of them is asked.
  expect_contains "$out" "FACT mint: FAIL — this call needs the head the call is bound to" "a closed pull request: the mint row points at the pull request"
  expect_absent "$(cat "$dir/logs/curl.calls")" "merge-token" "a closed pull request: no mint is asked for"
  expect_absent "$(cat "$dir/logs/audit.entries")" "credsvc" "a closed pull request: nothing reaches the service"
}

test_the_daemon_identity_missing_is_a_finding() {
  # The state this check was written against: the account does not exist, no
  # environment entry names a class, and nothing grants the invoker role. The row
  # must name all three halves and the files, not report a mystery refusal.
  local dir=$work/inert
  prepare "$dir"
  run_check "$dir" FAKE_MODE=inert
  expect_status "$status" 1 "the route inert"
  expect_contains "$out" "FACT daemon-identity: FAIL" "the route inert: the row"
  expect_contains "$out" "the account ${daemon} could not be read" "the route inert: the account"
  expect_contains "$out" "no environment entry of the service's revision names an orchestrator class" "the route inert: the class list"
  expect_contains "$out" "holds no roles/run.invoker" "the route inert: the grant"
  expect_contains "$out" "internal/cli/credentials_serve.go" "the route inert: the file that would carry it"
  expect_contains "$out" "internal/cli/orchestrator_labels.go" "the route inert: the daemon's label seam"
  expect_contains "$out" "orchestratorLabelIdentity is nil" "the route inert: the seam, by name"
  expect_contains "$out" "the route ships inert on this tree" "the route inert: SECURITY.md's own words"
  expect_contains "$out" "FACT mint: FAIL" "the route inert: the mint row"
  expect_contains "$out" "the service refused the daemon's own token with unverified_job_identity" "the route inert: the class's own refusal"
  expect_contains "$out" "FACT mint-person-token: PASS" "the route inert: the person's token is still refused"
  expect_contains "$out" "FACT audit: PASS" "the route inert: no line for a call refused before verification"
  assert_no_token_printed "the route inert"
}

test_a_class_named_but_no_invoker_grant_is_a_finding() {
  # Half the identity wired: the account exists and the class entry is there, and
  # the account may not invoke the service. The row names that half and no other.
  local dir=$work/noinvoker
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-invoker
  expect_status "$status" 1 "half the identity wired"
  expect_contains "$out" "FACT daemon-identity: FAIL" "half the identity wired: the row"
  expect_contains "$out" "${daemon} holds no roles/run.invoker" "half the identity wired: the missing half"
  expect_absent "$out" "could not be read (gcloud iam service-accounts describe" "half the identity wired: the account half is not reported missing"
}

test_a_service_that_is_not_deployed_is_a_finding() {
  local dir=$work/noservice
  prepare "$dir"
  run_check "$dir" FAKE_MODE=service-missing
  expect_status "$status" 1 "a service that is not deployed"
  expect_contains "$out" "FACT service: FAIL" "a service that is not deployed: the row"
  expect_contains "$out" "fugaro update deploys that service" "a service that is not deployed: what to do"
  expect_absent "$(cat "$dir/logs/curl.calls")" "merge-token" "a service that is not deployed: no mint is attempted"
}

test_a_daemon_token_that_cannot_be_minted_is_a_finding() {
  local dir=$work/tokenfails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=token-fails
  expect_status "$status" 1 "a daemon token that cannot be minted"
  expect_contains "$out" "FACT mint: FAIL" "a daemon token that cannot be minted: the row"
  expect_contains "$out" "could not mint an ID token for ${daemon}" "a daemon token that cannot be minted: what failed"
  expect_contains "$out" "roles/iam.serviceAccountTokenCreator" "a daemon token that cannot be minted: the other reading"
  expect_contains "$out" "FACT mint-person-token: FAIL" "a daemon token that cannot be minted: the person's row could not be measured either"
  expect_absent "$(cat "$dir/logs/curl.calls")" "merge-token" "a daemon token that cannot be minted: no call is made"
}

test_a_moved_head_that_is_minted_anyway_is_the_finding() {
  local dir=$work/movedminted
  prepare "$dir"
  run_check "$dir" FAKE_MODE=mint-moved-head
  expect_status "$status" 1 "a mint answered for a moved head"
  expect_contains "$out" "FACT mint: PASS" "a mint answered for a moved head: the head it is at is minted"
  expect_contains "$out" "FACT mint-moved-head: FAIL" "a mint answered for a moved head: the row"
  expect_contains "$out" "MINTED A TOKEN" "a mint answered for a moved head: what it found"
  expect_contains "$out" "G2" "a mint answered for a moved head: the binding"
  assert_no_token_printed "a mint answered for a moved head"
}

test_a_run_identity_that_is_minted_is_the_finding() {
  local dir=$work/runminted
  prepare "$dir"
  run_check "$dir" FAKE_MODE=mint-run-identity
  expect_status "$status" 1 "a run identity that is minted"
  expect_contains "$out" "FACT mint-run-identity: FAIL" "a run identity that is minted: the row"
  expect_contains "$out" "MINTED a merge token to a call that presented a run identity" "a run identity that is minted: what it found"
}

test_a_person_token_that_is_minted_is_the_finding() {
  # The credential a coding-agent session on the owner's machine holds, buying a
  # merge credential: the finding this row exists for.
  local dir=$work/personminted
  prepare "$dir"
  run_check "$dir" FAKE_MODE=mint-person
  expect_status "$status" 1 "a person's token that is minted"
  expect_contains "$out" "FACT mint-person-token: FAIL" "a person's token that is minted: the row"
  expect_contains "$out" "MINTED A MERGE TOKEN" "a person's token that is minted: what it found"
  expect_contains "$out" "Report this before anything else" "a person's token that is minted: how loud the row is"
  assert_no_token_printed "a person's token that is minted"
}

test_a_state_refusal_of_an_admitted_caller_is_reported() {
  local dir=$work/state500
  prepare "$dir"
  run_check "$dir" FAKE_MODE=route-500
  expect_status "$status" 1 "a state refusal"
  expect_contains "$out" "FACT mint: FAIL" "a state refusal: the row"
  expect_contains "$out" "state_unavailable" "a state refusal: the typed refusal"
  expect_contains "$out" "FACT audit: PASS" "a state refusal: the admitted call still has its line"
}

test_a_call_that_never_reached_the_route_is_reported() {
  local dir=$work/frontend
  prepare "$dir"
  run_check "$dir" FAKE_MODE=front-end
  expect_status "$status" 1 "a front end refusal"
  expect_contains "$out" "FACT mint: FAIL" "a front end refusal: the row"
  expect_contains "$out" "roles/run.invoker" "a front end refusal: the grant the front end answers about"
  expect_contains "$out" "FACT mint-person-token: PASS" "a front end refusal: the person's refusal, one step stronger"
  expect_contains "$out" "FACT audit: INFO" "a front end refusal: nothing of the rule to read"
  expect_absent "$(cat "$dir/logs/audit.entries")" "credsvc" "a front end refusal: nothing reached the service to be audited"
}

test_an_audit_line_before_verification_is_a_finding() {
  local dir=$work/auditwrong
  prepare "$dir"
  run_check "$dir" FAKE_MODE=audit-unverified
  expect_status "$status" 1 "an audit line before verification"
  expect_contains "$out" "FACT audit: FAIL" "an audit line before verification: the row"
  expect_contains "$out" "status unverified_job_identity" "an audit line before verification: the status"
  expect_contains "$out" "there is no identity for a line to name" "an audit line before verification: why it may not be written"
}

test_an_admitted_call_with_no_audit_line_is_a_finding() {
  local dir=$work/auditmissing
  prepare "$dir"
  run_check "$dir" FAKE_MODE=audit-drop
  expect_status "$status" 1 "an admitted call with no audit line"
  expect_contains "$out" "FACT mint: PASS" "an admitted call with no audit line: the mint itself held"
  expect_contains "$out" "FACT audit: FAIL" "an admitted call with no audit line: the row"
  expect_contains "$out" "wrote no merge_mint line of this run's with status ok" "an admitted call with no audit line: which line is missing"
}

# The line has to be THIS run's. The log holds another caller's line with the
# status an admitted call of this run would carry, written an hour before the
# calls — and this run's own calls wrote none. A row that matched the status over
# the window would call that older line this run's and PASS a call the service
# never audited, which is the finding this case pins.
test_an_older_line_with_the_same_status_is_not_this_runs() {
  local dir=$work/auditstale
  prepare "$dir"
  run_check "$dir" FAKE_MODE=audit-stale
  expect_status "$status" 1 "an older line with the same status"
  expect_contains "$out" "FACT mint: PASS" "an older line: the mint itself held, so an admitted call is missing its line"
  expect_contains "$out" "FACT audit: FAIL" "an older line: the row"
  expect_contains "$out" "at or after the instant the call went out" "an older line: the binding the row matched on"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- --xtrace
  expect_status "$status" 1 "xtrace"
  expect_contains "$out" "FACT xtrace: FAIL" "xtrace: the row"
  expect_contains "$out" "this shell is tracing" "xtrace: why it is refused"
  expect_contains "$err" "a trace of this run would copy the ID token" "xtrace: what a trace would carry"
  expect_absent "$(cat "$dir/logs/curl.calls")" "merge-token" "xtrace: nothing is minted"
}

test_an_agent_session_is_refused() {
  # The merge mint is the credential the gate exists to keep out of an agent's
  # session, so no step of this check runs in one.
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "an agent's session"
  expect_contains "$out" "FACT aborted: FAIL" "an agent's session: the aborted row"
  expect_contains "$err" "CLAUDECODE is set" "an agent's session: the marker, by name"
  expect_contains "$err" "no agent runs any step of it" "an agent's session: why"
  expect_absent "$(cat "$dir/logs/gh.calls")" "api" "an agent's session: nothing is read"
  expect_absent "$(cat "$dir/logs/curl.calls")" "merge-token" "an agent's session: no mint is asked for"
}

test_an_account_naming_belong_is_refused() {
  local dir=$work/belongaccount
  prepare "$dir"
  run_check "$dir" FAKE_MODE=belong-account
  expect_status "$status" 1 "an account naming belong"
  expect_contains "$out" "FACT aborted: FAIL" "an account naming belong: the aborted row"
  expect_contains "$err" "names belong" "an account naming belong: the reason"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "services describe" "an account naming belong: nothing there is read"
  expect_absent "$(cat "$dir/logs/gh.calls")" "api" "an account naming belong: no GitHub call is made"
}

test_a_checkout_belonging_to_belong_is_refused() {
  # The second half of the same fence: the account is the owner's own and the
  # installation the checkout belongs to is belong's, which is read from doctor
  # and refused where it is read.
  local dir=$work/belongcheckout
  prepare "$dir"
  run_check "$dir" FAKE_MODE=belong
  expect_status "$status" 1 "a checkout belonging to belong"
  expect_contains "$out" "FACT aborted: FAIL" "a checkout belonging to belong: the aborted row"
  expect_contains "$err" "belongs to belong-prod" "a checkout belonging to belong: the reason"
  expect_absent "$(cat "$dir/logs/gh.calls")" "api" "a checkout belonging to belong: nothing there is read"
  expect_absent "$(cat "$dir/logs/curl.calls")" "merge-token" "a checkout belonging to belong: no mint is asked for"
}

test_kept_scratch_is_reported_and_the_token_is_still_scrubbed() {
  # KEEP_SCRATCH is for a run that failed; the one thing it may not keep is the
  # installation token a successful mint answered with, and the check removes it
  # before the scratch is dealt with at all.
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK542_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$err" "CHECK542_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
  expect_contains "$err" "is now removed" "a kept scratch directory: the scrub says so"
  [ -d "$scratch" ] || fail "a kept scratch directory: $scratch was removed anyway"
  if grep -rq "$mint_token" "$scratch" 2>/dev/null; then
    fail "a kept scratch directory: the mint's token is still in $scratch"
  fi
  rm -rf "$scratch" 2>/dev/null || true
}

test_an_unknown_argument_is_refused() {
  local dir=$work/arg
  prepare "$dir"
  run_check "$dir" -- --wat
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --wat" "an unknown argument: the message"
}

test_a_missing_pr_is_die_not_a_prompt() {
  # Fugaro asks for nothing: the flag this check cannot work without is a refusal
  # with the fix on the same line, never a question and never a wait on stdin.
  local dir=$work/nopr
  prepare "$dir"
  fresh_logs "$dir"
  status=0
  env PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_JSON_DIR="$dir/json" \
    bash "$script" --sandbox "$dir/sandbox" <"/dev/null" >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  expect_status "$status" 2 "no --pr"
  expect_contains "$err" "Check 542 needs --pr" "no --pr: the refusal"
  expect_contains "$err" "the merge mint is asked about" "no --pr: what the pull request has to be"
  expect_absent "$(cat "$dir/logs/gh.calls")" "api" "no --pr: nothing is read"
}

# The MUTATION half: the guard is taken out of a copy of the script, and the run
# must NOT pass — a PASS with the guard removed would be this test's own failure,
# because it would mean the guard is reporting something it does not cause.
test_mutation_without_the_agent_guard_the_service_is_asked_from_a_session() {
  local dir=$work/mutagent
  prepare "$dir"
  mkdir -p "$dir/copy"
  sed 's/^  guard_agent_session$/  : guard_agent_session removed by the test/' "$script" >"$dir/copy/check542.sh"
  expect_contains "$(cat "$dir/copy/check542.sh")" "removed by the test" "mutation: the guard is really out of the copy"
  run_copy "$dir" "$dir/copy/check542.sh" CLAUDECODE=1
  expect_absent "$out" "FACT agent-session: PASS" "mutation: without the guard the row is not a pass"
  expect_contains "$(cat "$dir/logs/curl.calls")" "/v1/github/merge-token" "mutation: with the guard gone a session's own run asks the service for a merge token"
  expect_contains "$(cat "$dir/logs/curl.calls")" $'daemon\tnone' "mutation: and it asks as the daemon"
  expect_contains "$err" "asking the service for a merge token" "mutation: the run really got as far as the mint"
}

test_mutation_without_the_belong_guard_the_project_is_read() {
  local dir=$work/mutbelong
  prepare "$dir"
  mkdir -p "$dir/copy"
  sed 's/^  guard_belong$/  : guard_belong removed by the test/' "$script" >"$dir/copy/check542.sh"
  expect_contains "$(cat "$dir/copy/check542.sh")" "removed by the test" "mutation: the guard is really out of the copy"
  FAKE_MODE=belong-account run_copy "$dir" "$dir/copy/check542.sh"
  expect_absent "$out" "FACT belong: PASS" "mutation: without the guard the row is not a pass"
  expect_contains "$(cat "$dir/logs/gh.calls")" "api /user/installations" "mutation: with the guard gone the repository's App installation is read anyway"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "run services describe" "mutation: and so is the installation's service"
}

test_mutation_without_the_scrub_the_token_is_kept() {
  # A run that ends with the scratch kept must not leave a credential in it: the
  # scrub is the only step that removes one, and its absence is visible where it
  # matters — on the disk.
  local dir=$work/mutscrub
  prepare "$dir"
  mkdir -p "$dir/copy"
  sed 's/^  scrub_credentials$/  : scrub_credentials removed by the test/' "$script" >"$dir/copy/check542.sh"
  expect_contains "$(cat "$dir/copy/check542.sh")" "removed by the test" "mutation: the scrub is really out of the copy"
  run_copy "$dir" "$dir/copy/check542.sh" CHECK542_KEEP_SCRATCH=1
  expect_contains "$out" "FACT mint: PASS" "mutation: the mint still ran"
  if grep -rq "$mint_token" "$scratch" 2>/dev/null; then
    : # the finding: with the scrub gone the token is on the owner's disk
  else
    fail "mutation: the token is gone even with the scrub removed, so the scrub is not what removes it"
  fi
  rm -rf "$scratch" 2>/dev/null || true
}

# Sharding, so the Go test can run these side by side: CHECK_TEST_SHARD="i/n" runs
# every n-th test starting at i (each case works in its own directory under
# $work), CHECK_TEST_ONLY=name runs that one alone.
test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_help
t test_dry_run
t test_happy_path
t test_a_permission_the_app_does_not_hold_is_a_finding
t test_the_app_not_installed_on_the_repository_is_a_finding
t test_a_full_installations_page_is_a_finding
t test_the_app_on_a_ruleset_bypass_list_is_a_finding
t test_a_ruleset_with_no_bypass_list_is_a_finding
t test_a_full_ruleset_page_is_a_finding
t test_a_ruleset_that_cannot_be_read_again_is_unmeasured
t test_the_app_on_the_classic_protections_bypass_list_is_a_finding
t test_a_classic_protection_that_answers_another_status_is_a_finding
t test_a_protection_read_that_answers_no_status_is_unmeasured
t test_a_closed_pull_request_is_a_finding
t test_the_daemon_identity_missing_is_a_finding
t test_a_class_named_but_no_invoker_grant_is_a_finding
t test_a_service_that_is_not_deployed_is_a_finding
t test_a_daemon_token_that_cannot_be_minted_is_a_finding
t test_a_moved_head_that_is_minted_anyway_is_the_finding
t test_a_run_identity_that_is_minted_is_the_finding
t test_a_person_token_that_is_minted_is_the_finding
t test_a_state_refusal_of_an_admitted_caller_is_reported
t test_a_call_that_never_reached_the_route_is_reported
t test_an_audit_line_before_verification_is_a_finding
t test_an_admitted_call_with_no_audit_line_is_a_finding
t test_an_older_line_with_the_same_status_is_not_this_runs
t test_xtrace_refused
t test_an_agent_session_is_refused
t test_an_account_naming_belong_is_refused
t test_a_checkout_belonging_to_belong_is_refused
t test_kept_scratch_is_reported_and_the_token_is_still_scrubbed
t test_an_unknown_argument_is_refused
t test_a_missing_pr_is_die_not_a_prompt
t test_mutation_without_the_agent_guard_the_service_is_asked_from_a_session
t test_mutation_without_the_belong_guard_the_project_is_read
t test_mutation_without_the_scrub_the_token_is_kept

if [ "$failures" -ne 0 ]; then
  echo "check542_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check542_test.sh: ok"
