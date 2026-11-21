#!/usr/bin/env bash
# Tests for scripts/live-checks/check-840.sh, against a fake gcloud and a fake
# curl on PATH: nothing here reaches the network, there is no cloud project and
# no credential, and no step of the live check is run.
#
# The fake gcloud answers the job reads, the role read, the job's creation and
# deletion and the log read from files this test writes; the fake curl answers
# the four API calls the check makes -- `jobs.run`, the operation it polls, the
# execution it reads back and the signJwt it mints with -- and the two
# exchanges of the custom token. Every call is recorded, with the URL, the
# headers on the command line, the Authorization header and the URL of the
# 0600 `--config` files the check hands over, and the body file the exchange
# carried. That record is what lets a case say the minted token went into a
# 0600 file rather than onto a command line, and the sentinels are what lets
# the other cases say no credential reaches the run's output at all.
#
# Usage: bash scripts/live-checks/check-840_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-840.sh
# The PHYSICAL path, so a path this test compares and a path a child reports are
# spelled the same way.
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK840_TEST_KEEP:-}" ]; then
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
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT (aborted|project|region|job|probe|belong|xtrace|token|u1|u2|u3|verdict):' | sed 's/^/ ... last run: /')"
}

# The names every case reads: an installation's own job, the region it is in,
# the project, and the signer the check derives from the project unless it is
# told otherwise.
PROJECT=dev-installation-1234
REGION=europe-west1
JOB=fugaro-dev-job
# The one Cloud Run job the check creates, and never adopts. Its name is a
# constant of the script, so `probe_exists` below is what a case uses to put one
# in the way.
PROBE=fugaro-chk840-probe
MARKER_ENV=FUGARO_CHK840_MARKER
SIGNER="fugaro-token-signer@${PROJECT}.iam.gserviceaccount.com"
SA="fugaro-job@${PROJECT}.iam.gserviceaccount.com"
EXEC_FULL="projects/${PROJECT}/locations/${REGION}/jobs/${PROBE}/executions/${PROBE}-abcde"
EXEC_SHORT="${PROBE}-abcde"
OP="projects/${PROJECT}/locations/${REGION}/operations/op-840"

# Three sentinels, and no credential is handled anywhere in this test: the
# access token the fake gcloud mints, the Firebase web API key the fake job
# carries, and the custom token the fake signJwt signs. The cases below assert
# each never reaches the run's output — that is the whole point of their being
# here.
TOKEN_SENTINEL='SENTINEL-840-access-token-not-a-credential'
APIKEY_SENTINEL='SENTINEL-840-web-api-key-not-a-credential'
CUSTOM_SENTINEL='SENTINEL-840-custom-token-not-a-credential'

# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(project region job probe belong xtrace token job-account probe-run
  probe-exec claims u1 launcher-perm u2 mint u3-first u3-second u3 verdict
  restore scratch-removed)

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake gcloud for check-840.sh's test. Every call is recorded; the answers
# come from files under $FAKE840_DIR, and a `.code` file beside one makes the
# call fail with it. Nothing here touches a cloud.
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"$FAKE840_DIR/calls"
answer() { # answer BODY_FILE CODE_FILE [STDERR_FILE]
  local body=$1 code=$2 err=${3:-}
  if [ -n "$err" ] && [ -s "$err" ]; then cat "$err" >&2; fi
  if [ -s "$code" ]; then exit "$(cat "$code")"; fi
  if [ -n "$body" ] && [ -f "$body" ]; then cat "$body"; fi
  exit 0
}
case "${1:-} ${2:-}" in
"auth print-access-token")
  if [ -s "$FAKE840_DIR/token.status" ]; then
    printf 'fake gcloud: no credential is available\n' >&2
    exit "$(cat "$FAKE840_DIR/token.status")"
  fi
  cat "$FAKE840_DIR/token.txt"
  ;;
"run jobs")
  case "${3:-}" in
  describe)
    # $4 is the job name: the installation's own job answers from job.json, and
    # the check's own throwaway job is absent unless a case puts one there.
    if [ "${4:-}" = "$FAKE840_PROBE" ]; then
      if [ -f "$FAKE840_DIR/probe-exists" ]; then
        cat "$FAKE840_DIR/probe.json"
        exit 0
      fi
      printf 'ERROR: (gcloud.run.jobs.describe) NOT_FOUND: job %s not found\n' "${4:-}" >&2
      exit 1
    fi
    answer "$FAKE840_DIR/job.json" "$FAKE840_DIR/describe.code" "$FAKE840_DIR/describe.err"
    ;;
  create) answer "" "$FAKE840_DIR/create.code" "$FAKE840_DIR/create.err" ;;
  delete) answer "" "$FAKE840_DIR/delete.code" "$FAKE840_DIR/delete.err" ;;
  *) printf 'fake gcloud: unexpected run jobs verb %s\n' "${3:-}" >&2; exit 64 ;;
  esac
  ;;
"iam roles")
  answer "$FAKE840_DIR/roles.json" "$FAKE840_DIR/roles.code" "$FAKE840_DIR/roles.err"
  ;;
"logging read")
  answer "$FAKE840_DIR/log.txt" "$FAKE840_DIR/log.code" "$FAKE840_DIR/log.err"
  ;;
*)
  printf 'fake gcloud: unexpected %s\n' "$*" >&2
  exit 64
  ;;
esac
GCLOUD_EOF

  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A fake curl for check-840.sh's test. The call is recorded, the `--config`
# file's Authorization header and `url` line are recorded separately (that is
# how a case says which file carried what), and the answer is picked by the URL:
# `:run` is the jobs.run call, `/operations/` the operation poll, `/executions/`
# the read-back, `signJwt` the mint and `signInWithCustomToken` the exchange,
# whose two calls are answered in order. Nothing here touches the network.
set -euo pipefail
printf 'curl %s\n' "$*" >>"$FAKE840_DIR/calls"
conf=""
out=""
url=""
data=""
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
  -d)
    data=${2#@}
    shift 2
    ;;
  -w)
    shift 2
    ;;
  --max-time)
    shift 2
    ;;
  -H)
    printf 'header %s\n' "${2:-}" >>"$FAKE840_DIR/calls"
    shift 2
    ;;
  -X) shift 2 ;;
  -*) shift ;;
  *)
    url=$1
    shift
    ;;
  esac
done
printf 'url %s\n' "$url" >>"$FAKE840_DIR/calls"
if [ -n "$conf" ]; then
  # Appended rather than written: the last config file a run passes is the U3
  # exchange's, which carries no Authorization header, and a truncating write
  # would throw away the token the earlier calls did travel by file.
  sed -n 's/^header = "Authorization: Bearer \(.*\)"$/\1/p' "$conf" >>"$FAKE840_DIR/auth" 2>/dev/null || true
  # The URL only ever set by a config file that has one, so the exchange's own
  # file is the one this ends up recording.
  if url_line=$(sed -n 's/^url = "\(.*\)"$/\1/p' "$conf" 2>/dev/null) && [ -n "$url_line" ]; then
    printf '%s\n' "$url_line" >"$FAKE840_DIR/conf.url"
  fi
  # The file's mode, so a case can say it is 0600 and not merely present. Read
  # with python3 rather than stat, whose -c is GNU's: the owner runs this check
  # on their own machine, and this test on both.
  python3 -c 'import os, sys; print("%o" % (os.stat(sys.argv[1]).st_mode & 0o777))' "$conf" \
    >"$FAKE840_DIR/conf.mode" 2>/dev/null || printf 'unknown\n' >"$FAKE840_DIR/conf.mode"
fi

if [ -s "$FAKE840_DIR/curl.status" ]; then
  printf 'fake curl: the request did not answer\n' >&2
  exit "$(cat "$FAKE840_DIR/curl.status")"
fi

body=""
code=""
case "$url" in
*":run")
  body=$FAKE840_DIR/run.json
  code=$(cat "$FAKE840_DIR/run.code" 2>/dev/null || true)
  [ -n "$code" ] || code=200
  ;;
*"/operations/"*)
  body=$FAKE840_DIR/operation.json
  code=$(cat "$FAKE840_DIR/operation.code" 2>/dev/null || true)
  [ -n "$code" ] || code=200
  ;;
*"/executions/"*)
  body=$FAKE840_DIR/execution.json
  code=$(cat "$FAKE840_DIR/execution.code" 2>/dev/null || true)
  [ -n "$code" ] || code=200
  ;;
*":signJwt")
  body=$FAKE840_DIR/signjwt.json
  code=$(cat "$FAKE840_DIR/signjwt.code" 2>/dev/null || true)
  [ -n "$code" ] || code=200
  ;;
*"signInWithCustomToken"*)
  n=$(( $(cat "$FAKE840_DIR/signin.count" 2>/dev/null || printf '0') + 1 ))
  printf '%s\n' "$n" >"$FAKE840_DIR/signin.count"
  # The body file the exchange carried: a case asserts the custom token
  # travelled in it, and never on a command line.
  if [ -n "$data" ] && [ -f "$data" ]; then cp "$data" "$FAKE840_DIR/signin.$n.body"; fi
  body=$FAKE840_DIR/signin.$n.json
  code=$(cat "$FAKE840_DIR/signin.$n.code" 2>/dev/null || true)
  [ -n "$code" ] || code=200
  ;;
*)
  printf 'fake curl: unexpected URL %s\n' "$url" >&2
  exit 64
  ;;
esac
if [ -n "$out" ] && [ -f "$body" ]; then cp "$body" "$out"; fi
printf '%s' "$code"
CURL_EOF

  chmod +x "$1/bin/gcloud" "$1/bin/curl"
}

# ---------------------------------------------------------------- the fixtures
# write_job [--no-key] [--no-sa]: the installation's own job, as `run jobs
# describe` answers it. It carries the Firebase web API key and the runtime
# database URL in its env, exactly as internal/infra writes them, and the
# service account the check reads off it and gives the job it creates.
write_job() {
  local key=1 sa=1
  for arg in "$@"; do
    case "$arg" in
    --no-key) key=0 ;;
    --no-sa) sa=0 ;;
    esac
  done
  python3 - "$dir/state/job.json" "$key" "$sa" "$APIKEY_SENTINEL" "$SA" <<'PY'
import json
import sys

out, key, sa, api_key, account = sys.argv[1:6]
env = [{"name": "FUGARO_RTDB_URL", "value": "https://dev-installation.firebaseio.com"}]
if key == "1":
    env.append({"name": "FUGARO_FIREBASE_API_KEY", "value": api_key})
template = {"containers": [{"image": "gcr.io/example/fugaro:dev", "env": env}]}
if sa == "1":
    template["serviceAccount"] = account
json.dump({"template": {"template": template}}, open(out, "w", encoding="utf-8"))
PY
}

# write_execution MARKER: what `run.executions.get` answers with. MARKER is
# `yes` (the override's env is there, with the value the check set) or `no` (the
# execution carries the job's env and not the override).
write_execution() {
  python3 - "$dir/state/execution.json" "$1" "$MARKER_ENV" "$MARKER_VALUE" "$SA" "$EXEC_FULL" <<'PY'
import json
import sys

out, marker, name, value, account, full = sys.argv[1:7]
env = [{"name": "FUGARO_RTDB_URL", "value": "https://dev-installation.firebaseio.com"}]
if marker == "yes":
    env.append({"name": name, "value": value})
doc = {
    "name": full,
    "completionTime": "2026-11-21T00:00:01Z",
    "template": {"containers": [{"image": "gcr.io/google.com/cloudsdktool/cloud-sdk:slim", "env": env}],
                 "serviceAccount": account},
}
json.dump(doc, open(out, "w", encoding="utf-8"))
PY
}

# write_log CLAIMS_VARIANT WHAT_NAMES_THE_EXECUTION: the probe's own lines, as
# `logging read` answers them. CLAIMS_VARIANT is `plain` (the identity token's
# claims, with nothing of the execution in them) or `names-execution` (a claim
# whose value carries the execution's name); WHAT_NAMES_THE_EXECUTION is inserted
# as that value.
write_log() {
  python3 - "$dir/state/log.txt" "$1" "$2" "$EXEC_SHORT" "$EXEC_FULL" "$MARKER_VALUE" <<'PY'
import sys

out, variant, subject, short, full, marker = sys.argv[1:7]
lines = [
    "CHK840 exec=%s" % short,
    "CHK840 marker=%s" % marker,
    "CHK840 token_len=1234",
    "CHK840 claim=aud=https://chk840-check.invalid/",
    "CHK840 claim=azp=1234567890",
    "CHK840 claim=email=fugaro-job@dev-installation-1234.iam.gserviceaccount.com",
    "CHK840 claim=email_verified=true",
    "CHK840 claim=exp=1795000000",
    "CHK840 claim=iat=1794996400",
    "CHK840 claim=iss=https://accounts.google.com",
    "CHK840 claim=sub=104729384756102938475",
]
if variant == "names-execution":
    lines.append("CHK840 claim=run.googleapis.com/execution=%s" % (subject or full))
open(out, "w", encoding="utf-8").write("\n".join(lines) + "\n")
PY
}

# write_roles CARRIES: the launcher role, as `iam roles describe` answers it.
# CARRIES is `yes` (run.executions.get is on it) or `no` (the role is there and
# the permission is not).
write_roles() {
  python3 - "$dir/state/roles.json" "$1" <<'PY'
import json
import sys

out, carries = sys.argv[1:3]
perms = ["run.jobs.get", "run.jobs.run"]
if carries == "yes":
    perms.insert(0, "run.executions.get")
json.dump({"name": "projects/dev-installation-1234/roles/fugaroLauncher",
           "title": "Fugaro launcher", "includedPermissions": perms},
          open(out, "w", encoding="utf-8"))
PY
}

MARKER_VALUE="marker-1234abcd"

# setup_case NAME — a fresh case: the fakes, an installation job that carries
# the web key and the job account, a launcher role that carries
# run.executions.get, the run and its operation, an execution whose env carries
# the override, the probe's log lines with nothing of the execution in the
# claims, and two accepted exchanges of one custom token.
setup_case() {
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp"
  write_fakes "$dir"
  : >"$dir/state/calls"
  printf '%s\n' "$TOKEN_SENTINEL" >"$dir/state/token.txt"
  : >"$dir/state/token.status"
  : >"$dir/state/curl.status"
  : >"$dir/state/describe.err"
  : >"$dir/state/describe.code"
  : >"$dir/state/create.err"
  : >"$dir/state/create.code"
  : >"$dir/state/delete.err"
  : >"$dir/state/delete.code"
  : >"$dir/state/roles.err"
  : >"$dir/state/roles.code"
  : >"$dir/state/log.err"
  : >"$dir/state/log.code"
  write_job
  write_roles yes
  write_execution yes
  write_log plain ""
  python3 - "$dir/state/run.json" "$OP" "$EXEC_FULL" <<'PY'
import json
import sys

out, op, full = sys.argv[1:4]
json.dump({"name": op, "done": True,
           "metadata": {"@type": "type.googleapis.com/google.cloud.run.v2.Job", "name": full}},
          open(out, "w", encoding="utf-8"))
PY
  python3 - "$dir/state/operation.json" <<'PY'
import json
import sys

json.dump({"name": "projects/dev-installation-1234/locations/europe-west1/operations/op-840",
           "done": True},
          open(sys.argv[1], "w", encoding="utf-8"))
PY
  python3 - "$dir/state/signjwt.json" "$CUSTOM_SENTINEL" <<'PY'
import json
import sys

json.dump({"keyId": "key-1", "signedJwt": sys.argv[2]}, open(sys.argv[1], "w", encoding="utf-8"))
PY
  for n in 1 2; do
    : >"$dir/state/signin.$n.code"
    python3 - "$dir/state/signin.$n.json" "$n" <<'PY'
import json
import sys

json.dump({"kind": "identitytoolkit#VerifyCustomTokenResponse",
           "idToken": "SENTINEL-840-id-token-%s-not-a-credential" % sys.argv[2],
           "refreshToken": "SENTINEL-840-refresh-token",
           "expiresIn": "3600",
           "isNewUser": False,
           "localId": "r~chk840~probe"},
          open(sys.argv[1], "w", encoding="utf-8"))
PY
  done
  printf '0\n' >"$dir/state/signin.count"
}

# run840 [ARGS...] — runs the check on the case at hand. RUN840_ARGS adds
# arguments, RUN840_ENV names environment variables (NAME=value). Sets $status,
# $out and $dir/err.
run840() {
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE840_DIR="$dir/state" FAKE840_PROBE="$PROBE" \
    CHECK840_POLL_TRIES=1 CHECK840_POLL_SECONDS=0 \
    CHECK840_LOG_TRIES=1 CHECK840_LOG_SECONDS=0 \
    CHECK840_MARKER_VALUE="$MARKER_VALUE" \
    PATH="$dir/bin:$PATH" \
    ${RUN840_ENV[@]+"${RUN840_ENV[@]}"} \
    bash ${RUN840_ARGS[@]+"${RUN840_ARGS[@]}"} "$script" "$@" 2>"$dir/err") || status=$?
  RUN840_ARGS=()
  RUN840_ENV=()
}

RUN840_ARGS=()
RUN840_ENV=()

expect_block_keys() { # what
  expect_contains "$out" "CHECK 840 RESULT" "$1: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$out" "FACT $key:" "$1: the $key row"
  done
  expect_contains "$out" "CHECK 840 RESULT END (exit $status)" "$1: the block's own ending"
}

scratch_of() { # dir — the scratch directory the run announced
  sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$1/err" | head -n 1
}

expect_scratch_gone() { # dir what
  local scratch
  scratch=$(scratch_of "$1")
  if [ -z "$scratch" ]; then
    fail "$2: the script did not say where its scratch directory is"
    return 0
  fi
  [ -d "$scratch" ] && fail "$2: the scratch directory $scratch survived the run"
  [ -z "$(find "$1/tmp" -mindepth 1 2>/dev/null)" ] || fail "$2: files were left in TMPDIR: $(find "$1/tmp" -mindepth 1)"
  return 0
}

# No credential reaches the log or a command line: the access token, the web
# API key and the custom token. Asserted on its own, so a case whose run stopped
# early can make it too.
expect_no_credential_leaks() { # what
  local name
  for name in TOKEN_SENTINEL APIKEY_SENTINEL CUSTOM_SENTINEL; do
    expect_absent "$out" "${!name}" "$1: $name in the run's stdout"
    expect_absent "$(cat "$dir/state/calls" 2>/dev/null || true)" "${!name}" "$1: $name on a command line"
  done
  expect_absent "$(cat "$dir/err" 2>/dev/null || true)" "$TOKEN_SENTINEL" "$1: the minted access token in the run's stderr"
  expect_absent "$(cat "$dir/err" 2>/dev/null || true)" "$APIKEY_SENTINEL" "$1: the web API key in the run's stderr"
  expect_absent "$(cat "$dir/err" 2>/dev/null || true)" "$CUSTOM_SENTINEL" "$1: the custom token in the run's stderr"
}

# And they travelled the way the check says they do: the access token through
# the 0600 file curl reads with --config, and the web key inside the URL that
# same file carries (never on a command line).
expect_credentials_travelled_by_file() { # what
  expect_contains "$(cat "$dir/state/auth" 2>/dev/null || true)" "$TOKEN_SENTINEL" "$1: the access token the config file carried"
  expect_contains "$(cat "$dir/state/conf.mode" 2>/dev/null || true)" "600" "$1: the config file's mode"
}

# The probe the check hands the job it creates, decoded out of the base64 in
# `--args`: a case says the job really reads the execution's own metadata
# identity endpoint, and really carries the marker name.
probe_source() { # dir
  python3 - "$dir/state/calls" <<'PY'
import base64
import re
import sys

text = open(sys.argv[1]).read()
m = None
for line in text.splitlines():
    if line.startswith("gcloud ") and "run jobs create" in line:
        m = line
if m is None:
    raise SystemExit(0)
for token in m.split():
    if re.fullmatch(r"[A-Za-z0-9+/=]{40,}", token):
        try:
            print(base64.b64decode(token).decode("utf-8", "replace"))
            break
        except Exception:
            pass
PY
}

# ------------------------------------------------------------------- the cases
test_the_three_readings_hold() {
  setup_case all-pass
  run840 --project "$PROJECT" --region "$REGION" --job "$JOB"
  expect_status "$status" 0 "the installation's answers"
  expect_block_keys "the installation's answers"
  for key in project region job probe belong xtrace token job-account probe-run probe-exec claims u1 launcher-perm u2 mint u3-first u3-second u3 verdict restore scratch-removed; do
    expect_contains "$out" "FACT $key: PASS" "the installation's answers: the $key row"
  done
  expect_contains "$out" "FACT claims: PASS — the identity token carries" "the claims row prints what the token carries"
  expect_contains "$out" "aud=https://chk840-check.invalid/" "the claims row names the audience the probe minted for"
  expect_contains "$out" "email=$SA" "the claims row names the account the execution ran as"
  expect_contains "$(cat "$dir/state/calls")" "url https://run.googleapis.com/v2/projects/$PROJECT/locations/$REGION/jobs/$PROBE:run" "the override went through the API's jobs.run, which is the one call gcloud has no flag for"
  expect_contains "$(cat "$dir/state/calls")" "url https://run.googleapis.com/v2/$EXEC_FULL" "the read-back is the API's run.executions.get"
  expect_contains "$(cat "$dir/state/calls")" "gcloud run jobs create $PROBE" "the probe job is the check's own, created by name"
  expect_contains "$(cat "$dir/state/calls")" "--service-account $SA" "the probe runs as the installation job's own account, whose name the identity token then carries"
  expect_contains "$(probe_source "$dir")" "metadata.google.internal" "the probe reads the execution's own metadata identity endpoint"
  expect_contains "$(probe_source "$dir")" "$MARKER_ENV" "the probe prints the marker the override sets"
  expect_contains "$(cat "$dir/state/signin.1.body" 2>/dev/null || true)" "$CUSTOM_SENTINEL" "the custom token travelled in the body FILE of the first exchange, never on a command line"
  expect_contains "$(cat "$dir/state/signin.2.body" 2>/dev/null || true)" "$CUSTOM_SENTINEL" "the same token was exchanged a second time"
  expect_contains "$(cat "$dir/state/conf.url" 2>/dev/null || true)" "$APIKEY_SENTINEL" "the web API key travelled inside the config file's URL, never on a command line"
  expect_contains "$out" "FACT u3-uid: PASS — r~chk840~probe" "the two exchanges answered under one uid"
  expect_no_credential_leaks "the installation's answers"
  expect_credentials_travelled_by_file "the installation's answers"
  expect_contains "$out" "FACT restore: PASS" "everything the check created is gone"
  expect_scratch_gone "$dir" "the installation's answers"
}

# U1: a claim that names the execution is the finding the check exists to
# produce (R15 and R-C1-execution), and it must read as a FAIL rather than as a
# pass on the installation.
test_a_claim_naming_the_execution_fails_u1() {
  setup_case u1-fail
  write_log names-execution "$EXEC_FULL"
  run840 --project "$PROJECT" --region "$REGION" --job "$JOB"
  expect_status "$status" 1 "an identity token carrying the execution"
  expect_contains "$out" "FACT u1: FAIL" "an identity token carrying the execution"
  expect_contains "$out" "run.googleapis.com/execution" "the row names the claim that carries it"
  expect_contains "$out" "FACT verdict: FAIL" "the verdict reads the three rows"
  # And the check still removed what it created: a FAIL is not a reason to leave
  # a job behind.
  expect_contains "$out" "FACT restore: PASS" "an installation whose U1 does not hold is still left clean"
}

test_overrides_that_do_not_come_back_fail_u2() {
  setup_case u2-fail
  write_execution no
  run840 --project "$PROJECT" --region "$REGION" --job "$JOB"
  expect_status "$status" 1 "an execution whose override does not come back"
  expect_contains "$out" "FACT u2: FAIL" "an execution whose override does not come back"
  expect_contains "$out" "FACT u1: PASS" "the other readings are still taken"
  expect_contains "$out" "$MARKER_ENV" "the row names the variable the launcher set"
}

test_a_launcher_role_without_the_permission_fails_it() {
  setup_case launcher-perm
  write_roles no
  run840 --project "$PROJECT" --region "$REGION" --job "$JOB"
  expect_status "$status" 1 "a launcher role without run.executions.get"
  expect_contains "$out" "FACT launcher-perm: FAIL" "a launcher role without run.executions.get"
  expect_contains "$out" "run.executions.get" "the row names the permission it looked for"
  # The read that would be refused for it is still taken, and still says what
  # the API answered.
  expect_contains "$out" "FACT u2: PASS" "the read the launcher could not make is still made with the owner's own credential"
}

test_a_second_exchange_that_is_refused_fails_u3() {
  setup_case u3-second-refused
  printf '400' >"$dir/state/signin.2.code"
  python3 - "$dir/state/signin.2.json" <<'PY'
import json
import sys

json.dump({"error": {"code": 400, "message": "TOKEN_EXPIRED"}}, open(sys.argv[1], "w", encoding="utf-8"))
PY
  run840 --project "$PROJECT" --region "$REGION" --job "$JOB"
  expect_status "$status" 1 "a custom token Firebase exchanges once"
  expect_contains "$out" "FACT u3-first: PASS" "the first exchange was accepted"
  expect_contains "$out" "FACT u3-second: FAIL" "a token Firebase exchanges once"
  expect_contains "$out" "FACT u3: FAIL" "a token Firebase exchanges once"
  expect_contains "$out" "race" "the row says what the answer means for F9"
}

test_a_first_exchange_that_is_refused_stops() {
  setup_case u3-first-refused
  printf '400' >"$dir/state/signin.1.code"
  run840 --project "$PROJECT" --region "$REGION" --job "$JOB"
  expect_status "$status" 2 "a custom token Firebase refused outright"
  expect_contains "$out" "FACT u3-first: FAIL" "a custom token Firebase refused outright"
  expect_contains "$out" "FACT aborted: FAIL" "the run ends as an abort rather than as an answer about the second exchange"
  expect_contains "$out" "FACT u3: FAIL — not reached" "nothing is concluded about the second exchange"
  expect_contains "$out" "FACT restore: PASS" "an aborted run still removes the job it created"
}

test_a_job_without_the_web_key_stops() {
  setup_case no-key
  write_job --no-key
  run840 --project "$PROJECT" --region "$REGION" --job "$JOB"
  expect_status "$status" 2 "an installation job that carries no web API key"
  expect_contains "$out" "FACT job-account: PASS" "the account was still read off the job"
  expect_contains "$out" "FUGARO_FIREBASE_API_KEY" "the row names the variable it looked for"
  expect_absent "$(cat "$dir/state/calls")" "jobs/$PROBE:run" "nothing was launched, so nothing had to be cleaned up"
  expect_contains "$out" "FACT probe: PASS" "no job was created"
}

test_a_launch_that_is_refused_stops_and_removes_the_job() {
  setup_case run-refused
  printf '403' >"$dir/state/run.code"
  python3 - "$dir/state/run.json" <<'PY'
import json
import sys

json.dump({"error": {"code": 403, "status": "PERMISSION_DENIED",
                     "message": "The caller does not have permission"}},
          open(sys.argv[1], "w", encoding="utf-8"))
PY
  run840 --project "$PROJECT" --region "$REGION" --job "$JOB"
  expect_status "$status" 2 "a launch the API refused"
  expect_contains "$out" "FACT probe-run: FAIL" "a launch the API refused"
  expect_contains "$out" "PERMISSION_DENIED" "the row carries the API's own code"
  expect_contains "$out" "FACT restore: PASS" "the job this run created is deleted again"
}

test_a_log_read_that_never_carries_the_execution_stops() {
  setup_case no-log
  : >"$dir/state/log.txt"
  run840 --project "$PROJECT" --region "$REGION" --job "$JOB"
  expect_status "$status" 2 "a log read that never answered with this execution's lines"
  expect_contains "$out" "FACT claims: FAIL" "a log read that never answered with this execution's lines"
  expect_contains "$out" "FACT u1: FAIL — not reached" "nothing is concluded about the identity token"
  expect_contains "$out" "FACT restore: PASS" "an unread log still leaves nothing behind"
}

test_a_job_that_already_exists_is_refused() {
  setup_case adopted
  printf '1\n' >"$dir/state/probe-exists"
  printf '{}\n' >"$dir/state/probe.json"
  run840 --project "$PROJECT" --region "$REGION" --job "$JOB"
  expect_status "$status" 2 "a job of the check's own name already there"
  expect_contains "$out" "FACT probe: FAIL" "a job of the check's own name already there"
  expect_absent "$(cat "$dir/state/calls")" "run jobs create" "the check never adopts a job it did not create"
  expect_absent "$(cat "$dir/state/calls")" "run jobs delete" "and it deletes nothing it did not create"
}

test_a_missing_token_stops() {
  setup_case no-token
  printf '1' >"$dir/state/token.status"
  run840 --project "$PROJECT" --region "$REGION" --job "$JOB"
  expect_status "$status" 2 "a run that could not mint an access token"
  expect_contains "$out" "FACT token: FAIL" "a run that could not mint an access token"
  expect_contains "$out" "gcloud auth login" "the row says what to do"
  expect_absent "$(cat "$dir/state/calls")" "run jobs create" "nothing was created"
  expect_scratch_gone "$dir" "a run that could not mint a token"
}

test_belong_is_refused() {
  setup_case belong
  run840 --project belong-installation --region "$REGION" --job "$JOB"
  expect_status "$status" 2 "a project naming the owner's installation"
  expect_contains "$out" "FACT belong: FAIL" "a project naming the owner's installation"
  expect_absent "$(cat "$dir/state/calls")" "gcloud" "nothing was called at all"

  setup_case belong-job
  run840 --project "$PROJECT" --region "$REGION" --job belong-sandbox-job
  expect_status "$status" 2 "a job naming the owner's installation"
  expect_contains "$out" "FACT belong: FAIL" "a job naming the owner's installation"
  expect_contains "$out" "belong-sandbox-job" "the row says which value it refused"
}

test_an_id_that_is_not_a_project_or_a_region_is_refused() {
  setup_case bad-args
  run840 --project 'Bad_Id!' --region "$REGION" --job "$JOB"
  expect_status "$status" 2 "an id that is not a project id"
  expect_contains "$out" "FACT aborted: FAIL" "an id that is not a project id"

  run840 --project "$PROJECT" --region 'Europe West 1' --job "$JOB"
  expect_status "$status" 2 "a region that is not a region"
  expect_contains "$out" "FACT aborted: FAIL" "a region that is not a region"

  run840 --project "$PROJECT" --region "$REGION" --job "$PROBE"
  expect_status "$status" 2 "a job name equal to the check's own throwaway name"
  expect_contains "$out" "FACT aborted: FAIL" "a job name equal to the check's own throwaway name"
  expect_absent "$(cat "$dir/state/calls")" "gcloud" "a refused argument calls nothing"
}

test_a_dry_run_makes_no_call() {
  setup_case dry-run
  run840 --project "$PROJECT" --region "$REGION" --job "$JOB" --dry-run
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "FACT dry-run: PASS" "a dry run"
  expect_contains "$out" "FACT job: PASS — $JOB" "a dry run still reads its arguments"
  expect_contains "$out" "FACT belong: PASS" "a dry run still runs the guards"
  expect_contains "$(cat "$dir/err")" "jobs/$PROBE:run" "the plan names the call a real run would make"
  expect_absent "$(cat "$dir/state/calls")" "gcloud" "a dry run calls nothing"
  expect_absent "$(cat "$dir/state/calls")" "curl" "a dry run calls nothing"
}

test_xtrace_is_refused() {
  setup_case xtrace
  RUN840_ARGS=(-x)
  run840 --project "$PROJECT" --region "$REGION" --job "$JOB"
  expect_status "$status" 2 "a shell started with tracing on"
  expect_contains "$out" "FACT xtrace: FAIL" "a shell started with tracing on"
  expect_no_credential_leaks "a traced shell"
  expect_absent "$(cat "$dir/err" 2>/dev/null || true)" "print-access-token --quiet" "the trace is off before anything is read"
  expect_absent "$(cat "$dir/state/calls")" "gcloud" "a traced shell created nothing"
}

test_usage() {
  setup_case usage
  run840
  expect_status "$status" 2 "no arguments"
  expect_contains "$out" "--project is required" "the block says which argument is missing"

  run840 --project "$PROJECT" --region "$REGION"
  expect_status "$status" 2 "--job with no value"
  expect_contains "$out" "--job is required" "--job is required"

  run840 --project "$PROJECT" --region "$REGION" --job "$JOB" --bucket x
  expect_status "$status" 2 "an argument this check does not take"
  expect_contains "$out" "unknown argument '--bucket'" "an argument this check does not take"

  run840 --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "usage: check-840.sh --project" "--help prints the usage"
  expect_absent "$out" "CHECK 840 RESULT" "--help is not a run, so it prints no block"
  expect_absent "$(cat "$dir/state/calls")" "gcloud" "--help calls nothing"
}

# The block is what the owner pastes back, so the assertion that no credential
# reaches it has to be one that would CATCH one: a check whose own block carried
# a live token is the leak this whole part of the script exists to prevent. This
# case runs a copy of the check with one line added, and asserts the assertion
# fails on it — the same self-test Check 361 makes of its token.
test_the_credential_assertions_catch_an_echoing_script() {
  setup_case echoing
  local copy=$dir/check-840-echoing.sh
  # The copy's added line reads a file whose name is the one the check writes
  # its custom token to, and this case puts the sentinel there so that the copy
  # really does print a credential.
  printf '%s' "$CUSTOM_SENTINEL" >"$dir/state/custom.jwt"
  python3 - "$script" "$copy" <<'PY'
import sys

src, dst = sys.argv[1], sys.argv[2]
lines = open(src).read().splitlines(keepends=True)
out = []
hit = 0
for line in lines:
    if line.lstrip().startswith('fact token PASS "'):
        out.append('    fact token PASS "leaked: $(cat "$FAKE840_DIR/custom.jwt")"  # a self-test copy\n')
        hit += 1
    else:
        out.append(line)
if hit != 1:
    raise SystemExit("the line this case edits is not in the check exactly once (%d): it has moved" % hit)
open(dst, "w").write("".join(out))
PY
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE840_DIR="$dir/state" FAKE840_PROBE="$PROBE" \
    CHECK840_POLL_TRIES=1 CHECK840_POLL_SECONDS=0 \
    CHECK840_LOG_TRIES=1 CHECK840_LOG_SECONDS=0 \
    CHECK840_MARKER_VALUE="$MARKER_VALUE" \
    PATH="$dir/bin:$PATH" bash "$copy" \
    --project "$PROJECT" --region "$REGION" --job "$JOB" 2>"$dir/err") || status=$?
  expect_contains "$out" "$CUSTOM_SENTINEL" "the echoing copy prints a credential, which is what this self-test needs"
  local caught
  caught=$(
    local caught_failures=0
    fail() { caught_failures=$((caught_failures + 1)); }
    expect_no_credential_leaks "detector"
    printf '%s' "$caught_failures"
  )
  [ "${caught:-0}" -ge 1 ] \
    || fail "detector: an echoing script raised none of the assertions that hold the credentials out of the block, so they are vacuous"
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
  "$1"
}

t test_the_three_readings_hold
t test_a_claim_naming_the_execution_fails_u1
t test_overrides_that_do_not_come_back_fail_u2
t test_a_launcher_role_without_the_permission_fails_it
t test_a_second_exchange_that_is_refused_fails_u3
t test_a_first_exchange_that_is_refused_stops
t test_a_job_without_the_web_key_stops
t test_a_launch_that_is_refused_stops_and_removes_the_job
t test_a_log_read_that_never_carries_the_execution_stops
t test_a_job_that_already_exists_is_refused
t test_a_missing_token_stops
t test_belong_is_refused
t test_an_id_that_is_not_a_project_or_a_region_is_refused
t test_a_dry_run_makes_no_call
t test_xtrace_is_refused
t test_usage
t test_the_credential_assertions_catch_an_echoing_script

if [ "$failures" -ne 0 ]; then
  echo "check-840_test: $failures failure(s)" >&2
  exit 1
fi
echo "check-840_test: all tests passed" >&2
