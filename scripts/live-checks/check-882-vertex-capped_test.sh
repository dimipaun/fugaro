#!/usr/bin/env bash
# Tests for scripts/live-checks/check-882-vertex-capped.sh, with fake `fugaro`,
# fake `gcloud` and fake `curl` on PATH: NOTHING here reaches a cloud, an
# installation, a run, a model or Vertex, no agent runs a live command, and the
# fakes record every call they are given so a case can assert what the check did
# — and did not — do.
#
# The fakes are a small model of one installation after release 1: a routing
# table whose entries route two Claude models to Vertex, an `enforce` project
# with a per-run cap, one Cloud Run job whose service account holds no Vertex
# permission, and a run the check launches whose own `result.json` in the runs
# bucket was served by Vertex and priced. Every case below bends one of those: a
# project that does not enforce, a table with no Vertex source, a job that can
# reach Vertex, a token that cannot be minted, a run a key served, a stage
# nobody priced, a run over its cap.
#
# Usage: bash scripts/live-checks/check-882-vertex-capped_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-882-vertex-capped.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK882_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK882_TEST_HEAD:-900}")" ;;
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
expect_row() { # out key verdict what
  local line
  line=$(printf '%s\n' "$1" | grep -m1 "^FACT $2:" || true)
  [ -n "$line" ] || {
    fail "$4: the block carries no $2 row"
    return 0
  }
  expect_contains "$line" ": $3 " "$4: the $2 row reads $3"
}

project=fugaro-chk882-test
bucket=fugaro-runs-${project}
job=chk882-job
job_sa=${job}@${project}.iam.gserviceaccount.com
other_job=chk882-job-two
other_sa=${other_job}@${project}.iam.gserviceaccount.com
model=claude-opus-5-5
model2=claude-sonnet-5
probe=sentinel-for

# --------------------------------------------------------------------- fakes
# write_fake_fugaro DIR: a stand-in for `fugaro` that answers the reads this
# check makes out of the case's own files under DIR/answers, and records every
# call. `run` writes the launch answer (or refuses, when the case says so) and
# records which model it was pinned to, so `storage cat` can answer a record for
# the run that model was pinned to.
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for check-882's test only. Every answer is a file of
# the case's own under $FAKE882_ANSWERS; a document the case did not write is a
# refusal, so a case cannot pass by the fake inventing an answer.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE882_LOG/fugaro.calls"
mode=${FAKE882_MODE:-default}
answers=${FAKE882_ANSWERS:?}
case "${1:-}" in
version) printf '%s\n' "${FAKE882_VERSION:-0.27.0}" ;;
doctor)
  cat "$answers/doctor.json"
  ;;
routing)
  cat "$answers/table.json"
  ;;
budget)
  # The real command narrows its listing to the repository `--repo` names
  # (internal/cli/budget.go: `keys[budget.Key(wantSlug)] = true`), so the fake
  # does too: a check that forgets --repo reads whatever row the listing happens
  # to carry first, and a case can catch it.
  repo_arg=""
  prev=""
  for a in "$@"; do
    case "$a" in
    --repo=*) repo_arg=${a#--repo=} ;;
    esac
    if [ "$prev" = "--repo" ]; then repo_arg=$a; fi
    prev=$a
  done
  if [ -z "$repo_arg" ]; then
    cat "$answers/budget.json"
  else
    python3 - "$answers/budget.json" "$repo_arg" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
want = sys.argv[2]
rows = []
for r in doc.get("repos") or []:
    if not isinstance(r, dict):
        continue
    if want in (r.get("slug") or "", r.get("repo") or "") or (r.get("repo") or "").endswith("/" + want):
        rows.append(r)
doc["repos"] = rows
print(json.dumps(doc))
PY
  fi
  ;;
runs)
  # A case may model a run that is still going when the check first polls it:
  # with answers/runs.inflight present the FIRST poll answers that row and every
  # later poll answers the launched runs. The real CLI answers a run in flight
  # with outcome "none" and terminal false — the runner writes OutcomeNone at
  # bootstrap — so the ended signal a reader may stop on is `terminal`, and the
  # rows a launched run answers carry it true.
  polls=$(grep -c . "$FAKE882_LOG/runs.calls" 2>/dev/null || true)
  printf 'poll\n' >>"$FAKE882_LOG/runs.calls"
  if [ -f "$answers/runs.inflight" ] && [ "${polls:-0}" -eq 0 ]; then
    cat "$answers/runs.inflight"
  elif [ -f "$answers/runs.json" ]; then
    cat "$answers/runs.json"
  else
    awk -F'|' 'NR>1{printf ","} {printf "{\"run\":\"%s\",\"run_id\":\"%s\",\"outcome\":\"ready\",\"terminal\":true,\"settled\":true}", $1, $2}' \
      "$FAKE882_LOG/launches" | sed 's/^/[/;s/$/]/'
    printf '\n'
  fi
  ;;
run)
  case "$mode" in
  launch-refused)
    echo "fugaro: unknown flag --model: this build pins a run's model with 'fugaro models use' and has no --model override" >&2
    exit 2
    ;;
  esac
  pinned=""
  prev=""
  for a in "$@"; do
    case "$a" in
    --model=*) pinned=${a#--model=} ;;
    esac
    if [ "$prev" = "--model" ]; then pinned=$a; fi
    prev=$a
  done
  n=$(grep -c . "$FAKE882_LOG/launches" 2>/dev/null || true)
  n=$(( ${n:-0} + 1 ))
  id="20261122090000-b882-${n}"
  ref="chk882/${id}"
  printf '%s|%s|%s\n' "$ref" "$id" "$pinned" >>"$FAKE882_LOG/launches"
  printf '{"run":"%s","run_id":"%s"}\n' "$ref" "$id"
  ;;
*)
  echo "fugaro: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# write_fake_gcloud DIR: a stand-in for `gcloud` for the impersonated token, the
# job list, the IAM policy, a custom role's permissions and the run's own record
# out of the runs bucket. It prints no value of anything: the record is a file of
# the case's own, and the token it mints is a stand-in that must never be shown.
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for check-882's test only.
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"$FAKE882_LOG/gcloud.calls"
mode=${FAKE882_MODE:-default}
answers=${FAKE882_ANSWERS:?}
sub="${1:-} ${2:-} ${3:-}"
case "$sub" in
"auth print-access-token"*)
  case "$mode" in
  no-impersonation)
    echo "ERROR: (gcloud.auth.print-access-token) PERMISSION_DENIED: the caller does not have iam.serviceAccounts.getAccessToken on this service account" >&2
    exit 1
    ;;
  esac
  sa=""
  for a in "$@"; do
    case "$a" in
    --impersonate-service-account=*) sa=${a#*=} ;;
    esac
  done
  if [ -z "$sa" ]; then
    echo "ERROR: (gcloud.auth.print-access-token) no --impersonate-service-account was asked" >&2
    exit 1
  fi
  printf 'sentinel-for-%s\n' "$sa"
  ;;
"run jobs list"*)
  cat "$answers/jobs.json"
  ;;
"projects get-iam-policy"*)
  cat "$answers/iam.json"
  ;;
"iam roles describe"*)
  # The real command takes a predefined role with its `roles/` prefix and no
  # --project, and a custom one as a bare ID plus the --project it belongs to
  # (`iam_util.GetRoleName`). The fake holds the same contract, so a check that
  # hands over a spelling gcloud refuses is refused here too, and the case that
  # expects the role's permissions fails.
  role="" role_project=""
  shift 3
  while [ $# -gt 0 ]; do
    case "$1" in
    --project=*) role_project=${1#--project=} ;;
    --project)
      role_project=${2:-}
      shift
      ;;
    --format=*) ;;
    --format) shift ;;
    -*) ;;
    *) [ -n "$role" ] || role=$1 ;;
    esac
    shift
  done
  python3 - "$answers/roles.json" "$role" "$role_project" <<'PY'
import json, sys
path, role, project = sys.argv[1], sys.argv[2], sys.argv[3]
try:
    doc = json.load(open(path))
except Exception:
    sys.exit(1)
if role.startswith("projects/") or role.startswith("organizations/"):
    print("ERROR: (gcloud.iam.roles.describe) Invalid value for [ROLE_ID]: The role id should not include any 'projects/' or 'organizations/' prefix.", file=sys.stderr)
    sys.exit(1)
if role.startswith("roles/"):
    if project:
        print("ERROR: (gcloud.iam.roles.describe) Invalid value for [ROLE_ID]: The role id that starts with 'roles/' only stands for predefined role. Should not specify the project or organization for predefined roles", file=sys.stderr)
        sys.exit(1)
    want = role
else:
    if "/" in role:
        print("ERROR: (gcloud.iam.roles.describe) Invalid value for [ROLE_ID]: The role id should not include any '/' character.", file=sys.stderr)
        sys.exit(1)
    if not project:
        print("ERROR: (gcloud.iam.roles.describe) Missing required argument [--organization or --project]: Should specify the project or organization name for custom roles.", file=sys.stderr)
        sys.exit(1)
    want = "projects/%s/roles/%s" % (project, role)
for r in doc.get("roles") or []:
    if (r.get("name") or "") == want:
        print(json.dumps(r))
        sys.exit(0)
print("ERROR: (gcloud.iam.roles.describe) NOT_FOUND: Role %s not found" % want, file=sys.stderr)
sys.exit(1)
PY
  ;;
"storage cat"*)
  case "$mode" in
  record-absent)
    echo "ERROR: (gcloud.storage.cat) NotFoundException: 404 no such object" >&2
    exit 1
    ;;
  esac
  ref=${3#gs://*/runs/}
  ref=${ref%/result.json}
  pinned=$(awk -F'|' -v r="$ref" '$1 == r {print $3}' "$FAKE882_LOG/launches" | head -n 1)
  sed "s|@MODEL@|${pinned}|g" "$answers/record.json"
  ;;
*)
  echo "gcloud: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

# write_fake_curl DIR: a stand-in for `curl` that answers the Vertex probe. It
# records the URL and whether the Authorization header arrived through the
# `--config` file rather than on the command line, and answers the status the
# case wrote. It never prints the config file.
write_fake_curl() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A stand-in for `curl`, for check-882's test only.
set -euo pipefail
answers=${FAKE882_ANSWERS:?}
out="" wfmt="" conf="" url=""
while [ $# -gt 0 ]; do
  case "$1" in
  --config) conf=${2:-}; shift 2 ;;
  --output | -o) out=${2:-}; shift 2 ;;
  --write-out | -w) wfmt=${2:-}; shift 2 ;;
  --request | -X | --data-binary | -d | --data | --data-raw) shift 2 ;;
  --header | -H)
    printf 'header-arg=%s\n' "${2:-}" >>"$FAKE882_LOG/curl.calls"
    shift 2
    ;;
  --silent | -s | --show-error | -S | --fail) shift ;;
  *) url=$1; shift ;;
  esac
done
printf 'url=%s\n' "$url" >>"$FAKE882_LOG/curl.calls"
if [ -n "$conf" ] && grep -q '^header = "Authorization: Bearer ' "$conf" 2>/dev/null; then
  printf 'auth-from-config=yes\n' >>"$FAKE882_LOG/curl.calls"
fi
if [ -n "$conf" ] && grep -q 'Authorization' <<<"$url"; then
  printf 'auth-in-url=yes\n' >>"$FAKE882_LOG/curl.calls"
fi
status=$(cat "$answers/vertex.status" 2>/dev/null || printf '403')
[ -n "$out" ] && cat "$answers/vertex.body" >"$out"
case "$wfmt" in
*%{http_code}*) printf '%s' "$status" ;;
esac
exit 0
CURL_EOF
  chmod +x "$1/bin/curl"
}

# write_answers DIR: the documents every case starts from, each of them the
# answer the check is about. A case overwrites the one it bends.
write_answers() { # dir
  mkdir -p "$1/answers"
  local a=$1/answers
  cat >"$a/doctor.json" <<JSON
{"ok":true,"project":{"name":"chk882","gcp_project":"${project}","region":"us-east5","registry_host":"us-east5-docker.pkg.dev"}}
JSON
  cat >"$a/table.json" <<JSON
{"entries":[
  {"match":"${model}","chain":[{"kind":"vertex"}]},
  {"match":"${model2}","chain":[{"kind":"vertex"}]},
  {"match":"claude-*","chain":[{"kind":"anthropic","name":"project-key"}]}
]}
JSON
  # Two repositories, and the OTHER one comes first with a cap of its own: the
  # check must read the row of the repository the checkout's origin remote names
  # (`acme/chk882`, cap $10), not the first capped row in the listing — which is
  # what a multi-repository installation would otherwise be reported as.
  cat >"$a/budget.json" <<JSON
{"project":"chk882","gcp_project":"${project}","mode":"enforce","day":"2026-11-22",
 "defaults":{"repo_daily_cap_usd":20,"repo_per_run_cap_usd":10},
 "repos":[{"slug":"chk882-other","repo":"acme/chk882-other","per_run_cap_usd":1,"per_run_cap_source":"repo",
   "daily_cap_usd":1,"daily_cap_source":"repo","counted_usd":0,"spent_usd":0,"notional_usd":0,
   "leased_usd":0,"headroom_usd":1,"running":[]},
  {"slug":"chk882","repo":"acme/chk882","per_run_cap_usd":10,"per_run_cap_source":"default",
   "daily_cap_usd":20,"daily_cap_source":"default","counted_usd":0,"spent_usd":0,"notional_usd":0,
   "leased_usd":0,"headroom_usd":20,"running":[]}],
 "global":{"daily_cap_usd":100,"per_run_cap_usd":10,"counted_usd":0,"spent_usd":0,"notional_usd":0,"leased_usd":0,"headroom_usd":100},
 "running":[],"warnings":[]}
JSON
  cat >"$a/jobs.json" <<JSON
[{"metadata":{"name":"${job}"},
  "spec":{"template":{"template":{"serviceAccountName":"${job_sa}"}}}}]
JSON
  cat >"$a/iam.json" <<JSON
{"bindings":[
  {"role":"roles/run.developer","members":["user:chk882-owner@example.com"]},
  {"role":"roles/aiplatform.user","members":["serviceAccount:someone-else@${project}.iam.gserviceaccount.com"]}
]}
JSON
  cat >"$a/roles.json" <<JSON
{"roles":[{"name":"roles/run.developer","includedPermissions":["run.jobs.get","run.jobs.list","iam.serviceAccounts.actAs"]},
  {"name":"roles/aiplatform.user","includedPermissions":["aiplatform.endpoints.predict","aiplatform.endpoints.get","aiplatform.models.list"]},
  {"name":"projects/${project}/roles/chk882vertex","includedPermissions":["aiplatform.endpoints.predict"]}]}
JSON
  cat >"$a/vertex.status" <<JSON
403
JSON
  cat >"$a/vertex.body" <<JSON
{"error":{"code":403,"message":"Permission 'aiplatform.endpoints.predict' denied on resource","status":"PERMISSION_DENIED"}}
JSON
  cat >"$a/record.json" <<JSON
{"repo":"acme/chk882","status":"succeeded",
 "cost":{"model_usd":0.31,"model_notional_usd":0.0,"model_source":"gateway","fallback_usd":0.0,"total_usd":0.44,
   "credential":"${probe}"},
 "policy":{"effective":{"per_run_usd":10,"mode":"enforce"}},
 "stages":[
   {"stage":"implement","model":"@MODEL@","model_usd":0.31,"source":{"kind":"vertex","name":"vertex-us-east5"},"fallback":false}
 ]}
JSON
}

run_check() { # run_check DIR [K=V ...] [-- ARGS...]
  local dir=$1
  shift
  local envs=() cli=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    envs+=("$1")
    shift
  done
  if [ "${1:-}" = "--" ]; then
    shift
    cli=("$@")
  fi
  mkdir -p "$dir/logs" "$dir/home" "$dir/tmp"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/curl.calls"
  : >"$dir/logs/launches"
  : >"$dir/logs/runs.calls"
  status=0
  # `-x` is bash's own flag, so it goes BEFORE the script: the check refuses an
  # argument it does not know, and the case that measures the tracing refusal is
  # the only way to start a run that is refusing to be traced.
  local bashargs=()
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  # The check refuses a coding agent's session, and this test may itself run in
  # one: the markers are dropped for the child, and the case that measures the
  # refusal sets one back.
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE882_LOG="$dir/logs" FAKE882_ANSWERS="$dir/answers" \
    FAKE882_MODE="${FAKE882_MODE:-default}" \
    ${envs[@]+"${envs[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

# setup_case DIR [K=V ...]: a checkout, the three fakes and the answers, plus
# any environment the case wants written beside them.
setup_case() { # dir
  local dir=$1
  shift
  write_fake_fugaro "$dir"
  write_fake_gcloud "$dir"
  write_fake_curl "$dir"
  write_answers "$dir"
  mkdir -p "$dir/checkout"
  cat >"$dir/checkout/fugaro.yaml" <<'YAML'
version: 1
YAML
  git -C "$dir/checkout" init -q
  git -C "$dir/checkout" add fugaro.yaml
  git -C "$dir/checkout" -c user.email=chk882@example.com -c user.name=chk882 commit -q -m init
  # The origin remote is what names the repository the runs are charged to and
  # whose per-run cap the mode row reads; `fugaro run` resolves it the same way.
  git -C "$dir/checkout" remote add origin git@github.com:acme/chk882.git
}

default_args() { # dir
  printf -- '--checkout %s --gcp-project %s --region us-east5' "$1/checkout" "$project"
}

assert_no_launch() { # dir what
  if grep -q '^fugaro run ' "$1/logs/fugaro.calls" 2>/dev/null; then
    fail "$2: a run was launched where none may be"
  fi
}
assert_launched() { # dir want what
  local n
  n=$(grep -c '^fugaro run ' "$1/logs/fugaro.calls" 2>/dev/null || true)
  [ "$n" = "$2" ] || fail "$3: expected $2 launch(es), got $n"
}
assert_scratch_removed() { # dir what
  if [ -n "$scratch" ] && [ -e "$scratch" ]; then
    fail "$2: the scratch directory $scratch is still there"
  fi
}
assert_ran_in_the_scratch() { # dir what
  [ -n "$scratch" ] || {
    fail "$2: the check named no scratch directory"
    return 0
  }
  case "$scratch" in "$3"/*) ;; *) fail "$2: the scratch directory $scratch is not under $3" ;; esac
}

# ------------------------------------------------------------------- the cases
test_help() {
  local dir=$work/help
  setup_case "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage"
  expect_contains "$out" "check-882-vertex-capped.sh --dry-run" "--help: the dry run is offered"
  expect_absent "$out" "CHECK 882 RESULT" "--help: no block"
  [ -z "$scratch" ] || fail "--help: it made a scratch directory"
}

test_dry_run() {
  local dir=$work/dry
  setup_case "$dir"
  run_check "$dir" -- --dry-run
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 882 RESULT" "dry run: the block"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT mapped: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$out" "CHECK 882 RESULT: PASS (exit 0)" "dry run: the verdict line"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "version" "dry run: no fugaro call"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "dry run: no gcloud call"
  expect_absent "$(cat "$dir/logs/curl.calls")" "url=" "dry run: no curl call"
  assert_no_launch "$dir" "dry run"
  assert_scratch_removed "$dir" "dry run"
}

test_happy_path() {
  local dir=$work/happy
  setup_case "$dir"
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "FACT installation: PASS" "happy path: the installation row"
  expect_contains "$out" "$project" "happy path: the project doctor answered"
  expect_contains "$out" "FACT mode: PASS" "happy path: the mode row"
  expect_contains "$out" "enforce" "happy path: the mode it read"
  expect_contains "$out" "acme/chk882" "happy path: the repository the origin remote names"
  expect_contains "$out" "FACT mapped: PASS" "happy path: the mapped row"
  expect_contains "$out" "$model" "happy path: a mapped model"
  expect_contains "$out" "$model2" "happy path: the other mapped model"
  expect_contains "$out" "FACT chain: PASS" "happy path: the chain row"
  expect_contains "$out" "FACT jobs: PASS" "happy path: the jobs row"
  expect_contains "$out" "$job" "happy path: the job it read"
  expect_contains "$out" "$job_sa" "happy path: the account it read"
  expect_contains "$out" "FACT token: PASS" "happy path: the token row"
  expect_contains "$out" "never printed" "happy path: the token is never printed"
  expect_contains "$out" "FACT grants: PASS" "happy path: the grants row"
  expect_contains "$out" "aiplatform.endpoints.predict" "happy path: the permission it looked for"
  expect_contains "$out" "FACT vertex-refused: PASS" "happy path: the refusal row"
  expect_contains "$out" "403" "happy path: the status it read"
  expect_contains "$out" "FACT runs: PASS" "happy path: the runs row"
  expect_contains "$out" "FACT served: PASS" "happy path: the served row"
  expect_contains "$out" "FACT priced: PASS" "happy path: the priced row"
  expect_contains "$out" "FACT settled: PASS" "happy path: the settled row"
  expect_contains "$out" "CHECK 882 RESULT: PASS (exit 0)" "happy path: the verdict"
  assert_launched "$dir" 2 "happy path: one run per mapped model"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "run --task-file" "happy path: the launch command"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "--model ${model}" "happy path: the model is pinned"
  # The probe: the token travels in the curl config file, never on a command
  # line, and the call is the project's own Vertex path.
  expect_contains "$(cat "$dir/logs/curl.calls")" "auth-from-config=yes" "happy path: the token is read from the config file"
  expect_absent "$(cat "$dir/logs/curl.calls")" "Authorization: Bearer" "happy path: the token is never an argument"
  expect_absent "$(cat "$dir/logs/curl.calls")" "$probe" "happy path: no token in the call log"
  expect_contains "$(cat "$dir/logs/curl.calls")" "https://us-east5-aiplatform.googleapis.com/v1/projects/${project}/locations/us-east5/publishers/anthropic/models/" "happy path: the Vertex path it called"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "--impersonate-service-account=${job_sa}" "happy path: the job's own account is impersonated"
  assert_scratch_removed "$dir" "happy path"
}

test_the_block_never_carries_a_value() {
  local dir=$work/value
  setup_case "$dir"
  run_check "$dir" -- $(default_args "$dir")
  expect_absent "$out" "$probe" "no value: the block"
  expect_absent "$err" "$probe" "no value: not on stderr either"
  expect_absent "$(cat "$dir/logs/curl.calls")" "$probe" "no value: not in the call log"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "$probe" "no value: not in the gcloud log"
}

test_an_agent_session_is_refused() {
  local dir=$work/agent
  setup_case "$dir"
  run_check "$dir" CLAUDECODE=1 -- $(default_args "$dir")
  expect_status "$status" 2 "a coding agent's session"
  expect_contains "$err" "coding agent's session" "an agent's session: the refusal"
  assert_no_launch "$dir" "an agent's session"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  setup_case "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 2 "bash -x"
  expect_contains "$err" "is tracing" "bash -x: the guard names what it refuses"
  assert_no_launch "$dir" "bash -x"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  setup_case "$dir"
  run_check "$dir" -- --checkout "$dir/checkout" --gcp-project fugaro-belong --region us-east5
  expect_status "$status" 2 "a project naming belong"
  expect_contains "$err" "never run there" "belong: the refusal"
  assert_no_launch "$dir" "belong"
}

test_a_belong_installation_in_the_checkout_is_refused() {
  # The project this check goes on to use is doctor's own when --gcp-project is
  # not given, so a checkout whose installation names belong must be refused on
  # WHAT DOCTOR ANSWERS — guarding only what was typed guards the empty string
  # and would launch the paid runs of the owner's own installation.
  local dir=$work/belongdoctor
  setup_case "$dir"
  python3 - "$dir/answers/doctor.json" <<'PY'
import json, sys
p = sys.argv[1]
doc = json.load(open(p))
doc["project"]["name"] = "belong"
doc["project"]["gcp_project"] = "fugaro-belong"
json.dump(doc, open(p, "w"))
PY
  run_check "$dir" -- --checkout "$dir/checkout"
  expect_status "$status" 2 "a checkout whose doctor names belong"
  expect_contains "$err" "never run there" "belong in the checkout: the refusal"
  expect_contains "$out" "FACT belong: FAIL" "belong in the checkout: the row"
  expect_contains "$out" "fugaro doctor --json" "belong in the checkout: where the name came from"
  assert_no_launch "$dir" "belong in the checkout"
}

test_a_region_that_disagrees_with_the_installation_stops() {
  # The region is the Cloud Run region the jobs live in AND the Vertex location
  # the probe calls: a region that is not the installation's reads the wrong job
  # list and calls the wrong Vertex.
  local dir=$work/regiondisagree
  setup_case "$dir"
  python3 - "$dir/answers/doctor.json" <<'PY'
import json, sys
p = sys.argv[1]
doc = json.load(open(p))
doc["project"]["region"] = "us-central1"
json.dump(doc, open(p, "w"))
PY
  run_check "$dir" -- --checkout "$dir/checkout" --gcp-project "$project" --region us-east5
  expect_status "$status" 2 "a region that is not the installation's"
  expect_contains "$err" "us-central1" "a disagreeing region: the installation's own"
  expect_contains "$err" "us-east5" "a disagreeing region: what was typed"
  assert_no_launch "$dir" "a disagreeing region"
}

test_the_region_is_the_installations_own() {
  # No --region and no --gcp-project: both are the checkout's own, read out of
  # its `fugaro doctor --json` (which answers under `project`), never defaulted.
  local dir=$work/regionown
  setup_case "$dir"
  python3 - "$dir/answers/doctor.json" <<'PY'
import json, sys
p = sys.argv[1]
doc = json.load(open(p))
doc["project"]["region"] = "europe-west1"
json.dump(doc, open(p, "w"))
PY
  run_check "$dir" -- --checkout "$dir/checkout"
  expect_status "$status" 0 "the installation's own region"
  expect_contains "$out" "FACT installation: PASS" "the installation's own region: the row"
  expect_contains "$out" "europe-west1" "the installation's own region: the region it read"
  expect_contains "$out" "$project" "the installation's own region: the project it read"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "--region=europe-west1" "the installation's own region: the job list is read there"
  expect_contains "$(cat "$dir/logs/curl.calls")" "https://europe-west1-aiplatform.googleapis.com/v1/projects/${project}/locations/europe-west1/" "the installation's own region: the Vertex location it probed"
  assert_launched "$dir" 2 "the installation's own region: the runs still go out"
}

test_no_region_anywhere_stops() {
  local dir=$work/noregion
  setup_case "$dir"
  python3 - "$dir/answers/doctor.json" <<'PY'
import json, sys
p = sys.argv[1]
doc = json.load(open(p))
doc["project"].pop("region", None)
json.dump(doc, open(p, "w"))
PY
  run_check "$dir" -- --checkout "$dir/checkout"
  expect_status "$status" 2 "an installation that names no region"
  expect_contains "$err" "named no region" "an installation that names no region: the refusal"
  assert_no_launch "$dir" "an installation that names no region"
}

test_a_checkout_with_no_origin_remote_stops() {
  # Without the origin remote there is no way to name the repository the runs
  # are charged to, and the cap row could read another repository's cap.
  local dir=$work/noorigin
  setup_case "$dir"
  git -C "$dir/checkout" remote remove origin
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 2 "a checkout with no origin remote"
  expect_contains "$err" "origin" "a checkout with no origin remote: the refusal"
  assert_no_launch "$dir" "a checkout with no origin remote"
}

test_a_checkout_that_is_not_a_work_tree_stops() {
  local dir=$work/notree
  setup_case "$dir"
  mkdir -p "$dir/plain"
  run_check "$dir" -- --checkout "$dir/plain" --gcp-project "$project" --region us-east5
  expect_status "$status" 2 "a checkout that is not a work tree"
  expect_contains "$err" "is not a git work tree" "a checkout that is not a work tree: the refusal"
  assert_no_launch "$dir" "a checkout that is not a work tree"
}

test_a_missing_checkout_is_refused() {
  local dir=$work/nocheckout
  setup_case "$dir"
  run_check "$dir" -- --gcp-project "$project" --region us-east5
  expect_status "$status" 2 "no --checkout"
  expect_contains "$err" "--checkout" "no --checkout: the refusal names it"
  assert_no_launch "$dir" "no --checkout"
}

test_an_observe_project_stops() {
  # X8 keeps the job's Vertex permission on an `observe` project until release
  # 2, so a refusal is not what this project would show: the check measures the
  # `enforce` case and says so instead of spending a run.
  local dir=$work/observe
  setup_case "$dir"
  python3 - "$dir/answers/budget.json" <<'PY'
import json, sys
p = sys.argv[1]
doc = json.load(open(p))
doc["mode"] = "observe"
json.dump(doc, open(p, "w"))
PY
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 2 "a project that does not enforce"
  expect_contains "$out" "FACT mode: FAIL" "a project that does not enforce: the row"
  expect_contains "$out" "enforce" "a project that does not enforce: the rule it rests on"
  assert_no_launch "$dir" "a project that does not enforce"
}

test_no_per_run_cap_stops() {
  local dir=$work/nocap
  setup_case "$dir"
  python3 - "$dir/answers/budget.json" <<'PY'
import json, sys
p = sys.argv[1]
doc = json.load(open(p))
for r in doc["repos"]:
    r.pop("per_run_cap_usd", None)
json.dump(doc, open(p, "w"))
PY
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 2 "a repository with no per-run cap"
  expect_contains "$out" "FACT mode: FAIL" "a repository with no per-run cap: the row"
  expect_contains "$out" "per-run cap" "a repository with no per-run cap: what it found"
  assert_no_launch "$dir" "a repository with no per-run cap"
}

test_the_cap_read_is_the_checkouts_own() {
  # The listing carries two capped repositories and the other one is first, so a
  # reader that takes the first capped row reports the wrong cap; the check names
  # the repository on the command line and says whose cap it read.
  local dir=$work/capown
  setup_case "$dir"
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 0 "the checkout's own cap"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "budget show --json --repo acme/chk882" "the checkout's own cap: the repository is named on the call"
  expect_contains "$out" "acme/chk882" "the checkout's own cap: the row names it"
  expect_contains "$out" '$10' "the checkout's own cap: its own figure, not the other repository's"
}

test_another_repositorys_cap_is_not_this_ones() {
  # The false PASS the scoping exists to stop: this repository has no cap while
  # another one in the listing does.
  local dir=$work/capother
  setup_case "$dir"
  python3 - "$dir/answers/budget.json" <<'PY'
import json, sys
p = sys.argv[1]
doc = json.load(open(p))
for r in doc["repos"]:
    if r.get("repo") == "acme/chk882":
        r.pop("per_run_cap_usd", None)
json.dump(doc, open(p, "w"))
PY
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 2 "another repository's cap"
  expect_contains "$out" "FACT mode: FAIL" "another repository's cap: the row"
  expect_contains "$out" "acme/chk882" "another repository's cap: the repository it named"
  assert_no_launch "$dir" "another repository's cap"
}

test_a_table_with_no_vertex_source_stops() {
  local dir=$work/novertex
  setup_case "$dir"
  cat >"$dir/answers/table.json" <<JSON
{"entries":[{"match":"${model}","chain":[{"kind":"anthropic","name":"project-key"}]}]}
JSON
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 2 "a table with no Vertex source"
  expect_contains "$out" "FACT mapped: FAIL" "a table with no Vertex source: the row"
  expect_contains "$out" "vertex" "a table with no Vertex source: what it looked for"
  assert_no_launch "$dir" "a table with no Vertex source"
}

test_a_model_the_table_does_not_match_stops() {
  local dir=$work/nomatch
  setup_case "$dir"
  run_check "$dir" -- $(default_args "$dir") --model gemini-nonexistent-9
  expect_status "$status" 2 "a model the table does not match"
  expect_contains "$err" "no entry matches" "a model the table does not match: the refusal"
  assert_no_launch "$dir" "a model the table does not match"
}

test_a_model_whose_chain_names_no_vertex_stops() {
  local dir=$work/novertexchain
  setup_case "$dir"
  python3 - "$dir/answers/table.json" <<'PY'
import json, sys
p = sys.argv[1]
doc = json.load(open(p))
doc["entries"].append({"match": "claude-haiku-4-5", "chain": [{"kind": "anthropic", "name": "project-key"}]})
json.dump(doc, open(p, "w"))
PY
  run_check "$dir" -- $(default_args "$dir") --model claude-haiku-4-5
  expect_status "$status" 2 "a chain that names no Vertex source"
  expect_contains "$out" "FACT chain: FAIL" "a chain that names no Vertex source: the row"
  expect_contains "$out" "no vertex source" "a chain that names no Vertex source: what it found"
  assert_no_launch "$dir" "a chain that names no Vertex source"
}

test_no_job_stops() {
  local dir=$work/nojob
  setup_case "$dir"
  printf '[]\n' >"$dir/answers/jobs.json"
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 2 "no Cloud Run job"
  expect_contains "$out" "FACT jobs: FAIL" "no Cloud Run job: the row"
  expect_contains "$out" "no Cloud Run job" "no Cloud Run job: what it found"
  assert_no_launch "$dir" "no Cloud Run job"
}

test_every_job_is_probed() {
  # X8's rule is "no job's account", so with no --job every job of the project
  # is read and every one of their accounts is probed.
  local dir=$work/twojobs
  setup_case "$dir"
  cat >"$dir/answers/jobs.json" <<JSON
[{"metadata":{"name":"${job}"},"spec":{"template":{"template":{"serviceAccountName":"${job_sa}"}}}},
 {"metadata":{"name":"${other_job}"},"spec":{"template":{"template":{"serviceAccountName":"${other_sa}"}}}}]
JSON
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 0 "two jobs"
  expect_contains "$out" "$other_sa" "two jobs: the second account is read"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "--impersonate-service-account=${other_sa}" "two jobs: the second account is probed"
  [ "$(grep -c '^url=' "$dir/logs/curl.calls" || true)" = 2 ] || fail "two jobs: expected two probes, got $(grep -c '^url=' "$dir/logs/curl.calls" || true)"
}

test_a_token_that_cannot_be_minted_is_not_measured() {
  local dir=$work/notoken
  setup_case "$dir"
  run_check "$dir" FAKE882_MODE=no-impersonation -- $(default_args "$dir")
  expect_status "$status" 2 "a token that cannot be minted"
  expect_contains "$out" "FACT token: FAIL" "a token that cannot be minted: the row"
  expect_contains "$out" "could not be minted" "a token that cannot be minted: what it found"
  expect_contains "$err" "could NOT measure" "a token that cannot be minted: the run says so"
  assert_no_launch "$dir" "a token that cannot be minted"
}

test_an_authorized_account_fails_the_refusal_row() {
  # The finding this half of the check exists for: a job's account reached
  # Vertex on an `enforce` project.
  local dir=$work/authorized
  setup_case "$dir"
  printf '200\n' >"$dir/answers/vertex.status"
  cat >"$dir/answers/vertex.body" <<'JSON'
{"content":[{"type":"text","text":"hello"}]}
JSON
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 1 "an account that can reach Vertex"
  expect_contains "$out" "FACT vertex-refused: FAIL" "an account that can reach Vertex: the row"
  expect_contains "$out" "was served" "an account that can reach Vertex: what it found"
  assert_launched "$dir" 0 "an account that can reach Vertex: no run is spent"
}

test_a_model_with_no_vertex_spelling_is_its_own_finding() {
  # 403 is the refusal and 2xx is a model served; a 404 is neither. It is what a
  # mapped model with no Vertex spelling at this location looks like (U5), so it
  # is a finding of its own — and the check goes ON, so the runs below still
  # measure the service half instead of U5 going unmeasured.
  local dir=$work/nospelling
  setup_case "$dir"
  printf '404\n' >"$dir/answers/vertex.status"
  cat >"$dir/answers/vertex.body" <<'JSON'
{"error":{"code":404,"message":"Publisher Model projects/x/locations/us-east5/publishers/anthropic/models/y was not found"}}
JSON
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 1 "a model Vertex does not serve"
  expect_contains "$out" "FACT vertex-refused: FAIL" "a model Vertex does not serve: the row"
  expect_contains "$out" "404" "a model Vertex does not serve: the status"
  expect_contains "$out" "U5" "a model Vertex does not serve: the rule it rests on"
  local row
  row=$(printf '%s\n' "$out" | grep -m1 '^FACT vertex-refused:' || true)
  expect_absent "$row" "was served" "a model Vertex does not serve: it is not called a model served"
  assert_launched "$dir" 2 "a model Vertex does not serve: the run half is still measured"
  expect_contains "$out" "FACT served: PASS" "a model Vertex does not serve: the served row is still read"
}

test_a_status_that_is_not_the_refusal_is_unverified() {
  # A 401 (or a 429, or a 5xx) says nothing either way: the refusal this check
  # measures is not established, so the row reads UNVERIFIED and the check goes
  # on to measure the service half rather than recording the account as served.
  local dir=$work/otherstatus
  setup_case "$dir"
  printf '401\n' >"$dir/answers/vertex.status"
  cat >"$dir/answers/vertex.body" <<'JSON'
{"error":{"code":401,"message":"Request had invalid authentication credentials"}}
JSON
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 3 "a status that is not the refusal"
  expect_contains "$out" "FACT vertex-refused: UNVERIFIED" "a status that is not the refusal: the row"
  expect_contains "$out" "401" "a status that is not the refusal: the status"
  local row
  row=$(printf '%s\n' "$out" | grep -m1 '^FACT vertex-refused:' || true)
  expect_absent "$row" "was served" "a status that is not the refusal: it is not called a model served"
  assert_launched "$dir" 2 "a status that is not the refusal: the run half is still measured"
}

test_a_granted_predefined_role_is_a_finding() {
  local dir=$work/granted
  setup_case "$dir"
  python3 - "$dir/answers/iam.json" "$job_sa" <<'PY'
import json, sys
p, sa = sys.argv[1], sys.argv[2]
doc = json.load(open(p))
doc["bindings"].append({"role": "roles/aiplatform.user", "members": ["serviceAccount:" + sa]})
json.dump(doc, open(p, "w"))
PY
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 1 "a granted predefined role"
  expect_contains "$out" "FACT grants: FAIL" "a granted predefined role: the row"
  expect_contains "$out" "roles/aiplatform.user" "a granted predefined role: the role it found"
  # A predefined role is asked for under the `roles/` prefix the policy spells it
  # with and with no --project: the fake refuses the prefixed id beside a --project
  # and reads a bare id as a custom role, so either mistake would show here as
  # UNVERIFIED rather than FAIL.
  local calls
  calls=$(cat "$dir/logs/gcloud.calls")
  expect_contains "$calls" "iam roles describe roles/aiplatform.user --format=json" "a granted predefined role: the prefixed ID gcloud is asked for"
  expect_absent "$calls" "describe aiplatform.user " "a granted predefined role: the bare ID is not handed to gcloud"
}

test_a_custom_role_with_the_permission_is_a_finding() {
  local dir=$work/custom
  setup_case "$dir"
  python3 - "$dir/answers/iam.json" "$job_sa" "$project" <<'PY'
import json, sys
p, sa, project = sys.argv[1], sys.argv[2], sys.argv[3]
doc = json.load(open(p))
doc["bindings"].append({"role": "projects/%s/roles/chk882vertex" % project, "members": ["serviceAccount:" + sa]})
json.dump(doc, open(p, "w"))
PY
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 1 "a custom role with the permission"
  expect_contains "$out" "FACT grants: FAIL" "a custom role with the permission: the row"
  expect_contains "$out" "projects/${project}/roles/chk882vertex" "a custom role with the permission: the role it found"
  # A project role is the bare ID plus the project the binding names.
  local calls
  calls=$(cat "$dir/logs/gcloud.calls")
  expect_contains "$calls" "iam roles describe chk882vertex --project=${project} --format=json" "a custom role with the permission: the ID and its own project are what gcloud is asked"
  expect_absent "$calls" "describe projects/" "a custom role with the permission: the policy's own spelling is not handed to gcloud"
}

test_a_refused_launch_is_not_measured() {
  local dir=$work/refused
  setup_case "$dir"
  run_check "$dir" FAKE882_MODE=launch-refused -- $(default_args "$dir")
  expect_status "$status" 2 "a launch that is refused"
  expect_contains "$out" "FACT runs: FAIL" "a launch that is refused: the row"
  expect_contains "$out" "could not be launched" "a launch that is refused: what it saw"
  expect_contains "$out" "--model" "a launch that is refused: the refusal's own fix is quoted"
  expect_contains "$err" "could NOT measure" "a launch that is refused: the run says so"
}

test_a_run_served_by_a_key_fails_the_served_row() {
  local dir=$work/key
  setup_case "$dir"
  python3 - "$dir/answers/record.json" <<'PY'
import json, sys
p = sys.argv[1]
doc = json.load(open(p))
for s in doc["stages"]:
    s["source"] = {"kind": "anthropic", "name": "project-key"}
json.dump(doc, open(p, "w"))
PY
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 1 "a run a key served"
  expect_contains "$out" "FACT served: FAIL" "a run a key served: the row"
  expect_contains "$out" "was served by anthropic" "a run a key served: what it found"
}

test_a_stage_that_was_not_priced_is_a_finding() {
  local dir=$work/unpriced
  setup_case "$dir"
  python3 - "$dir/answers/record.json" <<'PY'
import json, sys
p = sys.argv[1]
doc = json.load(open(p))
doc["cost"]["model_source"] = "notional"
for s in doc["stages"]:
    s["model_usd"] = 0.0
json.dump(doc, open(p, "w"))
PY
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 1 "a stage that was not priced"
  expect_contains "$out" "FACT priced: FAIL" "a stage that was not priced: the row"
  expect_contains "$out" "notional" "a stage that was not priced: what it found"
}

test_a_run_over_its_cap_is_a_finding() {
  local dir=$work/overcap
  setup_case "$dir"
  python3 - "$dir/answers/record.json" <<'PY'
import json, sys
p = sys.argv[1]
doc = json.load(open(p))
doc["cost"]["total_usd"] = 12.5
json.dump(doc, open(p, "w"))
PY
  run_check "$dir" -- $(default_args "$dir")
  expect_status "$status" 1 "a run over its cap"
  expect_contains "$out" "FACT settled: FAIL" "a run over its cap: the row"
  expect_contains "$out" "cap" "a run over its cap: what it found"
}

test_a_run_that_never_ends_is_not_measured() {
  local dir=$work/neverends
  setup_case "$dir"
  # The shape the real CLI answers for a run that has bootstrapped but not
  # ended: outcome "none", terminal false.
  cat >"$dir/answers/runs.json" <<JSON
[{"run":"chk882/20261122090000-b882-1","run_id":"20261122090000-b882-1","outcome":"none","terminal":false,"settled":false}]
JSON
  run_check "$dir" CHECK882_WAIT_SECONDS=1 CHECK882_POLL_SECONDS=1 -- $(default_args "$dir")
  expect_status "$status" 2 "a run that never ends"
  expect_contains "$out" "FACT runs: FAIL" "a run that never ends: the row"
  expect_contains "$out" "still not terminal" "a run that never ends: what it saw"
}

test_a_run_still_going_is_not_read_as_ended() {
  # A run in flight already answers an outcome — `none`, written at bootstrap —
  # so a check that stops on the first non-empty outcome reads the record while
  # the run is still spending and prints a false FAIL. The ended signal is
  # `terminal`, and the row it prints carries the run's own outcome.
  local dir=$work/inflight
  setup_case "$dir"
  cat >"$dir/answers/runs.inflight" <<JSON
[{"run":"chk882/20261122090000-b882-1","run_id":"20261122090000-b882-1","outcome":"none","terminal":false,"settled":false}]
JSON
  run_check "$dir" CHECK882_POLL_SECONDS=1 -- $(default_args "$dir")
  expect_status "$status" 0 "a run still going at the first poll"
  expect_contains "$out" "FACT runs: PASS" "a run still going at the first poll: the row"
  local row
  row=$(printf '%s\n' "$out" | grep -m1 '^FACT runs:' || true)
  expect_contains "$row" "(ready)" "a run still going at the first poll: the outcome it waited for"
  expect_absent "$row" "none" "a run still going at the first poll: the in-flight outcome is not reported as the run's"
  # The in-flight answer is the first of three polls: one that reads it, then one
  # per launched run that finds it terminal. Fewer means the first answer was
  # taken for the end.
  [ "$(grep -c . "$dir/logs/runs.calls")" -ge 3 ] ||
    fail "a run still going at the first poll: the check polled $(grep -c . "$dir/logs/runs.calls") time(s), so it read the in-flight answer as the end"
}

test_a_record_that_cannot_be_read_is_not_measured() {
  local dir=$work/norecord
  setup_case "$dir"
  run_check "$dir" FAKE882_MODE=record-absent -- $(default_args "$dir")
  expect_status "$status" 2 "a record that cannot be read"
  expect_contains "$out" "FACT runs: FAIL" "a record that cannot be read: the row"
  expect_contains "$out" "could not be read" "a record that cannot be read: what it saw"
  expect_contains "$err" "could NOT measure" "a record that cannot be read: the run says so"
}

test_an_unknown_argument_is_refused() {
  local dir=$work/arg
  setup_case "$dir"
  run_check "$dir" -- --wat
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --wat" "an unknown argument: the message"
}

test_kept_scratch_is_reported() {
  local dir=$work/kept
  setup_case "$dir"
  run_check "$dir" CHECK882_KEEP_SCRATCH=1 -- $(default_args "$dir")
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
}

# ------------------------------------------------------------------- the run
# Sharding, so the Go test can run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i, CHECK_TEST_ONLY=name runs that one alone.
test_index=0
t() { # t NAME
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
t test_the_block_never_carries_a_value
t test_an_agent_session_is_refused
t test_xtrace_refused
t test_belong_is_refused_by_name
t test_a_belong_installation_in_the_checkout_is_refused
t test_a_region_that_disagrees_with_the_installation_stops
t test_the_region_is_the_installations_own
t test_no_region_anywhere_stops
t test_a_checkout_with_no_origin_remote_stops
t test_a_checkout_that_is_not_a_work_tree_stops
t test_a_missing_checkout_is_refused
t test_an_observe_project_stops
t test_no_per_run_cap_stops
t test_the_cap_read_is_the_checkouts_own
t test_another_repositorys_cap_is_not_this_ones
t test_a_table_with_no_vertex_source_stops
t test_a_model_the_table_does_not_match_stops
t test_a_model_whose_chain_names_no_vertex_stops
t test_no_job_stops
t test_every_job_is_probed
t test_a_token_that_cannot_be_minted_is_not_measured
t test_an_authorized_account_fails_the_refusal_row
t test_a_model_with_no_vertex_spelling_is_its_own_finding
t test_a_status_that_is_not_the_refusal_is_unverified
t test_a_granted_predefined_role_is_a_finding
t test_a_custom_role_with_the_permission_is_a_finding
t test_a_refused_launch_is_not_measured
t test_a_run_served_by_a_key_fails_the_served_row
t test_a_stage_that_was_not_priced_is_a_finding
t test_a_run_over_its_cap_is_a_finding
t test_a_run_that_never_ends_is_not_measured
t test_a_run_still_going_is_not_read_as_ended
t test_a_record_that_cannot_be_read_is_not_measured
t test_an_unknown_argument_is_refused
t test_kept_scratch_is_reported

if [ "$failures" -ne 0 ]; then
  echo "check-882-vertex-capped_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-882-vertex-capped_test.sh: ok"
