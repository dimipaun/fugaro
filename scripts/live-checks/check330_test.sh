#!/usr/bin/env bash
# Tests for scripts/live-checks/check330.sh, against a fake `fugaro` and a fake
# `gcloud` on PATH: nothing here reaches the network, there is no cloud project,
# no bucket and no run, and no credential is read, made or printed.
#
# The fakes model the two things this check reads and nothing else:
#
#   * a fake `fugaro` that answers the three reads the check makes of the
#     installation — `doctor --json`, `project policy show --json` and
#     `runs ls --all --json --since 0` — out of the case's own files, and logs
#     every call with the directory it was made in, so a case can say the check
#     read the installation from the sandbox and nowhere else;
#   * a fake `gcloud` whose runs bucket is a directory of the case's own: the
#     `result.json` and `launch.json` of each run are files under
#     `state/objects/<slug>/<run-id>/`, the gateway's log lines of an execution
#     are `state/logs/<execution>.json` (an execution nobody wrote a log for
#     answers `[]`, exactly as a real read does), and `run jobs describe` answers
#     `state/jobs/<job>.json`. It records every call, and the log view or project
#     each log read was scoped to, so the check's own scope is a reading and not
#     an argument.
#
# A case's fixtures are written by `prepare DIR [FIX_...=value ...]`, so a case
# says in one line which single reading it bends — a coder whose lines carry no
# route, a review served by something that is not a Claude model, a review whose
# dollars are notional, a `route_by` that carries the whole run, a contrast whose
# review is not account-served, a review run launched on another variant, a
# review job carrying a secret — because a row that passes for the wrong reason
# is what this check exists to catch. The rows a reader must not mistake for a
# finding (a record that is not there, a log read that answered nothing, a
# reference that resolves to no run) end the run as one that could not tell, and
# each of them has a case too.
#
# Usage: bash scripts/live-checks/check330_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check330.sh
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK330_TEST_KEEP:-}" ]; then
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 1200)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_exactly() { # got want what
  [ "$1" = "$2" ] || fail "$3: expected '$2', got '$1'"
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^(FACT (aborted|unmeasured):|CHECK 330 RESULT END)' | sed 's/^/ ... last run: /')"
}

# The project, the region and the three runs a case is about. None of them names
# `belong`, which is the installation this check must never read — and neither
# does a case's own directory, because the guard is over the checkout's own path
# as well.
PROJECT=prj-sandbox-a
REGION=europe-west1
SLUG=api-demo
RUN_ID=20261114-130101-a101
CONTRAST_ID=20261114-140101-b202
REVIEW_ID=20261114-150101-c303
RUN_REF=$SLUG/$RUN_ID
CONTRAST_REF=$SLUG/$CONTRAST_ID
REVIEW_REF=$SLUG/$REVIEW_ID
REVIEWER=claude-opus-4-5
# The review-only variant the fixture's own policy derives, as the deployment
# computes it: internal/backend/gcp's ProjectJobName over (standard, github,
# anthropic-api-key--project), with the account segment internal/credsvc's
# ReviewJobName builds and the stem cut to 36 characters before the triple's
# 12-hex hash. The account segment is only "anthropic-a" in the name, which is
# why the check has to derive names rather than read them.
SENTINEL_JOB=fugaro-p-standard-github-anthropic-a-999810468a57
# The same triple's SHARED job: it differs from the variant above in the hash
# alone, so a check that compared readable stems would read one as the other.
SENTINEL_SHARED_JOB=fugaro-p-standard-github-anthropic-a-71c17459896a

# Every fact key the block carries, whatever the run measured.
BLOCK_KEYS=(project sandbox agent-session belong budget policy run contrast-run review-run
  log-coder log-review review-dollars route-by contrast
  review-launch review-env review-log
  restore)

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin" "$1/state/objects" "$1/state/logs" "$1/state/jobs" "$1/tmp" "$1/home" "$1/sandbox"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check330.sh's test: the three installation reads the check
# makes, out of the case's own files, and every call recorded with the
# directory it was made in.
set -euo pipefail
dir=${FAKE330_DIR:?FAKE330_DIR is not set}
printf 'fugaro %s (cwd %s)\n' "$*" "$PWD" >>"$dir/calls"
case "$*" in
"doctor --json") verb=doctor ;;
"project policy show --json") verb=policy ;;
"runs ls --all --json --since 0") verb=runs ;;
*)
  printf 'fake fugaro: unexpected call: %s\n' "$*" >&2
  exit 64
  ;;
esac
if [ "${FAKE330_FAIL:-}" = "$verb" ]; then
  printf 'ERROR: fugaro %s: %s\n' "$verb" "${FAKE330_FAIL_MESSAGE:-the command failed}" >&2
  exit 1
fi
if [ "${FAKE330_SLEEP:-}" = "$verb" ]; then
  sleep 30
fi
if [ "${FAKE330_JUNK:-}" = "$verb" ]; then
  printf 'not a document at all\n'
  exit 0
fi
cat "$dir/$verb.json"
FUGARO_EOF
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake gcloud for check330.sh's test: the runs bucket and the log of each
# execution are files of the case's own, and every call is recorded with the
# directory it was made in. The log read's own scope — the installation's view,
# or the project's default bucket — is recorded as well, and separately, so the
# case that passes --log-view reads it rather than inferring it from the call.
set -euo pipefail
dir=${FAKE330_DIR:?FAKE330_DIR is not set}
printf 'gcloud %s (cwd %s)\n' "$*" "$PWD" >>"$dir/calls"
argv=("$@")
unknown() {
  printf 'fake gcloud: unexpected call: %s\n' "$*" >&2
  exit 64
}
case "${argv[0]:-}" in
storage)
  [ "${argv[1]:-}" = cat ] || unknown "$@"
  obj=${argv[2]:-}
  case "$obj" in
  gs://*/runs/*) ;;
  *) unknown "$@" ;;
  esac
  key=${obj#gs://*/runs/}
  if [ "${FAKE330_STORAGE_FAIL:-}" = "$key" ] || [ "${FAKE330_STORAGE_FAIL:-}" = all ]; then
    printf 'ERROR: (gcloud.storage.cat) HTTPError 403: %s does not have storage.objects.get access to the Google Cloud Storage object.\n' \
      "${FAKE330_ACCOUNT:-chk330-owner@prj-sandbox-a.iam.gserviceaccount.com}" >&2
    exit 1
  fi
  file="$dir/objects/$key"
  if [ ! -f "$file" ]; then
    printf 'ERROR: (gcloud.storage.cat) HTTPError 404: No such object: %s\n' "$obj" >&2
    exit 1
  fi
  cat "$file"
  ;;
logging)
  [ "${argv[1]:-}" = read ] || unknown "$@"
  filter=""
  view=""
  for a in "$@"; do
    case "$a" in
    --resource-names=*) view=${a#--resource-names=} ;;
    --*) ;;
    *) filter=$a ;;
    esac
  done
  printf 'scope=%s\n' "${view:-none}" >>"$dir/log-scope"
  name=$(printf '%s' "$filter" | grep -o 'execution_name"="[^"]*"' | head -n 1 | cut -d'"' -f3)
  [ -n "$name" ] || unknown "$@"
  if [ "${FAKE330_LOG_FAIL:-}" = "$name" ] || [ "${FAKE330_LOG_FAIL:-}" = all ]; then
    printf 'ERROR: (gcloud.logging.read) PERMISSION_DENIED: the caller does not have permission\n' >&2
    exit 1
  fi
  if [ "${FAKE330_LOG_SLEEP:-}" = "$name" ]; then
    sleep 30
  fi
  # An execution nobody wrote a log for answers an empty list, exactly as a real
  # read does: the check has to tell that from a log read that failed.
  if [ -f "$dir/logs/$name.json" ]; then
    cat "$dir/logs/$name.json"
  else
    printf '[]\n'
  fi
  ;;
run)
  [ "${argv[1]:-}" = jobs ] && [ "${argv[2]:-}" = describe ] || unknown "$@"
  job=${argv[3]:-}
  [ -n "$job" ] || unknown "$@"
  printf 'job=%s\n' "$job" >>"$dir/jobs-described"
  if [ -n "${FAKE330_JOB_FAIL:-}" ]; then
    printf 'ERROR: (gcloud.run.jobs.describe) NOT_FOUND: job %s was not found\n' "$job" >&2
    exit 1
  fi
  if [ -f "$dir/jobs/$job.json" ]; then
    cat "$dir/jobs/$job.json"
  else
    cat "$dir/jobs/default.json"
  fi
  ;;
*)
  unknown "$@"
  ;;
esac
GCLOUD_EOF
  chmod 755 "$1/bin/fugaro" "$1/bin/gcloud"
}

# ---------------------------------------------------------------- the fixtures
# write_fixtures DIR [FIX_...=value ...] writes the installation's three
# documents, the runs bucket, the gateway's logs and the review job for the case
# the FIX_ knobs name. Every knob has a value that makes the check PASS.
write_fixtures() { # write_fixtures DIR [FIX_...=value ...]
  local d=$1
  shift
  env "$@" FIX_DIR="$d" python3 - <<'PY'
import json
import os

env = os.environ
d = os.path.join(env["FIX_DIR"], "state")


def s(name, default=""):
    return env.get(name, default)


def flag(name):
    return s(name).lower() in ("1", "yes", "true")


def given(name):
    return name in env


def number(name, default):
    """The figure a knob names, kept as the JSON number it is, so a figure the
    record writes as an integer stays one."""
    text = s(name, default)
    return float(text) if "." in text else int(text)


project = s("FIX_PROJECT", "prj-sandbox-a")
region = "europe-west1"
slug = "api-demo"
run_id = "20261114-130101-a101"
contrast_id = "20261114-140101-b202"
review_id = "20261114-150101-c303"
coder = "deepseek/deepseek-v4.1-flash"
reviewer = "claude-opus-4-5"
job = "fugaro-%s-standard" % slug


def execution(run):
    return "projects/%s/locations/%s/jobs/%s/executions/%s" % (project, region, job, run)


def write(rel, doc):
    path = os.path.join(d, rel)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(doc, fh)


def stage(name, model, usd=None, notional=None, credential=None, served_by=None, calls=None):
    st = {"name": name}
    if model is not None:
        st["model"] = model
    if usd is not None:
        st["model_usd"] = usd
    if notional is not None:
        st["model_notional_usd"] = notional
    if credential is not None:
        st["credential"] = credential
    if served_by is not None:
        st["served_by"] = served_by
    if calls is not None:
        st["calls"] = calls
    st["duration_s"] = 12.5
    return st


# ------------------------------------------------------------ the installation
write("doctor.json", {
    "project": {"gcp_project": project, "region": region, "name": "sandbox"},
    "fugaro_yaml": {"path": "/sandbox/fugaro.yaml", "valid": True},
})

if flag("FIX_NO_POLICY"):
    policy = None
else:
    # The published policy names the axis's sentinel and one stage account, in
    # all three places a non-account value may sit: the project's default, a
    # stage's own declaration, and a profile's own list.
    # The profile carries its provider, as every published profile does
    # (projectpolicy's provider-required rule): a project job's name is derived
    # over (profile, provider, account), so the document has to have all three.
    policy = {
        "budget": {"mode": s("FIX_BUDGET_MODE", "enforce")},
        "default_claude_account": s("FIX_DEFAULT_ACCOUNT", "anthropic-api-key--project"),
        "claude_account_stages": {"review": s("FIX_STAGE_ACCOUNT", "provider-key--project")},
        "profiles": {"standard": {
            "provider": "github",
            "claude_accounts": [s("FIX_PROFILE_ACCOUNT", "anthropic-api-key--project")],
        }},
    }
write("policy.json", {"project": project, "policy": policy})

rows = []
if not flag("FIX_ROWS_EMPTY"):
    rows.append({"run_id": run_id, "run": "%s/%s" % (slug, run_id), "status": "done",
                 "execution": s("FIX_RUN_EXEC", execution(run_id)),
                 "repo": "acme/sandbox", "cost": 1.934})
    if not flag("FIX_NO_CONTRAST_ROW"):
        rows.append({"run_id": contrast_id, "run": "%s/%s" % (slug, contrast_id), "status": "done",
                     "execution": execution(contrast_id), "repo": "acme/sandbox", "cost": 1.5})
    if not flag("FIX_NO_REVIEW_ROW"):
        rows.append({"run_id": review_id, "run": "%s/%s" % (slug, review_id), "status": "done",
                     "execution": execution(review_id), "repo": "acme/sandbox", "cost": 0.4})
write("runs.json", {"runs": rows})

# -------------------------------------------------------------- a run's record
own = 0.7
stages = [
    stage("plan", coder, 0.2, served_by="api-key", calls=2),
    stage("implement", coder, 0.3, served_by="api-key", calls=3),
    stage("fix", coder, 0.1, served_by="api-key", calls=1),
    stage("review_first", reviewer, 0.1, served_by="api-key", calls=1),
]
if flag("FIX_BOUNCE"):
    # A run that fixed a finding and was reviewed again: the bounce loop writes
    # the stage's own name once per round (internal/runner/bounce_test.go records
    # implement, review_first, fix, review, fix, review_first, review), so `fix`
    # and `review_first` appear twice and the run's own spend is 0.9.
    stages.append(stage("fix", coder, 0.1, served_by="api-key", calls=1))
    stages.append(stage("review_first", reviewer, 0.1, served_by="api-key", calls=1))
    own = 0.9
if not flag("FIX_NO_REVIEW_STAGE"):
    stages.append(stage(
        "review", reviewer,
        usd=number("FIX_REVIEW_USD", "1.234"),
        notional=(number("FIX_REVIEW_NOTIONAL", "0") if given("FIX_REVIEW_NOTIONAL") else None),
        credential=(s("FIX_REVIEW_CREDENTIAL") or None),
        served_by=s("FIX_REVIEW_SERVED_BY", "api-key"),
        calls=2,
    ))
review_usd = 0.0 if flag("FIX_NO_REVIEW_STAGE") else number("FIX_REVIEW_USD", "1.234")
total = own + review_usd

cost = {"model_usd": number("FIX_TOTAL_USD", str(total))}
if s("FIX_ROUTE_BY") == "none":
    pass
elif s("FIX_ROUTE_BY") == "whole":
    cost["route_by"] = {"provider-x": total}
elif given("FIX_ROUTE_BY"):
    cost["route_by"] = json.loads(s("FIX_ROUTE_BY"))
else:
    cost["route_by"] = {"provider-x": own}

write("objects/%s/%s/result.json" % (slug, run_id), {
    "run_id": run_id, "status": "done", "outcome": "ready",
    "cost": cost, "stages": stages,
})
write("objects/%s/%s/launch.json" % (slug, run_id),
      {"run_id": run_id, "job": job, "execution": execution(run_id), "backend": "gcp"})

# ------------------------------------------------------------ the contrast run
if not flag("FIX_NO_CONTRAST_RECORD"):
    contrast_stages = [
        stage("plan", coder, 0.2, served_by="api-key", calls=2),
        stage("implement", coder, 0.3, served_by="api-key", calls=3),
    ]
    if not flag("FIX_CONTRAST_NO_REVIEW"):
        contrast_usd = number("FIX_CONTRAST_USD", "1.0")
        contrast_stages.append(stage(
            "review", reviewer,
            usd=contrast_usd,
            notional=(number("FIX_CONTRAST_NOTIONAL", "0") if given("FIX_CONTRAST_NOTIONAL") else contrast_usd),
            credential=s("FIX_CONTRAST_CREDENTIAL", "oauth"),
            served_by="oauth",
            calls=2,
        ))
    if flag("FIX_CONTRAST_NO_STAGES"):
        contrast_stages = []
    write("objects/%s/%s/result.json" % (slug, contrast_id), {
        "run_id": contrast_id, "status": "done", "outcome": "ready",
        "cost": {"model_usd": 2.0, "route_by": {"provider-x": 0.5}},
        "stages": contrast_stages,
    })
    write("objects/%s/%s/launch.json" % (slug, contrast_id),
          {"run_id": contrast_id, "job": job, "execution": execution(contrast_id), "backend": "gcp"})

# ------------------------------------------------------------ the review run (B)
# The review-only variants this fixture's profile derives, written out as the
# deployment computes them (gcp.ProjectJobName over the triple, with
# credsvc.ReviewJobName's account) rather than recomputed here: the check derives
# these names itself, so a fixture that derived them the same way would agree with
# the check's own derivation even if both were wrong.
DERIVED_REVIEW_JOB = {
    "anthropic-api-key--project": "fugaro-p-standard-github-anthropic-a-999810468a57",
    "provider-key--project": "fugaro-p-standard-github-provider-ke-d9cfd230e7ef",
    "team-b": "fugaro-p-standard-github-team-b-revi-e6fecafe504a",
}
profile_account = s("FIX_PROFILE_ACCOUNT", "anthropic-api-key--project")
review_job = s("FIX_REVIEW_JOB", DERIVED_REVIEW_JOB[profile_account])
if not flag("FIX_NO_REVIEW_RECORD"):
    write("objects/%s/%s/result.json" % (slug, review_id), {
        "run_id": review_id, "status": "done", "outcome": "ready",
        "cost": {"model_usd": 0.4, "route_by": {}},
        "stages": [stage("review", reviewer, 0.4, served_by="api-key", calls=2)],
    })
    write("objects/%s/%s/launch.json" % (slug, review_id), {
        "run_id": review_id, "job": review_job,
        "execution": execution(review_id), "backend": "gcp",
    })


# ----------------------------------------------------------------- the log call
def calls(entries):
    out = []
    for stage_name, model, serving, route, micros in entries:
        payload = {"message": "model call", "stage": stage_name, "model": model,
                   "serving_model": serving, "charged_micros": micros, "settled": True,
                   "status": "ok", "session_id": "s-1", "agent_id": "a-1"}
        if route:
            payload["route"] = route
        out.append({"jsonPayload": payload, "severity": "INFO",
                    "resource": {"type": "cloud_run_job"}})
    return out


if flag("FIX_LOG_EMPTY"):
    run_log = []
else:
    coder_route = s("FIX_LOG_CODER_ROUTE", "provider-x")
    run_log = []
    for name in ("plan", "implement", "fix", "review_first"):
        run_log.append((name, coder, coder, coder_route, 120000))
    if not flag("FIX_LOG_NO_REVIEW"):
        review_route = s("FIX_LOG_REVIEW_ROUTE", "")
        serving = s("FIX_LOG_REVIEW_SERVING", reviewer)
        run_log.append(("review", reviewer, serving, review_route, 480000))
        run_log.append(("adversarial", reviewer, serving, review_route, 96000))
write("logs/%s.json" % run_id, calls(run_log))

if flag("FIX_REVIEW_LOG_EMPTY"):
    write("logs/%s.json" % review_id, [])
else:
    write("logs/%s.json" % review_id,
          calls([("review", reviewer, s("FIX_REVIEW_LOG_SERVING", reviewer),
                  s("FIX_REVIEW_LOG_ROUTE", ""), 400000)]))

# ------------------------------------------------------------------ the job (B)
envs = [{"name": "FUGARO_CREDENTIALS_SERVICE", "value": "https://creds.example"},
        {"name": "FUGARO_RUN_ID", "value": review_id}]
if s("FIX_JOB_SECRET"):
    envs.append({"name": s("FIX_JOB_SECRET"),
                 "valueFrom": {"secretKeyRef": {"name": "repo-token", "key": "latest"}}})
volumes = []
mounts = []
if flag("FIX_JOB_SECRET_VOLUME"):
    volumes.append({"name": "repo-token", "secret": {"secretName": "repo-token"}})
    mounts.append({"name": "repo-token", "mountPath": "/secrets"})
write("jobs/%s.json" % review_job, {
    "metadata": {"name": review_job},
    "spec": {"template": {"template": {
        "containers": [{"image": "europe-west1-docker.pkg.dev/p/fugaro/runner:1", "env": envs,
                        "volumeMounts": mounts}],
        "volumes": volumes,
    }}},
})
PY
}

# ---------------------------------------------------------------- the run harness
TIMEOUT_BIN=$(command -v timeout || true)
CASE_TIMEOUT=${CASE_TIMEOUT:-60}
bounded() { # bounded CMD...
  local rc=0
  if [ -n "$TIMEOUT_BIN" ]; then
    "$TIMEOUT_BIN" "$CASE_TIMEOUT" "$@" || rc=$?
    return "$rc"
  fi
  "$@" &
  local pid=$!
  ( sleep "$CASE_TIMEOUT"; kill -TERM "$pid" 2>/dev/null ) &
  local watcher=$!
  wait "$pid" || rc=$?
  kill "$watcher" 2>/dev/null || true
  wait "$watcher" 2>/dev/null || true
  return "$rc"
}

prepare() { # prepare DIR [FIX_...=value ...]
  dir=$1
  shift
  rm -rf "$dir"
  write_fakes "$dir"
  : >"$dir/state/calls"
  : >"$dir/state/log-scope"
  : >"$dir/state/jobs-described"
  write_fixtures "$dir" "$@"
  status=0
  out=""
  err=""
  scratch=""
  RUN330_ARGS=()
  RUN330_ENV=()
}

# run330 [ARGS...] runs the check on the case at hand. RUN330_ARGS adds arguments
# and RUN330_ENV names environment variables (NAME=value) for the run's fakes.
run330() {
  status=0
  # The agent markers are dropped from this test's own environment: the check
  # refuses a coding agent's session by name, and the suite that runs this file
  # may itself be started from one. The case that measures that refusal sets the
  # marker again, and a variable given as an argument wins over `env -u`.
  bounded env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    TMPDIR="$dir/tmp" HOME="$dir/home" FAKE330_DIR="$dir/state" \
    CHECK330_TIMEOUT="${CHECK_TIMEOUT:-10}" PATH="$dir/bin:$PATH" \
    ${RUN330_ENV[@]+"${RUN330_ENV[@]}"} \
    bash "$script" ${RUN330_ARGS[@]+"${RUN330_ARGS[@]}"} "$@" >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  RUN330_ARGS=()
  RUN330_ENV=()
}
run330_default() { run330 --sandbox "$dir/sandbox" --run "$RUN_REF" --contrast "$CONTRAST_REF" "$@"; }
run330_both() { run330 --sandbox "$dir/sandbox" --run "$RUN_REF" --contrast "$CONTRAST_REF" --review "$REVIEW_REF" "$@"; }

# ------------------------------------------------------------------ assertions
fact_line() { # key
  printf '%s\n' "$out" | grep -m1 "^FACT $1:" || true
}

expect_block_keys() { # what
  local key
  expect_contains "$out" "CHECK 330 RESULT" "$1: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$out" "FACT $key:" "$1: the $key row"
  done
  expect_contains "$out" "CHECK 330 RESULT END (exit " "$1: the end line"
}

expect_no_calls() { # what
  if [ -s "$dir/state/calls" ]; then
    fail "$1: a tool was called: $(tr '\n' ';' <"$dir/state/calls")"
  fi
}

expect_gcloud_never_called() { # what
  if grep -q '^gcloud ' "$dir/state/calls"; then
    fail "$1: gcloud was called: $(grep '^gcloud ' "$dir/state/calls" | tr '\n' ';')"
  fi
}

expect_nothing_read_for() { # ref what — a run reference nothing was read for
  if grep -q "$1" "$dir/state/calls"; then
    fail "$2: something was read for '$1': $(grep "$1" "$dir/state/calls" | tr '\n' ';')"
  fi
}

expect_scratch_gone() { # what — the run announced its scratch and removed it
  if [ -z "$scratch" ]; then
    fail "$1: the check did not say where its scratch directory is"
    return 0
  fi
  [ -d "$scratch" ] && fail "$1: the scratch directory $scratch survived the run"
  [ -z "$(find "$dir/tmp" -mindepth 1 2>/dev/null)" ] || fail "$1: files were left in TMPDIR: $(find "$dir/tmp" -mindepth 1)"
  return 0
}

expect_no_scratch() { # what — the run stopped before it made one
  [ -z "$(find "$dir/tmp" -mindepth 1 2>/dev/null)" ] || fail "$1: files were left in TMPDIR: $(find "$dir/tmp" -mindepth 1)"
  return 0
}

expect_read_in_sandbox() { # what
  expect_contains "$(cat "$dir/state/calls")" "cwd $dir/sandbox" "$1: the installation is read from the sandbox checkout"
}

expect_log_scopes() { # want what — every log read's own scope, deduplicated
  expect_exactly "$(sort -u "$dir/state/log-scope" | tr '\n' ' ' | sed 's/ $//')" "$1" "$2"
}

# ------------------------------------------------------------------- the tests
# The pair as the plan's §5 wants it: the coder's calls carry the provider's
# route and the review's carry none, the review's dollars are the gateway's own,
# the split is the coder's stages', and the contrast's review is account-served.
test_the_pair_reads_as_pass() {
  prepare "$work/pass"
  run330_default
  expect_status "$status" 0 "the passing pair"
  expect_block_keys "the passing pair"
  expect_contains "$(fact_line log-coder)" "PASS" "the passing pair: the coder reached the provider"
  expect_contains "$(fact_line log-coder)" "on route provider-x" "the passing pair: and the route is named"
  expect_contains "$(fact_line log-coder)" "plan,implement,fix,review_first" "the passing pair: and the stages it read"
  expect_contains "$(fact_line log-review)" "PASS" "the passing pair: the review reached Anthropic"
  expect_contains "$(fact_line log-review)" "$REVIEWER" "the passing pair: and the model that served it is named"
  expect_contains "$(fact_line review-dollars)" "PASS" "the passing pair: the review's dollars are real"
  expect_contains "$(fact_line route-by)" "PASS" "the passing pair: the split is the coder's stages'"
  expect_contains "$(fact_line contrast)" "PASS" "the passing pair: the contrast is account-served"
  expect_contains "$(fact_line contrast)" "credential=oauth" "the passing pair: and the row names the credential"
  expect_contains "$(fact_line restore)" "nothing of this check's was created" "the passing pair: the Restore says what it is"
  expect_contains "$out" "CHECK 330 RESULT END (exit 0)" "the passing pair: the end line"
  expect_contains "$(fact_line run)" "PASS" "the passing pair: the run is resolved"
  # Slice B was not named, so its rows say so and gate nothing.
  expect_contains "$(fact_line review-launch)" "INFO" "the passing pair: slice B is not read"
  expect_contains "$(fact_line review-run)" "no --review" "the passing pair: and the review run says why it is not there"
  # The steps that stay the owner's are printed rather than faked, and slice C is
  # named as the one that is not run.
  expect_contains "$err" "THE STEPS THAT STAY YOURS" "the passing pair: the owner's own steps"
  expect_contains "$err" "Slice C (the OpenRouter path) is NOT run" "the passing pair: and that slice C is not run"
  expect_contains "$(fact_line project)" "gs://fugaro-runs-$PROJECT/runs/" "the passing pair: the bucket is the project's"
  expect_read_in_sandbox "the passing pair"
  expect_scratch_gone "the passing pair"
}

# A run whose own calls reached Anthropic is not the run slice A measures: the
# premise is a coder on the project's provider route, and a check that read this
# as a pass would answer the api-key review from the wrong run.
test_a_coder_with_no_route_is_a_fail() {
  prepare "$work/coder-anthropic" FIX_LOG_CODER_ROUTE=
  run330_default
  expect_status "$status" 1 "a coder with no route"
  expect_contains "$(fact_line log-coder)" "FAIL" "a coder with no route: the row fails"
  expect_contains "$(fact_line log-coder)" "no call of the run's own stages" "a coder with no route: and says what it did not find"
  expect_contains "$(fact_line log-review)" "PASS" "a coder with no route: the review half is still read"
  expect_absent "$out" "FACT unmeasured:" "a coder with no route: nothing is unread"
  expect_scratch_gone "a coder with no route"
}

# The review's calls DO carry a route: the stage was served by the provider, so
# nothing about the api-key path is measured.
test_a_review_on_a_route_is_a_fail() {
  prepare "$work/review-route" FIX_LOG_REVIEW_ROUTE=provider-x
  run330_default
  expect_status "$status" 1 "a review on a route"
  expect_contains "$(fact_line log-review)" "FAIL" "a review on a route: the row fails"
  expect_contains "$(fact_line log-coder)" "PASS" "a review on a route: the coder's half still holds"
  expect_scratch_gone "a review on a route"
}

# A route-less call of the review class served by something that is not a Claude
# model is not the Anthropic half either: the row is about Anthropic answering.
test_a_review_served_by_a_non_claude_model_is_a_fail() {
  prepare "$work/review-model" FIX_LOG_REVIEW_SERVING=gpt-5.1
  run330_default
  expect_status "$status" 1 "a review served by a non-Claude model"
  expect_contains "$(fact_line log-review)" "FAIL" "a review served by a non-Claude model: the row fails"
  expect_contains "$(fact_line log-review)" "served as gpt-5.1" "a review served by a non-Claude model: and names what served it"
  expect_scratch_gone "a review served by a non-Claude model"
}

# A review stage that wrote a notional figure spent dollars the account paid, so
# the api-key path was not what ran: this is the reading that tells the two
# halves of the pair apart on the record alone.
test_a_notional_review_dollar_is_a_fail() {
  prepare "$work/notional" FIX_REVIEW_NOTIONAL=1.234
  run330_default
  expect_status "$status" 1 "a notional review dollar"
  expect_contains "$(fact_line review-dollars)" "FAIL" "a notional review dollar: the row fails"
  expect_contains "$(fact_line review-dollars)" "model_notional_usd=1.234" "a notional review dollar: the row shows the figure it read"
  expect_scratch_gone "a notional review dollar"
}

# A review stage whose model_usd is zero spent nothing the gateway priced, which
# is not a real figure either.
test_a_zero_review_dollar_is_a_fail() {
  prepare "$work/zero-dollar" FIX_REVIEW_USD=0
  run330_default
  expect_status "$status" 1 "a zero review dollar"
  expect_contains "$(fact_line review-dollars)" "FAIL" "a zero review dollar: the row fails"
  expect_contains "$(fact_line review-dollars)" "model_usd=0" "a zero review dollar: the row shows what it read"
  expect_scratch_gone "a zero review dollar"
}

# A split that is not the run's own stages' spend: the review class's dollars are
# inside the provider's split, which is the one thing this reading is for.
test_a_split_that_is_not_the_own_stages_spend_is_a_fail() {
  prepare "$work/split-whole" FIX_ROUTE_BY=whole
  run330_default
  expect_status "$status" 1 "a split carrying the whole run"
  expect_contains "$(fact_line route-by)" "FAIL" "a split carrying the whole run: the row fails"
  expect_contains "$(fact_line route-by)" "not the run's own stages' spend" "a split carrying the whole run: and says why"
  expect_scratch_gone "a split carrying the whole run"
}

# The split and the run's whole spend being the same money is the other way the
# review's dollars end up inside the provider's split.
test_a_split_equal_to_the_whole_run_is_a_fail() {
  prepare "$work/split-same" FIX_TOTAL_USD=0.7
  run330_default
  expect_status "$status" 1 "a split equal to the whole run"
  expect_contains "$(fact_line route-by)" "FAIL" "a split equal to the whole run: the row fails"
  expect_contains "$(fact_line route-by)" "the two are the same money" "a split equal to the whole run: and says why"
  expect_scratch_gone "a split equal to the whole run"
}

# A record with no route_by at all: the run's own calls carried a route in the
# log, so the split that says who paid for what was never written.
test_a_missing_split_is_a_fail() {
  prepare "$work/split-missing" FIX_ROUTE_BY=none
  run330_default
  expect_status "$status" 1 "a missing split"
  expect_contains "$(fact_line route-by)" "FAIL" "a missing split: the row fails"
  expect_contains "$(fact_line route-by)" "no route_by at all" "a missing split: and names what is missing"
  expect_scratch_gone "a missing split"
}

# A run that fixed a finding runs `fix` and `review_first` once per round, and
# the split carries a residual ("unattributed") that belongs to no route: a sum
# that took only the first row of each stage name, and a total that counted the
# residual as a provider's share, both read this healthy run as a failed
# criterion — which is the shape the register tells the owner to launch.
test_a_bouncing_run_with_a_residual_reads_as_its_own_stages_spend() {
  prepare "$work/bounce" FIX_BOUNCE=1 FIX_ROUTE_BY='{"provider-x": 0.9, "unattributed": 0.05}'
  run330_default
  expect_status "$status" 0 "a bouncing run with a residual"
  expect_contains "$(fact_line route-by)" "PASS" "a bouncing run with a residual: the split is the run's own stages' spend"
  expect_contains "$(fact_line route-by)" '0.900000' "a bouncing run with a residual: every round is summed"
  expect_contains "$(fact_line route-by)" 'of 0.95' "a bouncing run with a residual: and the residual is in the row"
  expect_scratch_gone "a bouncing run with a residual"
}

# The residual is not a licence to skip the reading: a split whose routes carry
# more than the run's own stages' spend is still the review's dollars in it.
test_a_bouncing_run_whose_routes_carry_too_much_is_a_fail() {
  prepare "$work/bounce-fat" FIX_BOUNCE=1 FIX_ROUTE_BY='{"provider-x": 0.9, "provider-y": 0.4}'
  run330_default
  expect_status "$status" 1 "a bouncing run whose routes carry too much"
  expect_contains "$(fact_line route-by)" "FAIL" "a bouncing run whose routes carry too much: the row fails"
  expect_contains "$(fact_line route-by)" "1.3" "a bouncing run whose routes carry too much: and sums every route"
  expect_scratch_gone "a bouncing run whose routes carry too much"
}

# The contrast is the other half of the pair: an account-served review reads
# credential=oauth with a notional figure EQUAL to the real one, and a second run
# whose review was not account-served measures nothing about the contrast.
test_a_contrast_that_is_not_account_served_is_a_fail() {
  prepare "$work/contrast-plain" FIX_CONTRAST_CREDENTIAL=api-key
  run330_default
  expect_status "$status" 1 "a contrast that is not account-served"
  expect_contains "$(fact_line contrast)" "FAIL" "a contrast that is not account-served: the row fails"
  expect_contains "$(fact_line contrast)" "credential=api-key" "a contrast that is not account-served: the row shows what it read"
  expect_contains "$(fact_line review-dollars)" "PASS" "a contrast that is not account-served: slice A's own half still holds"
  expect_scratch_gone "a contrast that is not account-served"
}

# The two figures have to be the same money: a contrast whose review was priced
# partly by the gateway is not the account paying for it.
test_a_contrast_whose_figures_disagree_is_a_fail() {
  prepare "$work/contrast-figures" FIX_CONTRAST_USD=1.0 FIX_CONTRAST_NOTIONAL=0.5
  run330_default
  expect_status "$status" 1 "a contrast whose figures disagree"
  expect_contains "$(fact_line contrast)" "FAIL" "a contrast whose figures disagree: the row fails"
  expect_contains "$(fact_line contrast)" "model_usd=1.0" "a contrast whose figures disagree: the row shows both"
  expect_contains "$(fact_line contrast)" "model_notional_usd=0.5" "a contrast whose figures disagree: and the notional one"
  expect_scratch_gone "a contrast whose figures disagree"
}

# The contrast's own record unreadable is not a finding about the pair either.
test_an_unreadable_contrast_is_unmeasured() {
  prepare "$work/contrast-unread"
  RUN330_ENV=(FAKE330_STORAGE_FAIL="$SLUG/$CONTRAST_ID/result.json")
  run330_default
  expect_status "$status" 2 "an unreadable contrast"
  expect_contains "$(fact_line contrast)" "could not be read" "an unreadable contrast: the row says the read did not answer"
  expect_contains "$(fact_line unmeasured)" "contrast" "an unreadable contrast: the unmeasured row names it"
  expect_contains "$(fact_line review-dollars)" "PASS" "an unreadable contrast: slice A's own half is still read"
  expect_scratch_gone "an unreadable contrast"
}

# Slice B, on the run the plan's launch makes: the sentinel's own review variant,
# an environment holding no secret, and the run's calls route-less to Anthropic.
test_slice_b_reads_the_sentinel_variant() {
  prepare "$work/slice-b"
  run330_both
  expect_status "$status" 0 "slice B"
  expect_block_keys "slice B"
  expect_contains "$(fact_line review-launch)" "PASS" "slice B: the launch is the sentinel's variant"
  expect_contains "$(fact_line review-launch)" "$SENTINEL_JOB" "slice B: and the row names the job"
  expect_contains "$(fact_line review-launch)" "anthropic-api-key--project" "slice B: and the sentinel value"
  expect_contains "$(fact_line review-env)" "PASS" "slice B: the job holds no credential"
  expect_contains "$(fact_line review-env)" "none of them secret-sourced" "slice B: and says what it read"
  expect_contains "$(fact_line review-log)" "PASS" "slice B: the calls went through the gateway to Anthropic"
  expect_contains "$(fact_line review-log)" "$REVIEWER" "slice B: and names the model that served them"
  expect_contains "$(cat "$dir/state/jobs-described")" "job=$SENTINEL_JOB" "slice B: the job described is the one the run was launched on"
  expect_contains "$out" "CHECK 330 RESULT END (exit 0)" "slice B: the end line"
  expect_scratch_gone "slice B"
}

# A run launched on the OTHER non-account value is slice C's, not this slice's —
# and it is read, not mistaken for a failure of the Anthropic-key path. The
# policy's own profile is what derives the job: the variant's name comes off the
# account the profile lists, so the case spells the profile's account and the
# fixture derives the name it has.
test_slice_b_on_the_provider_key_variant_says_which_one_it_read() {
  prepare "$work/slice-b-provider" FIX_PROFILE_ACCOUNT=provider-key--project
  run330_both
  expect_status "$status" 0 "the provider-key variant"
  expect_contains "$(fact_line review-launch)" "PASS" "the provider-key variant: the row passes"
  expect_contains "$(fact_line review-launch)" "provider-key--project" "the provider-key variant: and names which value it read"
  expect_contains "$(fact_line review-launch)" "slice C" "the provider-key variant: and says which slice it belongs to"
  expect_contains "$(fact_line review-launch)" "fugaro-p-standard-github-provider-ke-d9cfd230e7ef" "the provider-key variant: the row reads the derived name, not the account's spelling"
  expect_scratch_gone "the provider-key variant"
}

# A review launched on an account's own variant is not the cloud Anthropic-key
# path: that repository's review is served by the account.
test_a_review_on_an_account_variant_is_a_fail() {
  prepare "$work/slice-b-account" FIX_PROFILE_ACCOUNT=team-b
  run330_both
  expect_status "$status" 1 "a review on an account's variant"
  expect_contains "$(fact_line review-launch)" "FAIL" "a review on an account's variant: the row fails"
  expect_contains "$(fact_line review-launch)" "a Claude account's job" "a review on an account's variant: and says what it is"
  expect_contains "$(fact_line review-launch)" "team-b" "a review on an account's variant: and names the account the policy derives it for"
  expect_scratch_gone "a review on an account's variant"
}

# The shared job of the sentinel's own triple: same profile, same provider, same
# account, and a name whose readable part is identical to the variant's to the
# character — only the hash differs. A check that matched the readable stem would
# read the job that mounts the App key as the review-only variant.
test_a_review_on_the_shared_job_of_the_sentinel_triple_is_a_fail() {
  prepare "$work/slice-b-shared" FIX_REVIEW_JOB="$SENTINEL_SHARED_JOB"
  run330_both
  expect_status "$status" 1 "a review on the shared job"
  expect_contains "$(fact_line review-launch)" "FAIL" "a review on the shared job: the row fails"
  expect_contains "$(fact_line review-launch)" "SHARED job" "a review on the shared job: and says which job it is"
  expect_contains "$(fact_line review-launch)" "anthropic-api-key--project" "a review on the shared job: naming the account the policy derives it over"
  expect_scratch_gone "a review on the shared job"
}

# No published policy and slice B named: the account segments a project job's name
# is derived from are the policy's profiles, so there is no name to compare the
# run's job with — a row nobody could read, not a finding about the release.
test_slice_b_with_no_published_policy_is_unmeasured() {
  prepare "$work/slice-b-no-policy" FIX_NO_POLICY=1
  run330_both
  expect_status "$status" 2 "slice B with no published policy"
  expect_contains "$(fact_line review-launch)" "FAIL" "slice B with no published policy: the row is not a pass"
  expect_contains "$(fact_line review-launch)" "no policy is published" "slice B with no published policy: and says why it could not read it"
  expect_contains "$(fact_line unmeasured)" "review-launch" "slice B with no published policy: the unmeasured row names it"
  expect_contains "$(fact_line log-coder)" "PASS" "slice B with no published policy: slice A's own reading still holds"
  expect_scratch_gone "slice B with no published policy"
}

# A job name no profile of the published policy derives under any provider and
# account: a review launched on a job the policy does not know is not the variant
# this slice reads, and the row says so rather than calling it a Claude account's.
test_a_review_job_the_policy_does_not_derive_is_a_fail() {
  prepare "$work/slice-b-unknown" FIX_REVIEW_JOB="fugaro-$SLUG-standard-anthropic-api-key--project-review"
  run330_both
  expect_status "$status" 1 "a job the policy does not derive"
  expect_contains "$(fact_line review-launch)" "FAIL" "a job the policy does not derive: the row fails"
  expect_contains "$(fact_line review-launch)" "no job the published policy derives" "a job the policy does not derive: and says so"
  expect_scratch_gone "a job the policy does not derive"
}

# The review-only variant mounts no credential of the repository's: a job that
# carries one is not the variant this slice measures.
test_a_review_job_carrying_a_secret_is_a_fail() {
  prepare "$work/job-secret" FIX_JOB_SECRET=GITHUB_TOKEN
  run330_both
  expect_status "$status" 1 "a review job carrying a secret"
  expect_contains "$(fact_line review-env)" "FAIL" "a review job carrying a secret: the row fails"
  expect_contains "$(fact_line review-env)" "GITHUB_TOKEN" "a review job carrying a secret: and names the entry"
  expect_scratch_gone "a review job carrying a secret"
}

test_a_review_job_carrying_a_secret_volume_is_a_fail() {
  prepare "$work/job-secret-volume" FIX_JOB_SECRET_VOLUME=1
  run330_both
  expect_status "$status" 1 "a review job carrying a secret volume"
  expect_contains "$(fact_line review-env)" "FAIL" "a review job carrying a secret volume: the row fails"
  expect_contains "$(fact_line review-env)" "secret volume(s)" "a review job carrying a secret volume: and names what it is"
  expect_scratch_gone "a review job carrying a secret volume"
}

# A review run whose calls carried a route did not go to the project's Anthropic
# key: the row is about the gateway's own line.
test_a_review_run_on_a_route_is_a_fail() {
  prepare "$work/slice-b-route" FIX_REVIEW_LOG_ROUTE=provider-x
  run330_both
  expect_status "$status" 1 "a review run on a route"
  expect_contains "$(fact_line review-log)" "FAIL" "a review run on a route: the row fails"
  expect_contains "$(fact_line review-log)" "no route-less model call" "a review run on a route: and says what it did not find"
  expect_scratch_gone "a review run on a route"
}

# A job that cannot be described is a row nobody could read, and it is never a
# pass: a job nobody described could hold anything.
test_an_undescribable_review_job_is_unmeasured() {
  prepare "$work/job-unread"
  RUN330_ENV=(FAKE330_JOB_FAIL=1)
  run330_both
  expect_status "$status" 2 "an undescribable review job"
  expect_contains "$(fact_line review-env)" "FAIL" "an undescribable review job: the row is not a pass"
  expect_contains "$(fact_line review-env)" "was not found" "an undescribable review job: and quotes what gcloud answered"
  expect_contains "$(fact_line unmeasured)" "review-env" "an undescribable review job: the unmeasured row names it"
  expect_contains "$(fact_line review-launch)" "PASS" "an undescribable review job: the launch itself was still read"
  expect_scratch_gone "an undescribable review job"
}

# Slice B's run that cannot be resolved at all: the four rows say so, and nothing
# is read for the reference.
test_a_review_run_that_does_not_resolve_is_unmeasured() {
  prepare "$work/review-run-missing"
  RUN330_ARGS=(--sandbox "$dir/sandbox" --run "$RUN_REF" --contrast "$CONTRAST_REF" --review "api-demo/20200101-000000-ffff")
  run330
  expect_status "$status" 2 "a review run that does not resolve"
  expect_contains "$(fact_line review-run)" "names no run" "a review run that does not resolve: the row says so"
  expect_contains "$(fact_line review-launch)" "did not resolve" "a review run that does not resolve: and slice B's rows too"
  expect_nothing_read_for "20200101-000000-ffff" "a review run that does not resolve"
  expect_scratch_gone "a review run that does not resolve"
}

# A record nobody can read is not a finding about the release: the criteria that
# stand on it read as rows nobody could take, and the run ends as one that could
# not tell.
test_an_unreadable_record_is_unmeasured() {
  prepare "$work/record-403"
  RUN330_ENV=(FAKE330_STORAGE_FAIL=all)
  run330_default
  expect_status "$status" 2 "an unreadable record"
  expect_contains "$(fact_line run)" "PASS" "an unreadable record: the run itself was still resolved"
  expect_contains "$(fact_line log-coder)" "PASS" "an unreadable record: the log read does not stand on the record"
  expect_contains "$(fact_line review-dollars)" "FAIL" "an unreadable record: the record's own row is not a pass"
  expect_contains "$(fact_line review-dollars)" "could not be read" "an unreadable record: and says why"
  expect_contains "$(fact_line route-by)" "could not be read" "an unreadable record: the split too"
  expect_contains "$(fact_line unmeasured)" "review-dollars" "an unreadable record: the unmeasured row names them"
  expect_absent "$out" "FACT review-dollars: PASS" "an unreadable record: nothing about the review is answered"
  expect_scratch_gone "an unreadable record"
}

# Slice B's own documents are the owner's, not this check's: the review run's
# record is read only for the job its launch names, and a launch record nobody
# can read is a row nobody could take — not this check's internal error, which
# would throw away the rows it had already read and cost the owner the launch.
test_an_unreadable_launch_record_is_unmeasured() {
  prepare "$work/launch-403"
  RUN330_ENV=(FAKE330_STORAGE_FAIL="$SLUG/$REVIEW_ID/launch.json")
  run330_both
  expect_status "$status" 2 "an unreadable launch record"
  expect_contains "$(fact_line review-launch)" "could not be read" "an unreadable launch record: the row says why"
  expect_contains "$(fact_line unmeasured)" "review-launch" "an unreadable launch record: the unmeasured row names it"
  expect_contains "$(fact_line review-dollars)" "PASS" "an unreadable launch record: slice A's own half is still read"
  expect_absent "$out" "FACT aborted" "an unreadable launch record: the check itself is not the failure"
  expect_scratch_gone "an unreadable launch record"
}

# The same contract for the review run's own record: a document that is not
# there is a row nobody could take, and the run ends as one that could not tell
# rather than as a finding about the cloud Anthropic-key path.
test_an_unreadable_review_record_is_unmeasured() {
  prepare "$work/review-record-403"
  RUN330_ENV=(FAKE330_STORAGE_FAIL="$SLUG/$REVIEW_ID/result.json")
  run330_both
  expect_status "$status" 2 "an unreadable review record"
  expect_contains "$(fact_line review-run)" "PASS" "an unreadable review record: the run itself was still resolved"
  expect_contains "$(fact_line review-launch)" "could not be read" "an unreadable review record: the row says why"
  expect_contains "$(fact_line unmeasured)" "review-launch" "an unreadable review record: the unmeasured row names them"
  expect_contains "$(fact_line unmeasured)" "review-log" "an unreadable review record: all three of slice B's rows"
  expect_contains "$(fact_line review-dollars)" "PASS" "an unreadable review record: slice A's own half is still read"
  expect_scratch_gone "an unreadable review record"
}

# A reference that resolves to no run is a reference that resolves to nothing,
# and the check says which runs the listing does hold rather than reading another
# one.
test_a_reference_that_resolves_to_no_run_is_unmeasured() {
  prepare "$work/run-ref-own"
  RUN330_ARGS=(--sandbox "$dir/sandbox" --run "api-demo/20200101-000000-ffff" --contrast "$CONTRAST_REF")
  run330
  expect_status "$status" 2 "a reference that resolves to no run"
  expect_contains "$(fact_line run)" "FAIL" "a reference that resolves to no run: the row fails"
  expect_contains "$(fact_line run)" "names no run" "a reference that resolves to no run: and says so"
  expect_contains "$(fact_line contrast-run)" "PASS" "a reference that resolves to no run: the contrast is still resolved"
  expect_nothing_read_for "20200101-000000-ffff" "a reference that resolves to no run: nothing about it is read"
  expect_scratch_gone "a reference that resolves to no run"
}

# An execution nobody can name is a log nobody can read: a run from before the
# launcher recorded one has no execution, and the row says so instead of
# reporting a log that holds nothing.
test_a_run_without_an_execution_has_no_log_to_read() {
  prepare "$work/no-exec" FIX_RUN_EXEC=
  run330_default
  expect_status "$status" 2 "a run without an execution"
  expect_contains "$(fact_line log-coder)" "not projects/<project>/locations" "a run without an execution: the row says what it read"
  expect_contains "$(fact_line unmeasured)" "log-coder" "a run without an execution: the unmeasured row names it"
  expect_scratch_gone "a run without an execution"
}

# A run whose review never ran measures nothing about the api-key review: the
# record holds no review-class stage, so there is nothing to read off it.
test_a_run_without_a_review_stage_is_unmeasured() {
  prepare "$work/no-review-stage" FIX_NO_REVIEW_STAGE=1
  run330_default
  expect_status "$status" 2 "a run without a review stage"
  expect_contains "$(fact_line review-dollars)" "holds no review-class stage" "a run without a review stage: the row says what is missing"
  expect_contains "$(fact_line review-dollars)" "plan,implement,fix,review_first" "a run without a review stage: and names the stages it did run"
  expect_scratch_gone "a run without a review stage"
}

# A log read that fails is not a log that holds no calls: the two read the same
# way in a row that does not say which happened, and this one says.
test_a_failed_log_read_is_unmeasured() {
  prepare "$work/log-denied"
  RUN330_ENV=(FAKE330_LOG_FAIL=all)
  run330_default
  expect_status "$status" 2 "a failed log read"
  expect_contains "$(fact_line log-coder)" "gcloud logging read" "a failed log read: the row says which call"
  expect_contains "$(fact_line log-coder)" "PERMISSION_DENIED" "a failed log read: and quotes what it answered"
  expect_contains "$(fact_line unmeasured)" "log-coder" "a failed log read: the unmeasured row names it"
  expect_scratch_gone "a failed log read"
}

# A log read that answers an empty list is a reading all the same: the run's
# calls are not in the log, and that is a FAIL about the run rather than a row
# nobody could take.
test_a_log_that_holds_no_calls_is_a_fail() {
  prepare "$work/log-empty" FIX_LOG_EMPTY=1
  run330_default
  expect_status "$status" 1 "a log that holds no calls"
  expect_contains "$(fact_line log-coder)" "FAIL" "a log that holds no calls: the row fails"
  expect_contains "$(fact_line log-review)" "FAIL" "a log that holds no calls: and so does the review's"
  expect_absent "$out" "FACT unmeasured:" "a log that holds no calls: nothing is unread"
  expect_scratch_gone "a log that holds no calls"
}

# The log read's scope is the installation's own view when there is one: without
# it a log-isolated installation's read answers nothing, and the row would read
# as a run whose calls never reached the gateway.
test_the_log_view_scopes_the_read() {
  prepare "$work/log-view"
  RUN330_ARGS=(--sandbox "$dir/sandbox" --run "$RUN_REF" --contrast "$CONTRAST_REF"
    --log-view "projects/$PROJECT/locations/global/buckets/fugaro/views/run-logs")
  run330
  expect_status "$status" 0 "the log view"
  expect_log_scopes "scope=projects/$PROJECT/locations/global/buckets/fugaro/views/run-logs" \
    "the log view: every log read is scoped to the installation's view"
  expect_scratch_gone "the log view"
}

# Without --log-view the read is scoped to the project itself, which is what the
# CLI does when the installation's config names no view.
test_without_a_log_view_the_read_is_scoped_to_the_project() {
  prepare "$work/no-log-view"
  run330_default
  expect_log_scopes "scope=projects/$PROJECT" "no log view: every log read is scoped to the project"
  expect_scratch_gone "no log view"
}

# A read that does not come back is a row nobody could take, and the check does
# not sit on it: the bound is CHECK330_TIMEOUT's, and the run ends as one that
# could not tell rather than answering from what it has.
test_a_read_that_does_not_come_back_is_bounded() {
  prepare "$work/hang"
  RUN330_ENV=(CHECK330_TIMEOUT=1 FAKE330_SLEEP=runs)
  run330_default
  expect_status "$status" 2 "a read that does not come back"
  expect_contains "$out" "did not answer within 1s" "a read that does not come back: the stop says which bound"
  expect_contains "$out" "FACT aborted: FAIL" "a read that does not come back: the run is aborted, not answered"
  expect_absent "$out" "FACT run: PASS" "a read that does not come back: no run is named"
  expect_scratch_gone "a read that does not come back"
}

# An installation with no published policy reads as one, and nothing about it is
# a finding: the rows say what they could not read instead of naming accounts.
test_an_installation_with_no_published_policy_reads_as_one() {
  prepare "$work/no-policy" FIX_NO_POLICY=1
  run330_default
  expect_status "$status" 0 "no published policy"
  expect_contains "$(fact_line policy)" "INFO" "no published policy: the policy row is not a criterion"
  expect_contains "$(fact_line policy)" "no policy is published" "no published policy: and says so"
  expect_contains "$(fact_line budget)" "INFO" "no published policy: and the budget row too"
  expect_scratch_gone "no published policy"
}

# --------------------------------------------------------------- the guards
# The project the sandbox belongs to is read, not typed, so the guard is over
# what fugaro doctor answers: an installation named `belong` is refused before a
# single run is read.
test_belong_is_refused() {
  prepare "$work/own-installation" FIX_PROJECT=prj-belong-sandbox
  run330_default
  expect_status "$status" 2 "belong"
  expect_contains "$(fact_line belong)" "FAIL" "belong: the row fails"
  expect_contains "$(fact_line belong)" "prj-belong-sandbox" "belong: and names the project it read"
  expect_contains "$out" "FACT aborted: FAIL" "belong: the run stops"
  expect_contains "$out" "must never read" "belong: and says never to read it"
  expect_gcloud_never_called "belong: no cloud call at all"
  expect_scratch_gone "belong"
}

# A run reference naming it is refused too — before the bucket is touched. This
# is the refusal that matters most: the reference is what gets typo'd.
test_belong_in_a_run_reference_is_refused() {
  prepare "$work/run-ref-guard"
  RUN330_ARGS=(--sandbox "$dir/sandbox" --run "$SLUG/belong" --contrast "$CONTRAST_REF")
  run330
  expect_status "$status" 2 "belong in a run reference"
  expect_contains "$(fact_line belong)" "FAIL" "belong in a run reference: the row fails"
  expect_contains "$(fact_line belong)" "api-demo/belong" "belong in a run reference: and names what it read"
  expect_gcloud_never_called "belong in a run reference: no cloud call at all"
  expect_scratch_gone "belong in a run reference"
}

# A log view naming it is refused too, and before a single read: that value is
# the scope of every log read (--resource-names), so a guard that did not see it
# would read the owner's own installation's logs while the belong row read PASS.
test_belong_in_a_log_view_is_refused() {
  prepare "$work/log-view-guard"
  RUN330_ARGS=(--sandbox "$dir/sandbox" --run "$RUN_REF" --contrast "$CONTRAST_REF"
    --log-view "projects/prj-belong-owner/locations/$REGION/views/run-logs")
  run330
  expect_status "$status" 2 "belong in a log view"
  expect_contains "$(fact_line belong)" "FAIL" "belong in a log view: the row fails"
  expect_contains "$(fact_line belong)" "prj-belong-owner" "belong in a log view: and names the view it read"
  expect_contains "$out" "FACT aborted: FAIL" "belong in a log view: the run stops"
  expect_gcloud_never_called "belong in a log view: no cloud call at all"
  expect_scratch_gone "belong in a log view"
}

# The dry run's guard is the same guard: the view is refused there too, before
# the plan is printed and before any read.
test_a_dry_run_still_refuses_belong_in_a_log_view() {
  prepare "$work/dry-log-view"
  run330 --sandbox "$dir/sandbox" --run "$RUN_REF" --contrast "$CONTRAST_REF" --dry-run \
    --log-view "projects/prj-belong-owner/locations/$REGION/views/run-logs"
  expect_status "$status" 2 "the dry run against a belong-named log view"
  expect_contains "$(fact_line belong)" "FAIL" "the dry run against a belong-named log view: the guard is asked"
  expect_contains "$(fact_line belong)" "prj-belong-owner" "the dry run against a belong-named log view: and names the view"
  expect_absent "$out" "FACT dry-run:" "the dry run against a belong-named log view: and it never reaches the plan"
  expect_gcloud_never_called "the dry run against a belong-named log view: no cloud call"
  expect_scratch_gone "the dry run against a belong-named log view"
}

# A log view that is not it is read, and the belong row says which view it
# stepped over rather than leaving the reader to guess.
test_a_log_view_is_named_in_the_belong_row() {
  prepare "$work/log-view-named"
  RUN330_ARGS=(--sandbox "$dir/sandbox" --run "$RUN_REF" --contrast "$CONTRAST_REF"
    --log-view "projects/$PROJECT/locations/global/buckets/fugaro/views/run-logs")
  run330
  expect_status "$status" 0 "a log view named in the belong row"
  expect_contains "$(fact_line belong)" "PASS" "a log view named in the belong row: the guard passes"
  expect_contains "$(fact_line belong)" "views/run-logs" "a log view named in the belong row: and the row names it"
  expect_scratch_gone "a log view named in the belong row"
}

# The review class refuses a run with no gateway (F11), so a mode-`off`
# installation measures that refusal and not the path: the check stops before it
# reads a run.
test_a_budget_mode_of_off_stops() {
  prepare "$work/budget-off" FIX_BUDGET_MODE=off
  run330_default
  expect_status "$status" 2 "a budget mode of off"
  expect_contains "$(fact_line budget)" "FAIL" "a budget mode of off: the row fails"
  expect_contains "$(fact_line budget)" "F11" "a budget mode of off: and names the refusal it would measure"
  expect_gcloud_never_called "a budget mode of off: no run is read"
  expect_scratch_gone "a budget mode of off"
}

# A coding agent's session is refused: the check is the owner's, and an agent
# that ran it would report its own session's limits as findings.
test_a_coding_agent_session_is_refused() {
  prepare "$work/agent"
  RUN330_ENV=(CLAUDECODE=1)
  run330_default
  expect_status "$status" 2 "a coding agent's session"
  expect_contains "$(fact_line agent-session)" "FAIL" "a coding agent's session: the row fails"
  expect_contains "$(fact_line agent-session)" "CLAUDECODE is set" "a coding agent's session: and names the marker"
  expect_gcloud_never_called "a coding agent's session: no cloud call at all"
  expect_scratch_gone "a coding agent's session"
}

# A traced shell is refused before anything is read: a trace would copy the
# run's own log output to a terminal.
test_a_traced_shell_is_refused() {
  prepare "$work/xtrace"
  status=0
  bounded env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    TMPDIR="$dir/tmp" HOME="$dir/home" FAKE330_DIR="$dir/state" PATH="$dir/bin:$PATH" \
    bash -x "$script" --sandbox "$dir/sandbox" --run "$RUN_REF" --contrast "$CONTRAST_REF" \
    >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=""
  expect_status "$status" 2 "a traced shell"
  expect_contains "$out" "FACT xtrace: FAIL" "a traced shell: the row fails"
  expect_contains "$out" "this shell is tracing" "a traced shell: and says what is tracing"
  expect_contains "$out" "FACT restore:" "a traced shell: the Restore is still printed"
  expect_no_calls "a traced shell: nothing is read at all"
  expect_no_scratch "a traced shell"
}

# ------------------------------------------------------------ the dry run
# The dry run prints the guards and every command a real one would make, reads
# nothing and gives no verdict.
test_a_dry_run_prints_the_plan_and_makes_no_call() {
  prepare "$work/dry"
  run330_default --dry-run
  expect_status "$status" 0 "the dry run"
  expect_contains "$out" "CHECK 330 RESULT" "the dry run: the block"
  expect_contains "$(fact_line belong)" "PASS" "the dry run: the belong guard over what was typed"
  expect_contains "$(fact_line dry-run)" "PASS" "the dry run: its own row"
  expect_contains "$(fact_line dry-run)" "no verdict was taken" "the dry run: and that no verdict was taken"
  expect_contains "$(fact_line restore)" "nothing of this check's was created" "the dry run: the Restore row"
  expect_absent "$out" "FACT project:" "the dry run: no row a real run would fill"
  # The plan itself: each read named, the scope the log read would be given, and
  # the rows it would print.
  expect_contains "$err" "NOTHING below was run" "the dry run: the plan says so"
  expect_contains "$err" "fugaro doctor --json" "the dry run: the plan names the first read"
  expect_contains "$err" "fugaro project policy show --json" "the dry run: and the policy read"
  expect_contains "$err" "fugaro runs ls --all --json --since 0" "the dry run: and the listing"
  expect_contains "$err" "gcloud storage cat" "the dry run: and the bucket read"
  expect_contains "$err" 'jsonPayload.message="model call"' "the dry run: and the log filter"
  expect_contains "$err" "gcloud run jobs describe" "the dry run: and the job read"
  expect_contains "$err" "route-by" "the dry run: and the rows it would print"
  expect_contains "$err" "gs://fugaro-runs-<project>/runs/" "the dry run: the bucket is a placeholder, not a read"
  expect_contains "$out" "CHECK 330 RESULT END (exit 0)" "the dry run: the end line"
  expect_no_calls "the dry run"
  expect_scratch_gone "the dry run"
}

test_a_dry_run_still_refuses_belong() {
  prepare "$work/dry-belong"
  run330 --sandbox "$dir/belong-checkout" --run "$RUN_REF" --contrast "$CONTRAST_REF" --dry-run
  expect_status "$status" 2 "the dry run against belong"
  expect_contains "$(fact_line belong)" "FAIL" "the dry run against belong: the guard is asked"
  expect_absent "$out" "FACT dry-run:" "the dry run against belong: and it never reaches the plan"
  expect_gcloud_never_called "the dry run against belong: no cloud call"
  expect_scratch_gone "the dry run against belong"
}

# ------------------------------------------------------------ the arguments
test_slice_c_is_refused_naming_check_50() {
  prepare "$work/slice-c"
  run330_default --provider
  expect_status "$status" 2 "slice C"
  expect_contains "$err" "Check 50" "slice C: the refusal names the check that gates it"
  expect_contains "$err" "internal/creds/select.go" "slice C: and why a run of it today measures nothing"
  expect_contains "$err" "Nothing was read" "slice C: and that nothing was read"
  expect_no_calls "slice C: nothing is read at all"
  expect_no_scratch "slice C"
}

test_usage_and_arguments() {
  prepare "$work/args"
  run330
  expect_status "$status" 2 "no arguments"
  expect_contains "$err" "--sandbox is required" "no arguments: the sandbox"
  expect_contains "$err" "usage: check330.sh" "no arguments: and the usage"
  expect_no_calls "no arguments: nothing is read"
  expect_no_scratch "no arguments"

  prepare "$work/args-run"
  run330 --sandbox "$dir/sandbox"
  expect_status "$status" 2 "no --run"
  expect_contains "$err" "--run is required" "no --run: the run"
  expect_no_calls "no --run: nothing is read"

  prepare "$work/args-contrast"
  run330 --sandbox "$dir/sandbox" --run "$RUN_REF"
  expect_status "$status" 2 "no --contrast"
  expect_contains "$err" "--contrast is required" "no --contrast: the contrast"
  expect_contains "$err" "the same task run again with a review stage account published" \
    "no --contrast: and why the pair is what the check is for"
  expect_no_calls "no --contrast: nothing is read"

  prepare "$work/args-unknown"
  run330_default --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument '--nonsense'" "an unknown argument: the refusal"
  expect_no_calls "an unknown argument: nothing is read"

  prepare "$work/args-slashes"
  run330 --sandbox "$dir/sandbox" --run a/b/c --contrast "$CONTRAST_REF"
  expect_status "$status" 2 "a run reference with two slashes"
  expect_contains "$err" "'a/b/c' carries two slashes" "a run reference with two slashes: the refusal"
  expect_no_calls "a run reference with two slashes: nothing is read"

  prepare "$work/args-help"
  run330 --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "usage: check330.sh --sandbox <checkout> --run <slug>/<run-id> --contrast <slug>/<run-id>" \
    "--help: the usage line"
  expect_absent "$out" "FACT " "--help: no block is printed"
  expect_absent "$out" "CHECK 330 RESULT" "--help: and no verdict"
  expect_no_calls "--help: nothing is read"
  expect_no_scratch "--help"
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

t test_the_pair_reads_as_pass
t test_a_coder_with_no_route_is_a_fail
t test_a_review_on_a_route_is_a_fail
t test_a_review_served_by_a_non_claude_model_is_a_fail
t test_a_notional_review_dollar_is_a_fail
t test_a_zero_review_dollar_is_a_fail
t test_a_split_that_is_not_the_own_stages_spend_is_a_fail
t test_a_split_equal_to_the_whole_run_is_a_fail
t test_a_missing_split_is_a_fail
t test_a_bouncing_run_with_a_residual_reads_as_its_own_stages_spend
t test_a_bouncing_run_whose_routes_carry_too_much_is_a_fail
t test_a_contrast_that_is_not_account_served_is_a_fail
t test_a_contrast_whose_figures_disagree_is_a_fail
t test_an_unreadable_contrast_is_unmeasured
t test_slice_b_reads_the_sentinel_variant
t test_slice_b_on_the_provider_key_variant_says_which_one_it_read
t test_a_review_on_an_account_variant_is_a_fail
t test_a_review_on_the_shared_job_of_the_sentinel_triple_is_a_fail
t test_slice_b_with_no_published_policy_is_unmeasured
t test_a_review_job_the_policy_does_not_derive_is_a_fail
t test_a_review_job_carrying_a_secret_is_a_fail
t test_a_review_job_carrying_a_secret_volume_is_a_fail
t test_a_review_run_on_a_route_is_a_fail
t test_an_undescribable_review_job_is_unmeasured
t test_a_review_run_that_does_not_resolve_is_unmeasured
t test_an_unreadable_record_is_unmeasured
t test_an_unreadable_launch_record_is_unmeasured
t test_an_unreadable_review_record_is_unmeasured
t test_a_reference_that_resolves_to_no_run_is_unmeasured
t test_a_run_without_an_execution_has_no_log_to_read
t test_a_run_without_a_review_stage_is_unmeasured
t test_a_failed_log_read_is_unmeasured
t test_a_log_that_holds_no_calls_is_a_fail
t test_the_log_view_scopes_the_read
t test_without_a_log_view_the_read_is_scoped_to_the_project
t test_a_read_that_does_not_come_back_is_bounded
t test_an_installation_with_no_published_policy_reads_as_one
t test_belong_is_refused
t test_belong_in_a_run_reference_is_refused
t test_belong_in_a_log_view_is_refused
t test_a_log_view_is_named_in_the_belong_row
t test_a_budget_mode_of_off_stops
t test_a_coding_agent_session_is_refused
t test_a_traced_shell_is_refused
t test_a_dry_run_prints_the_plan_and_makes_no_call
t test_a_dry_run_still_refuses_belong
t test_a_dry_run_still_refuses_belong_in_a_log_view
t test_slice_c_is_refused_naming_check_50
t test_usage_and_arguments

if [ "$failures" -ne 0 ]; then
  echo "check330_test: $failures failure(s)" >&2
  exit 1
fi
echo "check330_test: all tests passed" >&2
