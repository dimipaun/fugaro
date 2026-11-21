#!/usr/bin/env bash
# Tests for scripts/live-checks/check810.sh. NOTHING here is a cloud, a project,
# an installation or a run: every case builds FOUR REAL local git repositories in
# a temporary directory and drives the check against a fake `gcloud`, a fake
# `fugaro` and a fake `curl` on PATH whose answers the case owns — the database's
# two reads, the job's own document, the service's URL, the identity toolkit's two
# answers, the four launches, the runs listings, the records out of the runs
# bucket and the service's own log lines. No case can spend anything, because the
# only thing that spends is `fugaro run`, and here that is a script in $work.
#
# The readings are the point. Check 810 watches ONE migration across four
# repositories and a run in flight, and the cases that matter are the ones where a
# reading is bent: an installation already migrated (a premise that must STOP the
# check), an update that fails, a run the migration killed, a record that names
# the wrong credential, an update that says nothing, a hand-built call the service
# SERVES, a refusal with no audit line, a refusal that does not name the craft, a
# run that never ends, a basis that moved, a log read that does not answer.
#
# Usage: bash scripts/live-checks/check810_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check810.sh
work=$(mktemp -d)
# The installation's own local project config lives outside the case's own
# directory tree ($work/config unless a case says otherwise), because the check
# finds it through XDG_CONFIG_HOME and a case must never be able to read the
# config of the machine the test runs on.
confighome=$work/config
cleanup_all() {
  if [ -n "${CHECK810_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK810_TEST_HEAD:-1600}")" ;;
  esac
}
expect_not_contains() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: expected NOT to contain '$2'" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1"
}
expect_row() { # out key state what
  if printf '%s\n' "$1" | awk -v k="$2" -v s="$3" '$1 == k && $2 == s { found = 1 } END { exit !found }'; then
    return 0
  fi
  fail "$4: expected the row '$2' to read $3; got: $(printf '%s' "$1" | head -c "${CHECK810_TEST_HEAD:-1600}")"
}

# ---------------------------------------------------------------- the fakes
fakebin=$work/bin
mkdir -p "$fakebin"

cat >"$fakebin/gcloud" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"${FAKE810_LOG:?}"
dir=${FAKE810_DIR:?}
case "${1:-} ${2:-}" in
"auth print-access-token")
  printf '%s\n' 'fake810-access-token'
  ;;
"auth print-identity-token")
  printf '%s\n' 'fake810-job-token'
  ;;
"logging read")
  status=$(cat "$dir/log-status" 2>/dev/null || printf 0)
  if [ "$status" != 0 ]; then
    printf 'fake gcloud: the log read was refused\n' >&2
    exit "$status"
  fi
  # The filter this read was given, so a case can pin that the row read the lines
  # of the run it is about rather than every line of the service.
  printf '%s\n' "${3:-}" >>"$dir/log-filter"
  python3 - "$dir/log.json" "${3:-}" <<'PY'
import json, re, sys
try:
    doc = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
m = re.search(r'jsonPayload\.run="([^"]*)"', sys.argv[2])
want = m.group(1) if m else ""
rows = doc if isinstance(doc, list) else []
print(json.dumps([r for r in rows if (r.get("jsonPayload") or {}).get("run") == want]))
PY
  ;;
"run jobs")
  case "${3:-}" in
  describe) cat "$dir/job.json" ;;
  *)
    printf 'fake gcloud: unexpected run jobs verb %s\n' "${3:-}" >&2
    exit 64
    ;;
  esac
  ;;
"run services")
  case "${3:-}" in
  describe) cat "$dir/service-url.txt" ;;
  *)
    printf 'fake gcloud: unexpected run services verb %s\n' "${3:-}" >&2
    exit 64
    ;;
  esac
  ;;
"storage cat")
  status=$(cat "$dir/record-status" 2>/dev/null || printf 0)
  if [ "$status" != 0 ]; then
    printf 'fake gcloud: the record read was refused\n' >&2
    exit "$status"
  fi
  key=${3:-}
  ref=${key#gs://fugaro-runs-test-project/runs/}
  ref=${ref%/result.json}
  f="$dir/record_$(printf '%s' "$ref" | tr '/' '_').json"
  if [ ! -f "$f" ]; then
    printf 'fake gcloud: no record was written for %s\n' "$key" >&2
    exit 64
  fi
  cat "$f"
  ;;
*)
  printf 'fake gcloud: unexpected verb %s %s\n' "${1:-<none>}" "${2:-<none>}" >&2
  exit 64
  ;;
esac
FAKE
chmod +x "$fakebin/gcloud"

cat >"$fakebin/fugaro" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"${FAKE810_LOG:?}"
dir=${FAKE810_DIR:?}
name=$(basename "$PWD")
case "${1:-}" in
run)
  status=$(cat "$dir/launch-status-$name" 2>/dev/null || printf 0)
  if [ "$status" != 0 ]; then
    printf 'fake fugaro: the launch was refused\n' >&2
    exit "$status"
  fi
  # The FIRST launch in a checkout is the run in flight across the update and the
  # SECOND is the post-update run, so a case that wants them to differ writes
  # `launch-<name>.2.json` beside `launch-<name>.json`.
  n=$(cat "$dir/launch-calls-$name" 2>/dev/null || printf 0)
  printf '%s' "$((n + 1))" >"$dir/launch-calls-$name"
  f="$dir/launch-$name.json"
  if [ "$n" -gt 0 ] && [ -f "$dir/launch-$name.2.json" ]; then f="$dir/launch-$name.2.json"; fi
  cat "$f"
  ;;
runs)
  status=$(cat "$dir/runs-status-$name" 2>/dev/null || printf 0)
  if [ "$status" != 0 ]; then
    printf 'fake fugaro: the listing of runs was refused\n' >&2
    exit "$status"
  fi
  n=$(cat "$dir/poll-calls-$name" 2>/dev/null || printf 0)
  printf '%s' "$((n + 1))" >"$dir/poll-calls-$name"
  f="$dir/runs-$name.json"
  if [ "$n" -gt 0 ] && [ -f "$dir/runs-$name.2.json" ]; then f="$dir/runs-$name.2.json"; fi
  cat "$f"
  ;;
update)
  # The check runs the installation's own update and then one `--repo` per
  # checkout, and the two answer differently, so the fake tells them apart by the
  # flag rather than by position.
  repo=no
  for a in "$@"; do
    if [ "$a" = --repo ]; then repo=yes; fi
  done
  if [ "$repo" = yes ]; then
    status=$(cat "$dir/update-repo-status-$name" 2>/dev/null || printf 0)
    if [ "$status" != 0 ]; then
      printf 'fake fugaro: the repository update was refused\n' >&2
      exit "$status"
    fi
    cat "$dir/update-repo-$name.out" 2>/dev/null || true
  else
    status=$(cat "$dir/update-status" 2>/dev/null || printf 0)
    if [ "$status" != 0 ]; then
      printf 'fake fugaro: the update was refused\n' >&2
      exit "$status"
    fi
    cat "$dir/update.out"
  fi
  ;;
*)
  printf 'fake fugaro: unexpected verb %s\n' "${1:-<none>}" >&2
  exit 64
  ;;
esac
FAKE
chmod +x "$fakebin/fugaro"

cat >"$fakebin/curl" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
dir=${FAKE810_DIR:?}
log=${FAKE810_LOG:?}
out=""
data=""
code_out=""
url=""
conf=""
headers=()
while [ $# -gt 0 ]; do
  case "$1" in
  -o)
    out=$2
    shift 2
    ;;
  -d)
    data=$2
    shift 2
    ;;
  -w)
    code_out=$2
    shift 2
    ;;
  -H)
    headers+=("$2")
    shift 2
    ;;
  --config)
    conf=$2
    shift 2
    ;;
  -X | --max-time)
    shift 2
    ;;
  -sS | -s | -S)
    shift
    ;;
  *)
    url=$1
    shift
    ;;
  esac
done
# The check passes the identity toolkit's URL in the curl config file (it
# carries the job's own web key in its query), so the fake reads it from there
# when no URL was given as an argument.
if [ -z "$url" ] && [ -n "$conf" ]; then
  url=$(sed -n 's/^url = "\(.*\)"$/\1/p' "$conf" | head -1)
fi
printf 'curl %s\n' "$url" >>"$log"
for h in ${headers[@]+"${headers[@]}"}; do printf 'header %s\n' "$h" >>"$log"; done
# The curl config files carry the tokens this check mints: logging their contents
# is how a case pins that the call really presented the identity and the job's
# own token, and every token here is a literal a case wrote.
if [ -n "$conf" ]; then
  printf 'config\n' >>"$log"
  sed 's/^/  /' "$conf" >>"$log"
fi
emit() { # emit <bodyfile> <code>
  if [ -n "$out" ]; then cat "$1" >"$out"; fi
  if [ -n "$code_out" ]; then printf '%s' "$2"; fi
}
case "$url" in
*"/creds.json")
  n=$(cat "$dir/creds-calls" 2>/dev/null || printf 0)
  printf '%s' "$((n + 1))" >"$dir/creds-calls"
  status=$(cat "$dir/creds-status" 2>/dev/null || printf 0)
  if [ "$status" != 0 ]; then
    printf 'fake curl: the database read failed\n' >&2
    exit "$status"
  fi
  if [ "$n" -eq 0 ]; then emit "$dir/creds-before.json" 200; else emit "$dir/creds-after.json" 200; fi
  ;;
*":signJwt")
  # The claim set the check minted, held to what the case says the run's own
  # identity is: a check that signed the wrong slug or the wrong run is caught
  # here rather than by the row it would have left unverified.
  python3 - "$data" "$dir" <<'PY'
import json, os, sys
raw = sys.argv[1]
if raw.startswith("@"):
    raw = open(raw[1:]).read()
claims = json.loads(json.loads(raw)["payload"])
want = os.path.join(sys.argv[2], "want-identity.json")
if os.path.exists(want):
    inner = claims.get("claims") or {}
    for key, value in json.load(open(want)).items():
        have = claims[key] if key in claims else inner.get(key)
        if have != value:
            print("fake curl: the minted identity's %s is %r, not %r"
                  % (key, have, value), file=sys.stderr)
            raise SystemExit(64)
PY
  emit "$dir/signjwt.json" "$(cat "$dir/signjwt-code" 2>/dev/null || printf 200)"
  ;;
*"accounts:signInWithCustomToken"*)
  emit "$dir/signin.json" "$(cat "$dir/signin-code" 2>/dev/null || printf 200)"
  ;;
*"/v1/messages")
  # `call-status` is curl failing BEFORE any HTTP response (a call nobody saw an
  # answer to), which is not the same as the service answering with a code.
  status=$(cat "$dir/call-status" 2>/dev/null || printf 0)
  if [ "$status" != 0 ]; then
    printf 'fake curl: the call did not answer\n' >&2
    exit "$status"
  fi
  emit "$dir/call-answer.json" "$(cat "$dir/call-code" 2>/dev/null || printf 403)"
  ;;
*)
  printf 'fake curl: unexpected URL %s\n' "$url" >&2
  exit 64
  ;;
esac
FAKE
chmod +x "$fakebin/curl"

# --------------------------------------------------------------- the fixtures

OID_INFLIGHT=20261121-100001-aaaa
OID_OAUTH=20261121-110001-bbbb
OID_KEY=20261121-110002-cccc
OID_CASCADE=20261121-110003-dddd
OID_VERTEX=20261121-110004-eeee

slug_of() { # slug_of <owner/name>
  printf '%s-0011223344556677' "$(printf '%s' "$1" | tr 'A-Z' 'a-z' | tr -c 'a-z0-9' '-')"
}
SLUG_OAUTH=$(slug_of acme/oauth-app)
SLUG_KEY=$(slug_of acme/key-app)
SLUG_CASCADE=$(slug_of acme/cascade-app)
SLUG_VERTEX=$(slug_of acme/vertex-app)

make_sandbox() { # make_sandbox <dir> <name> <auth>
  mkdir -p "$1"
  git -C "$1" init -q
  git -C "$1" config user.email check810@example.invalid
  git -C "$1" config user.name check810
  printf 'agent:\n  auth: %s\n' "$3" >"$1/fugaro.yaml"
  printf 'sandbox\n' >"$1/README.md"
  git -C "$1" add -A
  git -C "$1" commit -qm sandbox
  git -C "$1" remote add origin "git@github.com:acme/$2.git"
}

# job_json writes the job's own document in the shape the checklist pins for
# `gcloud run jobs describe --format=json` (the v1 API: a job nests at
# spec.template.spec.template.spec). `nokey` writes one that carries no Firebase
# web key, which is a job the hand-built call cannot be addressed at.
job_json() { # job_json <out> [nokey]
  python3 - "$1" "${2:-}" <<'PY'
import json, sys
env = [{"name": "FUGARO_BUCKET", "value": "b"}]
if sys.argv[2] != "nokey":
    env.append({"name": "FUGARO_FIREBASE_API_KEY", "value": "fake-firebase-web-key"})
doc = {"spec": {"template": {"spec": {"template": {"spec": {
    "serviceAccountName": "job-oauth@test-project.iam.gserviceaccount.com",
    "containers": [{"image": "us-docker.pkg.dev/p/jobs/app:1.0.0", "env": env}],
}}}}}}
open(sys.argv[1], "w").write(json.dumps(doc))
PY
}

# creds_json writes the database's creds node: the four OLD records
# (creds/repoauth/<slug>, the closed shape internal/creds/repoauth.go pins) and,
# after the update, the four NEW ones (creds/repoaccess/<slug>). An argument
# `drop=<slug>` leaves a record out and `<slug>=<credential>` bends what a new
# record names, which is how the cases that fail a row are built.
creds_json() { # creds_json <out> <before|after> [drop=<slug>|<slug>=<credential> ...]
  local out=$1 phase=$2
  shift 2
  python3 - "$out" "$phase" "$@" <<'PY'
import json, sys
out, phase = sys.argv[1], sys.argv[2]
repos = [
    ("acme/oauth-app", "acme-oauth-app-0011223344556677", "oauth", ["default"], "default"),
    ("acme/key-app", "acme-key-app-0011223344556677", "api-key", [], "anthropic-api-key"),
    ("acme/cascade-app", "acme-cascade-app-0011223344556677", "cascade", [], "cascade-pool"),
    ("acme/vertex-app", "acme-vertex-app-0011223344556677", "vertex", [], "vertex"),
]
cred = {r[1]: r[4] for r in repos}
drop = set()
for raw in sys.argv[3:]:
    key, _, value = raw.partition("=")
    if key == "drop":
        drop.add(value)
    else:
        cred[key] = value
doc = {"repoauth": {}}
for _, slug, auth, accounts, _ in repos:
    if slug in drop:
        continue
    doc["repoauth"][slug] = {"version": 1, "auth": auth, "accounts": accounts}
if phase == "after":
    doc["repoaccess"] = {}
    for _, slug, _, _, _ in repos:
        if slug in drop:
            continue
        doc["repoaccess"][slug] = {"version": 1, "crafts": {"code": {"access": cred[slug]}}}
open(out, "w").write(json.dumps(doc))
PY
}

runs_json() { # runs_json <out> <ref> <outcome|-> ...
  local out=$1
  shift
  python3 - "$out" "$@" <<'PY'
import json, sys
rows = []
args = sys.argv[2:]
for i in range(0, len(args), 2):
    ref, outcome = args[i], args[i + 1]
    row = {"run": ref, "run_id": ref.rsplit("/", 1)[-1], "status": "running"}
    if outcome and outcome != "-":
        row["outcome"] = outcome
        row["status"] = "done"
    rows.append(row)
open(sys.argv[1], "w").write(json.dumps({"runs": rows}))
PY
}

record_json() { # record_json <out> <basis|-> [<member>=<value> ...]
  local out=$1
  shift
  python3 - "$out" "$@" <<'PY'
import json, sys
doc = {"run_id": "x", "status": "succeeded"}
if sys.argv[2] != "-":
    doc["cost"] = {"model_basis": sys.argv[2]}
for raw in sys.argv[3:]:
    key, _, value = raw.partition("=")
    doc.setdefault("cost", {})[key] = value
open(sys.argv[1], "w").write(json.dumps(doc))
PY
}

# log_json writes the service's own lines, one entry per `<run-id>|<msg>|<status>|
# <type>|<repository>` argument: `run` is the run's own ID and the repository is
# its own field, which is how the service writes them and what a row's filter on
# `jsonPayload.run` matches. The two messages are the service's: `credsvc: audit` is the
# line it writes for what it did with a credential — one per refusal on the new
# narrowing (C1.13) — and `credsvc: refused` is the diagnosis written beside it.
log_json() { # log_json <out> <run>|<msg>|<status>|<type> ...
  local out=$1
  shift
  python3 - "$out" "$@" <<'PY'
import json, sys
rows = []
for raw in sys.argv[2:]:
    parts = raw.split("|")
    run, msg, status = parts[0], parts[1], parts[2]
    payload = {"msg": msg, "status": status, "run": run, "credential": "default",
               "purpose": "forward", "repository": parts[4] if len(parts) > 4 else ""}
    if len(parts) > 3 and parts[3]:
        payload["type"] = parts[3]
    rows.append({"jsonPayload": payload})
open(sys.argv[1], "w").write(json.dumps(rows))
PY
}

# case_dir builds one case's own world: the four checkouts, the installation's
# own documents and every answer the fakes give, all in the state the happy path
# needs. A case then bends exactly one of them.
case_dir() { # case_dir <name>
  local root=$work/$1
  answers=$root/answers
  mkdir -p "$answers"
  CALLS="$answers/calls"
  : >"$CALLS"
  oauth=$root/oauth
  key=$root/key
  cascade=$root/cascade
  vertex=$root/vertex
  make_sandbox "$oauth" oauth-app oauth
  make_sandbox "$key" key-app api-key
  make_sandbox "$cascade" cascade-app cascade
  make_sandbox "$vertex" vertex-app vertex
  # The installation's own local project config, which is where the check reads
  # the database's URL from (`budget.rtdb_url`): `fugaro doctor --json` carries no
  # such member, so a case that invented one in a doctor document would be
  # testing a source the check does not have.
  confighome=$root/config
  mkdir -p "$confighome/fugaro/projects"
  printf 'project: test\nbudget:\n  rtdb_url: https://db.invalid\n' \
    >"$confighome/fugaro/projects/test.yaml"
  job_json "$answers/job.json"
  printf 'https://credsvc.invalid\n' >"$answers/service-url.txt"
  printf '{"signedJwt":"fake810-custom-token"}\n' >"$answers/signjwt.json"
  printf '{"idToken":"fake810-idtoken"}\n' >"$answers/signin.json"
  printf '{"error":{"type":"craft_not_in_record","message":"the craft is not in the record"}}\n' \
    >"$answers/call-answer.json"
  creds_json "$answers/creds-before.json" before
  creds_json "$answers/creds-after.json" after
  runs_json "$answers/runs-oauth.json" "$SLUG_OAUTH/$OID_INFLIGHT" succeeded "$SLUG_OAUTH/$OID_OAUTH" succeeded
  runs_json "$answers/runs-key.json" "$SLUG_KEY/$OID_KEY" succeeded
  runs_json "$answers/runs-cascade.json" "$SLUG_CASCADE/$OID_CASCADE" succeeded
  runs_json "$answers/runs-vertex.json" "$SLUG_VERTEX/$OID_VERTEX" succeeded
  launch_json "$answers/launch-oauth.json" "$SLUG_OAUTH" "$OID_INFLIGHT"
  launch_json "$answers/launch-oauth.2.json" "$SLUG_OAUTH" "$OID_OAUTH"
  launch_json "$answers/launch-key.json" "$SLUG_KEY" "$OID_KEY"
  launch_json "$answers/launch-cascade.json" "$SLUG_CASCADE" "$OID_CASCADE"
  launch_json "$answers/launch-vertex.json" "$SLUG_VERTEX" "$OID_VERTEX"
  record_json "$answers/record_${SLUG_OAUTH}_$OID_INFLIGHT.json" subscription
  record_json "$answers/record_${SLUG_OAUTH}_$OID_OAUTH.json" subscription
  record_json "$answers/record_${SLUG_KEY}_$OID_KEY.json" api-list
  record_json "$answers/record_${SLUG_CASCADE}_$OID_CASCADE.json" api-list
  record_json "$answers/record_${SLUG_VERTEX}_$OID_VERTEX.json" api-list
  {
    printf 'fugaro update: acme/oauth-app crafts: code -> default\n'
    printf 'fugaro update: acme/key-app crafts: code -> anthropic-api-key\n'
    printf 'fugaro update: acme/cascade-app crafts: code -> cascade-pool\n'
    printf 'fugaro update: acme/vertex-app crafts: code -> vertex\n'
  } >"$answers/update.out"
  local n
  for n in key cascade vertex; do
    printf 'fugaro update: acme/%s-app re-applied\n' "$n" >"$answers/update-repo-$n.out"
  done
  printf 'fugaro update: acme/oauth-app re-applied\n' >"$answers/update-repo-oauth.out"
  log_json "$answers/log.json" \
    "$OID_INFLIGHT|credsvc: audit|ok" \
    "$OID_OAUTH|credsvc: audit|ok" \
    "$OID_OAUTH|credsvc: audit|refused" \
    "$OID_OAUTH|credsvc: refused|403|craft_not_in_record|$SLUG_OAUTH" \
    "$OID_KEY|credsvc: audit|ok" \
    "$OID_CASCADE|credsvc: audit|ok" \
    "$OID_VERTEX|credsvc: audit|ok"
}

launch_json() { # launch_json <out> <slug> <run-id>
  printf '{"run_id":"%s","run":"%s/%s"}\n' "$3" "$2" "$3" >"$1"
}

# want_identity writes what the minted identity must carry: the uid of the run
# the check launched AFTER the update on the subscription repository, and the
# claims the credentials service verifies.
want_identity() { # want_identity <out> <slug> <run-id>
  python3 - "$1" "$2" "$3" <<'PY'
import json, sys
open(sys.argv[1], "w").write(json.dumps({
    "uid": "r~%s~%s" % (sys.argv[2], sys.argv[3]),
    "fs": sys.argv[2],
    "fr": sys.argv[3],
    "fp": "test",
    "iss": "fugaro-token-signer@test-project.iam.gserviceaccount.com",
    "sub": "fugaro-token-signer@test-project.iam.gserviceaccount.com",
    "aud": "https://identitytoolkit.googleapis.com/google.identity.identitytoolkit.v1.IdentityToolkit",
}))
PY
}

# ------------------------------------------------------------------ the runs

run810_raw() { # run810_raw <args...>
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    PATH="$fakebin:$PATH" FAKE810_LOG="${CALLS:-/dev/null}" FAKE810_DIR="${answers:-$work}" \
    XDG_CONFIG_HOME="$confighome" CHECK810_CONFIG= \
    CHECK810_POLLS="${CHECK810_POLLS:-2}" CHECK810_POLL_SECONDS=0 \
    CHECK810_LOG_POLL_SECONDS=0 CHECK810_LOG_WAIT_SECONDS=0 CHECK810_CURL_SECONDS=5 \
    ${RUN810_ENV[@]+"${RUN810_ENV[@]}"} \
    bash "$script" "$@" 2>"$work/err") || status=$?
  err=$(cat "$work/err")
  RUN810_ENV=()
}

run810() { # run810 [extra args...]
  local extra=(--access-header X-Fugaro-Craft)
  if [ -n "${RUN810_NO_HEADER:-}" ]; then extra=(); fi
  run810_raw --installation test --gcp-project test-project --region us-test1 \
    --oauth "$oauth" --api-key "$key" --cascade "$cascade" --vertex "$vertex" \
    --job test-job "${extra[@]}" "$@"
}

# ------------------------------------------------------------------- the cases

test_the_happy_path() {
  case_dir happy_path
  want_identity "$answers/want-identity.json" "$SLUG_OAUTH" "$OID_OAUTH"
  run810
  expect_status "$status" 0 "the happy path"
  expect_contains "$out" "CHECK 810 RESULT: PASS (exit 0)" "the happy path"
  expect_row "$out" premise PASS "the happy path"
  expect_row "$out" in-flight PASS "the happy path"
  expect_row "$out" update PASS "the happy path"
  expect_row "$out" crafts PASS "the happy path"
  expect_row "$out" says PASS "the happy path"
  expect_row "$out" in-flight-2 PASS "the happy path"
  expect_row "$out" runs-launched PASS "the happy path"
  expect_row "$out" audit PASS "the happy path"
  expect_row "$out" oauth-run PASS "the happy path"
  expect_row "$out" api-key-run PASS "the happy path"
  expect_row "$out" cascade-run PASS "the happy path"
  expect_row "$out" vertex-run PASS "the happy path"
  expect_row "$out" restore PASS "the happy path"
  # The call was really made the way the row says: the craft in the header C1
  # names, the job's own token, and the identity minted for the run the check
  # launched AFTER the update.
  expect_contains "$(cat "$CALLS")" "header X-Fugaro-Craft: chk810absent" "the hand-built call declares the craft"
  expect_contains "$(cat "$CALLS")" "X-Fugaro-Run-Identity: fake810-idtoken" "the hand-built call presents the run's identity"
  expect_contains "$(cat "$CALLS")" "Authorization: Bearer fake810-job-token" "the hand-built call presents the job's own token"
  expect_contains "$(cat "$answers/log-filter")" "$OID_OAUTH" "the audit row read the run's own lines"
  # Five runs, five updates: the in-flight one, four after the update, the
  # installation's own update and one per repository.
  local launches updates
  launches=$(grep -c '^fugaro run ' "$CALLS" || true)
  updates=$(grep -c '^fugaro update ' "$CALLS" || true)
  [ "$launches" = 5 ] || fail "the happy path: expected 5 launches, saw $launches"
  [ "$updates" = 5 ] || fail "the happy path: expected 5 updates, saw $updates"
  expect_not_contains "$out" "UNVERIFIED" "the happy path"
  expect_not_contains "$out" "|FAIL|" "the happy path"
}

test_help() {
  CALLS=
  run810_raw --help
  expect_status "$status" 0 "check810.sh --help"
  expect_contains "$out" "Usage:" "check810.sh --help"
}

test_a_dry_run_calls_nothing() {
  case_dir dry_run
  run810_raw --dry-run --installation test --gcp-project test-project --region us-test1 \
    --oauth "$oauth" --api-key "$key" --cascade "$cascade" --vertex "$vertex" --job test-job
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "Check 810 plan" "a dry run"
  [ ! -s "$CALLS" ] || fail "a dry run: it called something: $(head -c 400 "$CALLS")"
}

test_a_missing_flag_is_refused() {
  CALLS=
  run810_raw --installation test --gcp-project test-project --region us-test1
  expect_status "$status" 2 "a missing flag"
  expect_contains "$err" "no --oauth was given" "a missing flag"
}

test_an_agent_session_is_refused() {
  case_dir an_agent_session
  status=0
  out=$(CLAUDECODE=1 PATH="$fakebin:$PATH" FAKE810_LOG="$CALLS" FAKE810_DIR="$answers" \
    bash "$script" --installation test --gcp-project test-project --region us-test1 \
    --oauth "$oauth" --api-key "$key" --cascade "$cascade" --vertex "$vertex" --job test-job 2>&1) || status=$?
  expect_status "$status" 2 "an agent session"
  expect_contains "$out" "coding agent's session" "an agent session"
  [ ! -s "$CALLS" ] || fail "an agent session: it called something: $(head -c 400 "$CALLS")"
}

test_a_traced_shell_is_refused() {
  case_dir a_traced_shell
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT \
    -u AI_AGENT PATH="$fakebin:$PATH" FAKE810_LOG="$CALLS" FAKE810_DIR="$answers" \
    bash -x "$script" --installation test --gcp-project test-project --region us-test1 \
    --oauth "$oauth" --api-key "$key" --cascade "$cascade" --vertex "$vertex" --job test-job 2>&1) || status=$?
  expect_status "$status" 2 "a traced shell"
  expect_contains "$out" "this shell is tracing" "a traced shell"
}

test_a_checkout_that_is_not_a_checkout_is_refused() {
  case_dir not_a_checkout
  run810 --vertex "$work/not-a-repo"
  expect_status "$status" 2 "a checkout that is not a checkout"
  expect_contains "$err" "is not a directory" "a checkout that is not a checkout"
}

test_a_checkout_whose_auth_is_not_the_flags_is_refused() {
  case_dir wrong_auth
  # A checkout pointed at --api-key that commits another repository's auth: the
  # premise is about the four files the flags name, not about four directories.
  make_sandbox "$work/wrong_auth/other-app" other-app oauth
  run810 --api-key "$work/wrong_auth/other-app"
  expect_status "$status" 2 "a checkout on another auth"
  expect_row "$out" premise FAIL "a checkout on another auth"
  expect_contains "$out" "writes agent.auth 'oauth'" "a checkout on another auth"
  expect_contains "$out" "--api-key is the repository on api-key" "a checkout on another auth"
}

test_an_installation_already_migrated_stops_the_check() {
  case_dir already_migrated
  creds_json "$answers/creds-before.json" after
  run810
  expect_status "$status" 2 "an installation already migrated"
  expect_row "$out" premise FAIL "an installation already migrated"
  expect_contains "$out" "already carries an ACCESS record" "an installation already migrated"
  # Nothing is launched on an installation nobody read: the premise is the whole
  # point of the row.
  [ ! -s "$CALLS" ] || case "$(cat "$CALLS")" in
  *"fugaro run"*) fail "an installation already migrated: it launched a run" ;;
  esac
}

test_a_creds_node_that_cannot_be_read_stops_the_check() {
  case_dir creds_unreadable
  printf 7 >"$answers/creds-status"
  run810
  expect_status "$status" 2 "a creds node that cannot be read"
  expect_row "$out" premise UNVERIFIED "a creds node that cannot be read"
  [ ! -s "$CALLS" ] || case "$(cat "$CALLS")" in
  *"fugaro run"*) fail "a creds node that cannot be read: it launched a run" ;;
  esac
}

test_a_repository_with_no_old_record_stops_the_check() {
  case_dir no_old_record
  creds_json "$answers/creds-before.json" before "drop=$SLUG_VERTEX"
  run810
  expect_status "$status" 2 "a repository with no old record"
  expect_row "$out" premise FAIL "a repository with no old record"
  expect_contains "$out" "acme/vertex-app" "a repository with no old record"
}

test_an_update_that_fails_stops_the_check() {
  case_dir update_fails
  printf 1 >"$answers/update-status"
  run810
  expect_status "$status" 2 "an update that fails"
  expect_row "$out" update FAIL "an update that fails"
}

test_a_repository_update_that_fails_stops_the_check() {
  case_dir repo_update_fails
  printf 1 >"$answers/update-repo-status-cascade"
  run810
  expect_status "$status" 2 "a repository update that fails"
  expect_row "$out" update FAIL "a repository update that fails"
  expect_contains "$out" "acme/cascade-app" "a repository update that fails"
}

test_a_record_that_names_the_wrong_credential_is_a_finding() {
  case_dir wrong_credential
  creds_json "$answers/creds-after.json" after "$SLUG_VERTEX=anthropic-api-key"
  run810
  expect_status "$status" 1 "a record naming the wrong credential"
  expect_row "$out" crafts FAIL "a record naming the wrong credential"
  expect_contains "$out" "names NO 'vertex'" "a record naming the wrong credential"
}

test_a_subscription_record_that_names_vertex_is_a_finding() {
  case_dir subscription_names_vertex
  creds_json "$answers/creds-after.json" after "$SLUG_OAUTH=vertex"
  run810
  expect_status "$status" 1 "a subscription record naming vertex"
  expect_row "$out" crafts FAIL "a subscription record naming vertex"
}

test_a_record_the_update_never_wrote_is_a_finding() {
  case_dir no_new_record
  creds_json "$answers/creds-after.json" after "drop=$SLUG_KEY"
  run810
  expect_status "$status" 1 "a record the update never wrote"
  expect_row "$out" crafts FAIL "a record the update never wrote"
  expect_contains "$out" "acme/key-app: NO access record was written" "a record the update never wrote"
}

test_an_update_that_says_nothing_is_a_finding() {
  case_dir update_says_nothing
  : >"$answers/update.out"
  run810
  expect_status "$status" 1 "an update that says nothing"
  expect_row "$out" says FAIL "an update that says nothing"
}

test_an_in_flight_run_that_dies_is_a_finding() {
  case_dir inflight_dies
  runs_json "$answers/runs-oauth.json" "$SLUG_OAUTH/$OID_INFLIGHT" failed "$SLUG_OAUTH/$OID_OAUTH" succeeded
  run810
  expect_status "$status" 1 "an in-flight run that dies"
  expect_row "$out" in-flight-2 FAIL "an in-flight run that dies"
  expect_row "$out" oauth-run PASS "an in-flight run that dies"
}

test_an_in_flight_run_that_never_ends_leaves_the_row_unverified() {
  case_dir inflight_never_ends
  runs_json "$answers/runs-oauth.json" "$SLUG_OAUTH/$OID_INFLIGHT" - "$SLUG_OAUTH/$OID_OAUTH" succeeded
  run810
  expect_status "$status" 3 "an in-flight run that never ends"
  expect_row "$out" in-flight-2 UNVERIFIED "an in-flight run that never ends"
}

test_a_run_that_never_ends_leaves_its_row_unverified() {
  case_dir run_never_ends
  runs_json "$answers/runs-key.json" "$SLUG_KEY/$OID_KEY" -
  run810
  expect_status "$status" 3 "a run that never ends"
  expect_row "$out" api-key-run UNVERIFIED "a run that never ends"
  expect_row "$out" oauth-run PASS "a run that never ends"
}

test_a_listing_that_does_not_answer_leaves_the_row_unverified() {
  case_dir listing_unreadable
  printf 1 >"$answers/runs-status-vertex"
  run810
  expect_status "$status" 3 "a listing that does not answer"
  expect_row "$out" vertex-run UNVERIFIED "a listing that does not answer"
}

test_a_run_with_no_served_line_is_a_finding() {
  case_dir no_served_line
  log_json "$answers/log.json" \
    "$OID_INFLIGHT|credsvc: audit|ok" \
    "$OID_OAUTH|credsvc: audit|ok" \
    "$OID_OAUTH|credsvc: audit|refused" \
    "$OID_OAUTH|credsvc: refused|403|craft_not_in_record|$SLUG_OAUTH" \
    "$OID_CASCADE|credsvc: audit|ok" \
    "$OID_VERTEX|credsvc: audit|ok"
  run810
  expect_status "$status" 1 "a run with no served line"
  expect_row "$out" api-key-run FAIL "a run with no served line"
  expect_contains "$out" "did not go through its gateway" "a run with no served line"
}

test_a_basis_that_moved_is_a_finding() {
  case_dir basis_moved
  record_json "$answers/record_${SLUG_KEY}_$OID_KEY.json" subscription
  run810
  expect_status "$status" 1 "a basis that moved"
  expect_row "$out" api-key-run FAIL "a basis that moved"
  expect_contains "$out" "not 'api-list'" "a basis that moved"
}

test_the_subscription_run_is_held_to_the_basis_it_had_before() {
  case_dir basis_changed_for_oauth
  record_json "$answers/record_${SLUG_OAUTH}_$OID_OAUTH.json" api-list
  run810
  expect_status "$status" 1 "the subscription basis"
  expect_row "$out" oauth-run FAIL "the subscription basis"
  expect_contains "$out" "not 'subscription'" "the subscription basis"
}

test_a_record_with_no_basis_leaves_the_row_unverified() {
  case_dir no_basis
  record_json "$answers/record_${SLUG_KEY}_$OID_KEY.json" -
  run810
  expect_status "$status" 3 "a record with no basis"
  expect_row "$out" api-key-run UNVERIFIED "a record with no basis"
}

test_a_run_that_ends_failed_is_a_finding() {
  case_dir run_failed
  runs_json "$answers/runs-cascade.json" "$SLUG_CASCADE/$OID_CASCADE" failed
  run810
  expect_status "$status" 1 "a run that ends failed"
  expect_row "$out" cascade-run FAIL "a run that ends failed"
}

test_a_launch_that_fails_is_a_finding() {
  case_dir launch_fails
  printf 1 >"$answers/launch-status-vertex"
  run810
  expect_status "$status" 1 "a launch that fails"
  expect_row "$out" vertex-run FAIL "a launch that fails"
  expect_row "$out" oauth-run PASS "a launch that fails"
}

test_a_served_hand_built_call_is_a_finding() {
  case_dir call_served
  printf 200 >"$answers/call-code"
  printf '{"content":[{"type":"text","text":"served"}]}\n' >"$answers/call-answer.json"
  run810
  expect_status "$status" 1 "a served hand-built call"
  expect_row "$out" audit FAIL "a served hand-built call"
  expect_contains "$out" "SERVED the hand-built call" "a served hand-built call"
}

test_a_refusal_with_no_audit_line_is_a_finding() {
  case_dir refusal_without_audit
  log_json "$answers/log.json" \
    "$OID_INFLIGHT|credsvc: audit|ok" \
    "$OID_OAUTH|credsvc: audit|ok" \
    "$OID_OAUTH|credsvc: refused|403|craft_not_in_record|$SLUG_OAUTH" \
    "$OID_KEY|credsvc: audit|ok" \
    "$OID_CASCADE|credsvc: audit|ok" \
    "$OID_VERTEX|credsvc: audit|ok"
  run810
  expect_status "$status" 1 "a refusal with no audit line"
  expect_row "$out" audit FAIL "a refusal with no audit line"
  expect_contains "$out" "AUDIT log holds no line" "a refusal with no audit line"
}

test_a_refusal_that_does_not_name_the_craft_is_unverified() {
  case_dir refusal_another_reason
  printf '{"error":{"type":"missing_declaration"}}\n' >"$answers/call-answer.json"
  log_json "$answers/log.json" \
    "$OID_INFLIGHT|credsvc: audit|ok" \
    "$OID_OAUTH|credsvc: audit|ok" \
    "$OID_OAUTH|credsvc: audit|refused" \
    "$OID_OAUTH|credsvc: refused|403|missing_declaration|$SLUG_OAUTH" \
    "$OID_KEY|credsvc: audit|ok" \
    "$OID_CASCADE|credsvc: audit|ok" \
    "$OID_VERTEX|credsvc: audit|ok"
  run810
  expect_status "$status" 3 "a refusal for another reason"
  expect_row "$out" audit UNVERIFIED "a refusal for another reason"
  expect_contains "$out" "NAMES the craft" "a refusal for another reason"
}

test_two_refusals_for_the_run_leave_the_row_unverified() {
  case_dir two_refusals
  log_json "$answers/log.json" \
    "$OID_OAUTH|credsvc: audit|ok" \
    "$OID_OAUTH|credsvc: audit|refused" \
    "$OID_OAUTH|credsvc: refused|403|craft_not_in_record|$SLUG_OAUTH" \
    "$OID_OAUTH|credsvc: audit|refused" \
    "$OID_OAUTH|credsvc: refused|403|craft_not_in_record|$SLUG_OAUTH" \
    "$OID_KEY|credsvc: audit|ok" \
    "$OID_CASCADE|credsvc: audit|ok" \
    "$OID_VERTEX|credsvc: audit|ok"
  run810
  expect_status "$status" 3 "two refusals for the run"
  expect_row "$out" audit UNVERIFIED "two refusals for the run"
}

test_an_audit_read_that_does_not_answer_leaves_the_row_unverified() {
  case_dir log_unreadable
  printf 1 >"$answers/log-status"
  run810
  expect_status "$status" 3 "a log read that does not answer"
  expect_row "$out" audit UNVERIFIED "a log read that does not answer"
  expect_row "$out" oauth-run UNVERIFIED "a log read that does not answer"
}

test_no_access_header_leaves_the_audit_row_unverified() {
  case_dir no_header
  RUN810_NO_HEADER=1 run810
  expect_status "$status" 3 "no access header"
  expect_row "$out" audit UNVERIFIED "no access header"
  expect_contains "$out" "no --access-header was given" "no access header"
  # Nothing was minted for a row that cannot make its call.
  expect_not_contains "$(cat "$CALLS")" "signJwt" "no access header"
}

test_a_job_with_no_key_leaves_the_audit_row_unverified() {
  case_dir job_without_key
  job_json "$answers/job.json" nokey
  run810
  expect_status "$status" 3 "a job with no key"
  expect_row "$out" audit UNVERIFIED "a job with no key"
  expect_contains "$out" "carries no FUGARO_FIREBASE_API_KEY" "a job with no key"
}

test_a_job_that_names_no_account_leaves_the_audit_row_unverified() {
  case_dir job_without_account
  printf '{"spec":{"template":{"spec":{"template":{"spec":{"containers":[]}}}}}}\n' >"$answers/job.json"
  run810
  expect_status "$status" 3 "a job that names no account"
  expect_row "$out" audit UNVERIFIED "a job that names no account"
}

test_a_mint_that_fails_leaves_the_audit_row_unverified() {
  case_dir mint_fails
  printf 403 >"$answers/signjwt-code"
  run810
  expect_status "$status" 3 "a mint that fails"
  expect_row "$out" audit UNVERIFIED "a mint that fails"
  expect_contains "$out" "signJwt answered HTTP 403" "a mint that fails"
}

test_an_exchange_that_fails_leaves_the_audit_row_unverified() {
  case_dir exchange_fails
  printf 400 >"$answers/signin-code"
  run810
  expect_status "$status" 3 "an exchange that fails"
  expect_row "$out" audit UNVERIFIED "an exchange that fails"
  expect_contains "$out" "refused the exchange at test-project" "an exchange that fails"
}

test_a_service_with_no_url_leaves_the_audit_row_unverified() {
  case_dir no_service_url
  : >"$answers/service-url.txt"
  run810
  expect_status "$status" 3 "a service with no URL"
  expect_row "$out" audit UNVERIFIED "a service with no URL"
}

test_a_call_that_does_not_answer_leaves_the_audit_row_unverified() {
  case_dir call_no_answer
  printf 7 >"$answers/call-status"
  run810
  expect_status "$status" 3 "a call that does not answer"
  expect_row "$out" audit UNVERIFIED "a call that does not answer"
  expect_contains "$out" "did not answer" "a call that does not answer"
}

test_a_local_config_with_no_database_url_stops_the_check() {
  case_dir no_rtdb
  printf 'project: test\n' >"$confighome/fugaro/projects/test.yaml"
  run810
  expect_status "$status" 2 "a local config with no database URL"
  expect_row "$out" premise UNVERIFIED "a local config with no database URL"
}

test_an_rtdb_url_that_is_given_is_used() {
  case_dir rtdb_given
  printf 'project: test\n' >"$confighome/fugaro/projects/test.yaml"
  run810 --rtdb-url https://db.invalid
  expect_status "$status" 0 "an rtdb url that is given"
  expect_contains "$(cat "$CALLS")" "curl https://db.invalid/creds.json" "an rtdb url that is given"
}

test_an_unknown_argument_is_refused() {
  CALLS=
  run810_raw --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument --nonsense" "an unknown argument"
}

# ------------------------------------------------------------------- the footer

test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK810_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK810_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_the_happy_path
t test_help
t test_a_dry_run_calls_nothing
t test_a_missing_flag_is_refused
t test_an_agent_session_is_refused
t test_a_traced_shell_is_refused
t test_a_checkout_that_is_not_a_checkout_is_refused
t test_a_checkout_whose_auth_is_not_the_flags_is_refused
t test_an_installation_already_migrated_stops_the_check
t test_a_creds_node_that_cannot_be_read_stops_the_check
t test_a_repository_with_no_old_record_stops_the_check
t test_an_update_that_fails_stops_the_check
t test_a_repository_update_that_fails_stops_the_check
t test_a_record_that_names_the_wrong_credential_is_a_finding
t test_a_subscription_record_that_names_vertex_is_a_finding
t test_a_record_the_update_never_wrote_is_a_finding
t test_an_update_that_says_nothing_is_a_finding
t test_an_in_flight_run_that_dies_is_a_finding
t test_an_in_flight_run_that_never_ends_leaves_the_row_unverified
t test_a_run_that_never_ends_leaves_its_row_unverified
t test_a_listing_that_does_not_answer_leaves_the_row_unverified
t test_a_run_with_no_served_line_is_a_finding
t test_a_basis_that_moved_is_a_finding
t test_the_subscription_run_is_held_to_the_basis_it_had_before
t test_a_record_with_no_basis_leaves_the_row_unverified
t test_a_run_that_ends_failed_is_a_finding
t test_a_launch_that_fails_is_a_finding
t test_a_served_hand_built_call_is_a_finding
t test_a_refusal_with_no_audit_line_is_a_finding
t test_a_refusal_that_does_not_name_the_craft_is_unverified
t test_two_refusals_for_the_run_leave_the_row_unverified
t test_an_audit_read_that_does_not_answer_leaves_the_row_unverified
t test_no_access_header_leaves_the_audit_row_unverified
t test_a_job_with_no_key_leaves_the_audit_row_unverified
t test_a_job_that_names_no_account_leaves_the_audit_row_unverified
t test_a_mint_that_fails_leaves_the_audit_row_unverified
t test_an_exchange_that_fails_leaves_the_audit_row_unverified
t test_a_service_with_no_url_leaves_the_audit_row_unverified
t test_a_call_that_does_not_answer_leaves_the_audit_row_unverified
t test_a_local_config_with_no_database_url_stops_the_check
t test_an_rtdb_url_that_is_given_is_used
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check810_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check810_test.sh: ok"
