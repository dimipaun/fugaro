#!/usr/bin/env bash
# Tests for scripts/live-checks/check191.sh, with fake `fugaro`, `gcloud` and
# `curl` on PATH: NOTHING here reaches a cloud, a project, a Bitbucket
# repository or the network, no agent runs a live command, and the fake tools
# record every call they are given so a case can assert what the check did — and
# did not — run.
#
# Usage: bash scripts/live-checks/check191_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check191.sh
work=$(mktemp -d)
kept=()
cleanup_all() {
  local d
  for d in ${kept[@]+"${kept[@]}"}; do rm -rf "$d" 2>/dev/null || true; done
  if [ -n "${CHECK191_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 700)" ;;
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

# One run id base for the whole test file, so a case that asserts on a run id and
# the run the check launches cannot disagree. It starts with today's UTC date
# because the check refuses a base that does not: the service reads a run's
# outcome under the day the RUN ID names.
run_base="$(date -u +%Y%m%d)-010101-chk191test"

# --------------------------------------------------------------------- fakes
# The fakes keep their state in ONE directory, so what the check launches is what
# the fake `gcloud` then answers audit lines and job documents about: a run the
# check started has a run id, and every later read of that run's lines answers
# about it and no other.
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check191.sh's test only.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE_LOG_DIR/fugaro.calls"
state=$FAKE_FUGARO_STATE
mode=${FAKE_FUGARO_MODE:-ok}

flag() { # flag NAME args... — the value of --NAME=<v> or --NAME <v>
  local name=$1 a
  shift
  while [ $# -gt 0 ]; do
    case "$1" in
    "$name")
      shift
      printf '%s' "${1:-}"
      return 0
      ;;
    "$name"=*)
      printf '%s' "${1#"$name"=}"
      return 0
      ;;
    esac
    shift
  done
  return 0
}
has() {
  local want=$1 a
  shift
  for a in "$@"; do
    if [ "$a" = "$want" ]; then return 0; fi
  done
  return 1
}

sub=${1:-}
shift || true
case "$sub" in
version)
  case "$mode" in
  dev) echo "fugaro 0.15.0-dev (abc1234, dirty)" ;;
  *) echo "fugaro 0.15.0 (test)" ;;
  esac
  ;;
doctor)
  # Faithful to internal/cli/doctor.go: the human report's `project:` line, and
  # --json's document with its `checks` array, which is where the after-flip
  # check reads bitbucket-direct from.
  if has --json "$@"; then
    cat "$state/doctor.json"
  else
    case "$mode" in
    other) echo "project: fugaro (GCP someone-else-prod, us-east5)" ;;
    *) echo "project: fugaro (GCP fugaro-dev, us-east5)" ;;
    esac
  fi
  ;;
project)
  # `project policy show --json`: the published node, {project, policy}.
  cat "$state/project-policy.json"
  ;;
run)
  rid=$(flag --run-id "$@")
  [ -n "$rid" ] || {
    echo "fugaro run: no --run-id" >&2
    exit 1
  }
  kind=work
  if has --recipe "$@"; then kind=review; fi
  task=$(flag --task-file "$@")
  case "${task##*/}" in *ended*) kind=ended ;; esac
  printf '%s\t%s\t%s\n' "$rid" "$kind" "$(flag --repo "$@")" >>"$state/runs"
  printf '{"run_id":"%s","repo":"%s","status":"queued"}\n' "$rid" "$(flag --repo "$@")"
  ;;
runs)
  python3 - "$state" "$mode" "$(flag --repo "$@")" <<'PY'
import json, os, sys
state, mode, want = sys.argv[1], sys.argv[2], sys.argv[3]
runs = []
path = os.path.join(state, "runs")
for line in open(path).read().splitlines() if os.path.exists(path) else []:
    rid, kind, repo = line.split("\t")
    if want and want != repo:
        continue
    row = {"run": repo.replace("/", "-") + "/" + rid, "run_id": rid,
           "repo": repo, "terminal": True}
    if kind == "ended":
        row.update({"status": "halted", "pushed": False,
                    "reason": "the run has ended: its outcome is written"})
    elif mode == "run-failed" and kind == "work":
        row.update({"status": "failed", "pushed": False})
    else:
        row.update({"status": "succeeded", "pushed": False})
    runs.append(row)
print(json.dumps({"project": "fugaro", "runs": runs, "totals": {}}))
PY
  ;;
logs)
  rid=${1:-}
  kind=$(sed -n "s/^$rid\t//p" "$state/runs" 2>/dev/null | head -n 1 | cut -f1)
  case "$kind" in
  ended) echo "fatal: the run has ended: its outcome is written" ;;
  review) echo "CHK191 attempt=push refused=yes answer=403" ;;
  *)
    for attempt in other-repository other-ref tag ref-delete other-pull-request merge workspaces; do
      refused=yes
      answer=403
      if [ "$mode" = unreported ] && [ "$attempt" = tag ]; then
        refused=no
        answer=200
      fi
      echo "CHK191 attempt=$attempt refused=$refused answer=$answer"
    done
    ;;
  esac
  ;;
*)
  echo "fugaro: unknown command $sub" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env python3
"""A stand-in for `gcloud`, for scripts/live-checks/check191.sh's test only.

Every subcommand the check may call is listed with the flags it may pass; a call
to a subcommand that is not here, or with a flag that is not in its list, fails
with exit 2, so an invented or misspelled flag cannot hide in any call — and no
write subcommand is callable at all.
"""
import json, os, sys

STATE = os.environ["FAKE_GCLOUD_STATE"]
LOG = os.environ["FAKE_LOG_DIR"]
mode = os.environ.get("FAKE_GCLOUD_MODE", "ok")

args = sys.argv[1:]
with open(os.path.join(LOG, "gcloud.calls"), "a") as fh:
    fh.write("gcloud " + " ".join(args) + "\n")

ALLOWED = {
    ("auth", "print-access-token"): (),
    ("run", "jobs", "list"): ("--project", "--region", "--format"),
    ("secrets", "get-iam-policy"): ("--project", "--format"),
    ("logging", "read"): ("--project", "--freshness", "--limit", "--format"),
}
sub = None
for n in (3, 2):
    if tuple(args[:n]) in ALLOWED:
        sub = tuple(args[:n])
        break
if sub is None:
    sys.stderr.write("ERROR: (gcloud) unknown command: %s\n" % " ".join(args))
    sys.exit(2)
for a in args:
    if a.startswith("--") and a.split("=")[0] not in ALLOWED[sub]:
        sys.stderr.write("ERROR: (gcloud) unrecognized argument: %s\n" % a)
        sys.exit(2)


def flag(name, default=""):
    for i, a in enumerate(args):
        if a.startswith(name + "="):
            return a[len(name) + 1:]
        if a == name:
            return args[i + 1] if i + 1 < len(args) else ""
    return default


def state(name, default):
    path = os.path.join(STATE, name)
    return open(path).read() if os.path.exists(path) else default


PROJECT = flag("--project")
if PROJECT and PROJECT != "fugaro-dev":
    sys.stderr.write("ERROR: (gcloud) this check runs on fugaro-dev only\n")
    sys.exit(1)

if sub == ("auth", "print-access-token"):
    sys.stdout.write("FAKE-ACCESS-TOKEN\n")
elif sub == ("run", "jobs", "list"):
    if flag("--region") != "us-east5":
        sys.stderr.write("ERROR: (gcloud) this check reads us-east5 only\n")
        sys.exit(1)
    sys.stdout.write(state("jobs.json", "[]\n"))
elif sub == ("secrets", "get-iam-policy"):
    if "bitbucket-token" not in args:
        sys.stderr.write("ERROR: (gcloud) this check reads bitbucket-token only\n")
        sys.exit(1)
    sys.stdout.write(state("secret-policy.json", '{"bindings":[]}\n'))
elif sub == ("logging", "read"):
    # The audit lines of every run the fake `fugaro` launched: one `ok` line for
    # a run that was served, one refusal line per class the check's attempts are
    # expected to be refused with. The ended run's refusal is NOT audited (the
    # service answers it before there is a verified identity) and so gets none.
    classes = ["other_repository", "other_ref", "tag_ref", "ref_delete",
               "other_pull_request", "call_not_allowed", "call_not_allowed"]
    out = []
    path = os.path.join(STATE, "runs")
    for line in open(path).read().splitlines() if os.path.exists(path) else []:
        rid, kind, _repo = line.split("\t")
        if kind == "ended":
            continue
        common = {"message": "credsvc: audit", "run": rid,
                  "repository": "bitbucket/workspace-a/repo-a",
                  "job": "fugaro-workspace-a-repo-a-web-0123456789ab",
                  "caller": "fugaro-b-1@fugaro-dev.iam.gserviceaccount.com",
                  "credential": "bitbucket-token"}
        out.append({"jsonPayload": dict(common, purpose="github_read", status="ok")})
        want = classes if kind == "work" else ["no_run_branch"]
        if mode == "missing-tag-refusal" and kind == "work":
            want = [c for c in want if c != "tag_ref"]
        if mode == "no-review-refusal" and kind == "review":
            want = []
        for cls in want:
            out.append({"jsonPayload": dict(common, purpose="git", status=cls)})
    sys.stdout.write(json.dumps(out) + "\n")
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_curl() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A stand-in for `curl`, for scripts/live-checks/check191.sh's test only: the
# outcome node the check writes before it launches the ended-run probe, the
# read-back of it, and the delete that follows. It is stateful — a node that was
# PUT reads back, a node that was DELETEd reads back `null` — and it records the
# method and the URL alone: never a header, and never an authorization token.
set -euo pipefail
mode=${FAKE_CURL_MODE:-ok}
state=$FAKE_CURL_STATE
method=GET
url=""
while [ $# -gt 0 ]; do
  case "$1" in
  -X)
    shift
    method=${1:-GET}
    ;;
  -H | -d | -o | --data | --header | --output)
    shift
    ;;
  http://* | https://*) url=$1 ;;
  esac
  shift
done
printf 'curl %s %s\n' "$method" "$url" >>"$FAKE_LOG_DIR/curl.calls"
case "$url" in
*"/outcomes/"*) ;;
*) exit 0 ;;
esac
case "$method" in
PUT)
  if [ "$mode" = outcome-refused ]; then
    echo "curl: (22) The requested URL returned error: 403" >&2
    exit 22
  fi
  : >"$state/outcome.json"
  ;;
DELETE) rm -f "$state/outcome.json" ;;
GET)
  if [ -f "$state/outcome.json" ]; then
    printf '{"status":"succeeded","requestedBy":"check191"}\n'
  else
    printf 'null\n'
  fi
  ;;
esac
CURL_EOF
  chmod +x "$1/bin/curl"
}

# ------------------------------------------------------------------- the runs
run_check() { # run_check DIR SCRIPT ENV... -- ARGS...
  local dir=$1 checked=$2
  shift 2
  local args=() cli=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    args+=("$1")
    shift
  done
  if [ "${1:-}" = "--" ]; then
    shift
    cli=("$@")
  fi
  mkdir -p "$dir/state" "$dir/logs" "$dir/home/.config/fugaro/projects"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/curl.calls"
  cat >"$dir/home/.config/fugaro/projects/fugaro.yaml" <<'CFG_EOF'
budget:
  rtdb_url: https://fugaro-dev-default-rtdb.firebaseio.com
CFG_EOF
  status=0
  env \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_GCLOUD_STATE="$dir/state" \
    FAKE_FUGARO_STATE="$dir/state" FAKE_CURL_STATE="$dir/state" \
    CHECK191_REPO=workspace-a/repo-a CHECK191_OTHER_REPO=workspace-a/repo-b \
    CHECK191_PLAIN_REPO=workspace-a/plain CHECK191_OTHER_PR=4 \
    CHECK191_RUN_ID="$run_base" CHECK191_WAIT_SECONDS=0 CHECK191_POLL_SECONDS=0 \
    ${args[@]+"${args[@]}"} \
    bash "$checked" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

prepare() { # dir
  mkdir -p "$1/home" "$1/tmp" "$1/state"
  write_fake_fugaro "$1"
  write_fake_gcloud "$1"
  write_fake_curl "$1"
  # The published node: the sandbox repository is admitted with proxy: true.
  cat >"$1/state/project-policy.json" <<'POLICY_EOF'
{"project":"fugaro","policy":{"bitbucket_token":{"allow_repos":["bitbucket/workspace-a/repo-a"],"proxy":true}}}
POLICY_EOF
  cat >"$1/state/doctor.json" <<'DOCTOR_EOF'
{"ok":true,"checks":[{"id":"bitbucket-direct","ok":true},{"id":"creds-service","ok":true}],"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}
DOCTOR_EOF
  # The repository's jobs, as `gcloud run jobs list --format=json` answers them:
  # the v1 Job shape, whose service account and container env are at
  # spec.template.spec.template.spec.
  cat >"$1/state/jobs.json" <<'JOBS_EOF'
[{"metadata":{"name":"fugaro-workspace-a-repo-a-web-0123456789ab"},
  "spec":{"template":{"spec":{"template":{"spec":{
    "serviceAccountName":"fugaro-b-1@fugaro-dev.iam.gserviceaccount.com",
    "containers":[{"image":"example.invalid/base:0.15.0","env":[
      {"name":"FUGARO_REMOTE","value":"https://bitbucket.org/workspace-a/repo-a.git"},
      {"name":"FUGARO_RUN","value":"1"}]}]}}}}}},
 {"metadata":{"name":"fugaro-workspace-a-plain-web-fedcba987654"},
  "spec":{"template":{"spec":{"template":{"spec":{
    "serviceAccountName":"fugaro-b-2@fugaro-dev.iam.gserviceaccount.com",
    "containers":[{"image":"example.invalid/base:0.15.0","env":[
      {"name":"FUGARO_REMOTE","value":"https://bitbucket.org/workspace-a/plain.git"},
      {"name":"FUGARO_BITBUCKET_TOKEN","valueSource":{"secretKeyRef":{"secret":"bitbucket-token","version":"latest"}}}]}]}}}}}}]
JOBS_EOF
  # The secret's own IAM policy: after the flip no job account holds a role on it.
  cat >"$1/state/secret-policy.json" <<'IAM_EOF'
{"bindings":[{"role":"roles/secretmanager.secretAccessor","members":["serviceAccount:fugaro-credentials@fugaro-dev.iam.gserviceaccount.com"]}]}
IAM_EOF
}

# --------------------------------------------------------------- the test cases
test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" "$script" CHECK191_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 191 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version"
  expect_contains "$out" "FACT project-region: PASS — fugaro-dev/us-east5" "dry run: the hard-coded project"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker fact"
  expect_contains "$out" "FACT restore: PASS — nothing of this check's was created" "dry run: nothing to delete"
  expect_contains "$out" "CHECK 191 RESULT END (exit 0)" "dry run: the end line"
  # A row the plan did not measure reads INFO, never PASS: a plan is not a
  # reading, and a block that said PASS would be one nobody took.
  expect_contains "$out" "FACT proxied: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$out" "FACT refusal-tag-ref: INFO — planned" "dry run: a planned attempt is INFO"
  expect_contains "$err" "the throwaway run itself" "dry run: the plan names the run it would launch"
  # Nothing was launched, read or written.
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "dry run: no run launched"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "dry run: no cloud read"
  expect_absent "$(cat "$dir/logs/curl.calls")" "PUT" "dry run: no outcome written"
  [ -n "$scratch" ] || fail "dry run: the script did not say where its scratch directory is"
  if [ -d "$scratch" ]; then fail "dry run: the scratch directory $scratch survived"; fi
}

test_the_plan_prints_for_the_flip() {
  local dir=$work/flipdry
  prepare "$dir"
  run_check "$dir" "$script" -- --after-flip --dry-run
  expect_status "$status" 0 "after-flip dry run"
  expect_contains "$out" "CHECK 192 RESULT" "after-flip dry run: the block is Check 192's"
  expect_contains "$out" "FACT mount: INFO — planned" "after-flip dry run: the mount row is planned"
  expect_contains "$out" "FACT accessor: INFO — planned" "after-flip dry run: the accessor row is planned"
  expect_contains "$err" "gcloud secrets get-iam-policy bitbucket-token" "after-flip dry run: the accessor read is printed"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "after-flip dry run: nothing was read"
  if [ -d "$scratch" ]; then fail "after-flip dry run: the scratch directory survived"; fi
}

test_project_guard() {
  local dir=$work/other
  prepare "$dir"
  run_check "$dir" "$script" FAKE_FUGARO_MODE=other CHECK191_DRY_RUN=1
  expect_status "$status" 1 "project guard"
  expect_contains "$err" "not the fugaro-dev installation" "project guard: the stop"
  expect_contains "$out" "FACT aborted: FAIL" "project guard: the block says the check was stopped"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "project guard: nothing was read of another project"
  if [ -d "$scratch" ]; then fail "project guard: the scratch directory survived"; fi
}

# The red step, as the plan states it: the project guard is what stops a check
# pointed at another installation. The copy below has the guard's own stop
# neutralised (its line becomes a no-op, so the copy still parses), and with it
# gone the very same run goes to its end — which is the whole of what the guard
# is for, and what fails the moment the guard stops being load-bearing.
test_the_project_guard_is_what_stops_a_foreign_installation() {
  local dir=$work/guard copy
  prepare "$dir"
  copy=$dir/check191-noguard.sh
  sed 's|^\( *\)stop "this is not|\1: "this is not|' "$script" >"$copy" 2>/dev/null || true
  if [ ! -s "$copy" ] || cmp -s "$script" "$copy"; then
    fail "red step: the copy is the script: the guard's own stop line was not found to neutralise"
    return 0
  fi
  run_check "$dir" "$copy" CHECK191_DRY_RUN=1 FAKE_FUGARO_MODE=other
  expect_status "$status" 0 "red step: without the guard the run is not stopped"
  expect_contains "$out" "FACT dry-run: PASS" "red step: the run went past the guard to its end"
  expect_contains "$out" "CHECK 191 RESULT END (exit 0)" "red step: its block is a finished block"
}

test_repo_guard() {
  local dir=$work/norepo
  prepare "$dir"
  run_check "$dir" "$script" CHECK191_REPO=belong/app CHECK191_DRY_RUN=1
  expect_status "$status" 1 "repo guard"
  expect_contains "$err" "belong" "repo guard: the stop names what it refused"
  if [ -d "$scratch" ]; then fail "repo guard: the scratch directory survived"; fi
}

test_the_run_id_must_name_today() {
  local dir=$work/ridday
  prepare "$dir"
  run_check "$dir" "$script" CHECK191_RUN_ID=20200101-000000-chk191 CHECK191_DRY_RUN=1
  expect_status "$status" 1 "run id of another day"
  expect_contains "$err" "today's UTC date" "run id of another day: the stop says why"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir" "$script"
  expect_status "$status" 0 "happy path"
  local want calls
  for want in \
    "FACT proxied: PASS" \
    "FACT run-launch: PASS" \
    "FACT run-outcome: PASS" \
    "FACT refusal-other-repository: PASS" \
    "FACT refusal-other-ref: PASS" \
    "FACT refusal-tag-ref: PASS" \
    "FACT refusal-ref-delete: PASS" \
    "FACT refusal-other-pull-request: PASS" \
    "FACT refusal-merge: PASS" \
    "FACT refusal-workspaces: PASS" \
    "FACT review-run: PASS" \
    "FACT ended-identity: PASS" \
    "FACT restore: PASS" \
    "FACT scratch-removed: PASS" \
    "CHECK 191 RESULT END (exit 0)"; do
    expect_contains "$out" "$want" "happy path: $want"
  done
  # The three runs, each launched with the id this check built for it, the
  # review one as a review-only run.
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_contains "$calls" "--run-id $run_base-work" "happy path: the working run"
  expect_contains "$calls" "--run-id $run_base-review" "happy path: the review run"
  expect_contains "$calls" "--recipe review-only" "happy path: the review run is a review-only run"
  expect_contains "$calls" "--run-id $run_base-ended" "happy path: the probe"
  # The outcome node was written, read back and deleted, and it is the node the
  # service reads a run's outcome from.
  calls=$(cat "$dir/logs/curl.calls")
  expect_contains "$calls" "PUT https://fugaro-dev-default-rtdb.firebaseio.com/outcomes/" "happy path: the outcome was written"
  expect_contains "$calls" "DELETE https://fugaro-dev-default-rtdb.firebaseio.com/outcomes/" "happy path: the outcome was deleted"
  # No token anywhere in what the check printed.
  expect_absent "$out" "FAKE-ACCESS-TOKEN" "happy path: the access token is not in the block"
  expect_absent "$err" "FAKE-ACCESS-TOKEN" "happy path: the access token is not on stderr"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "--project=fugaro-dev" "happy path: every gcloud call names the installation's project"
}

test_a_run_that_did_not_succeed_is_a_failure() {
  local dir=$work/runfailed
  prepare "$dir"
  run_check "$dir" "$script" FAKE_FUGARO_MODE=run-failed
  expect_status "$status" 1 "a failed run"
  expect_contains "$out" "FACT run-outcome: FAIL" "a failed run: the fact"
}

test_a_forbidden_attempt_that_was_not_audited_is_a_failure() {
  local dir=$work/unaudited
  prepare "$dir"
  run_check "$dir" "$script" FAKE_GCLOUD_MODE=missing-tag-refusal
  expect_status "$status" 1 "an unaudited refusal"
  expect_contains "$out" "FACT refusal-tag-ref: FAIL" "an unaudited refusal: the fact names the class that is missing"
  expect_contains "$out" "FACT refusal-other-ref: PASS" "an unaudited refusal: the other rows still hold on their own audit lines"
}

test_a_forbidden_attempt_the_run_did_not_report_is_a_failure() {
  local dir=$work/unreported
  prepare "$dir"
  run_check "$dir" "$script" FAKE_FUGARO_MODE=unreported
  expect_status "$status" 1 "an unreported refusal"
  expect_contains "$out" "FACT refusal-tag-ref: FAIL" "an unreported refusal: the fact names the attempt the run reported as served"
}

test_the_review_runs_push_refusal_is_asserted() {
  local dir=$work/review
  prepare "$dir"
  run_check "$dir" "$script" FAKE_GCLOUD_MODE=no-review-refusal
  expect_status "$status" 1 "the review run's push"
  expect_contains "$out" "FACT review-run: FAIL" "the review run's push: the fact"
  expect_contains "$out" "FACT ended-identity: PASS" "the review run's push: the rows after it were still measured"
}

test_an_outcome_that_cannot_be_written_stops_the_check() {
  local dir=$work/endedrefused
  prepare "$dir"
  # The outcome write is refused, so the probe cannot be set up at all: the row
  # is a FAIL, the probe is never launched, and the check ends as one that could
  # not measure rather than as one whose criteria held.
  run_check "$dir" "$script" FAKE_CURL_MODE=outcome-refused
  expect_status "$status" 2 "an outcome that could not be written"
  expect_contains "$out" "FACT ended-identity: FAIL" "an unwritable outcome: the fact"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "--run-id $run_base-ended" "an unwritable outcome: no probe was launched"
  expect_contains "$out" "FACT scratch-removed: PASS" "an unwritable outcome: the scratch directory is still restored"
}

test_after_flip_happy_path() {
  local dir=$work/after
  prepare "$dir"
  run_check "$dir" "$script" -- --after-flip
  expect_status "$status" 0 "after-flip"
  local want
  for want in \
    "FACT jobs: PASS" \
    "FACT mount: PASS" \
    "FACT accessor: PASS" \
    "FACT doctor: PASS" \
    "FACT run-still-works: PASS" \
    "FACT plain-repo: PASS" \
    "FACT restore: PASS" \
    "CHECK 192 RESULT END (exit 0)"; do
    expect_contains "$out" "$want" "after-flip: $want"
  done
  expect_contains "$out" "FACT mount: PASS — 1 job of workspace-a/repo-a carries FUGARO_REMOTE and none mounts bitbucket-token" \
    "after-flip: the mount row counts the repository's jobs"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "--run-id $run_base-plain" "after-flip: the plain repository's run"
  expect_absent "$(cat "$dir/logs/curl.calls")" "PUT" "after-flip: no outcome node is written"
}

test_after_flip_a_job_that_still_mounts_the_token_is_a_failure() {
  local dir=$work/mounted
  prepare "$dir"
  python3 - "$dir/state/jobs.json" <<'PY'
import json, sys
path = sys.argv[1]
jobs = json.load(open(path))
spec = jobs[0]["spec"]["template"]["spec"]["template"]["spec"]
spec["containers"][0]["env"].append(
    {"name": "FUGARO_BITBUCKET_TOKEN",
     "valueSource": {"secretKeyRef": {"secret": "bitbucket-token", "version": "latest"}}})
open(path, "w").write(json.dumps(jobs))
PY
  run_check "$dir" "$script" -- --after-flip
  expect_status "$status" 1 "a job that still mounts the token"
  expect_contains "$out" "FACT mount: FAIL" "a stale mount: the fact"
}

test_after_flip_an_account_that_still_has_the_accessor_is_a_failure() {
  local dir=$work/accessor
  prepare "$dir"
  cat >"$dir/state/secret-policy.json" <<'IAM_EOF'
{"bindings":[{"role":"roles/secretmanager.secretAccessor","members":["serviceAccount:fugaro-credentials@fugaro-dev.iam.gserviceaccount.com","serviceAccount:fugaro-b-1@fugaro-dev.iam.gserviceaccount.com"]}]}
IAM_EOF
  run_check "$dir" "$script" -- --after-flip
  expect_status "$status" 1 "an account that still holds the accessor"
  expect_contains "$out" "FACT accessor: FAIL" "a live accessor: the fact"
}

test_after_flip_a_doctor_finding_is_a_failure() {
  local dir=$work/doctorwarn
  prepare "$dir"
  cat >"$dir/state/doctor.json" <<'DOCTOR_EOF'
{"ok":false,"checks":[{"id":"bitbucket-direct","ok":false,"severity":"warning","problem":"workspace-a/repo-a is proxied and flipped, and fugaro-workspace-a-repo-a-web-0123456789ab still mounts bitbucket-token"}],"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}
DOCTOR_EOF
  run_check "$dir" "$script" -- --after-flip
  expect_status "$status" 1 "doctor's bitbucket-direct warning"
  expect_contains "$out" "FACT doctor: FAIL" "a doctor finding: the fact"
  expect_contains "$out" "still mounts bitbucket-token" "a doctor finding: the problem word is carried into the detail"
}

test_after_flip_a_plain_repository_that_lost_its_mount_is_a_failure() {
  local dir=$work/plainlost
  prepare "$dir"
  python3 - "$dir/state/jobs.json" <<'PY'
import json, sys
path = sys.argv[1]
jobs = json.load(open(path))
spec = jobs[1]["spec"]["template"]["spec"]["template"]["spec"]
spec["containers"][0]["env"] = [e for e in spec["containers"][0]["env"]
                                if e["name"] != "FUGARO_BITBUCKET_TOKEN"]
open(path, "w").write(json.dumps(jobs))
PY
  run_check "$dir" "$script" -- --after-flip
  expect_status "$status" 1 "a plain repository without its mount"
  expect_contains "$out" "FACT plain-repo: FAIL" "a plain repository without its mount: the fact"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "--run-id $run_base-plain" "a plain repository without its mount: no run was launched in it"
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i (each case works in its own directory under
# $work), CHECK_TEST_ONLY=name runs that one alone (a solo rerun of one that
# looked flaky). Neither set: all of them run.
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

t test_dry_run
t test_the_plan_prints_for_the_flip
t test_project_guard
t test_the_project_guard_is_what_stops_a_foreign_installation
t test_repo_guard
t test_the_run_id_must_name_today
t test_happy_path
t test_a_run_that_did_not_succeed_is_a_failure
t test_a_forbidden_attempt_that_was_not_audited_is_a_failure
t test_a_forbidden_attempt_the_run_did_not_report_is_a_failure
t test_the_review_runs_push_refusal_is_asserted
t test_an_outcome_that_cannot_be_written_stops_the_check
t test_after_flip_happy_path
t test_after_flip_a_job_that_still_mounts_the_token_is_a_failure
t test_after_flip_an_account_that_still_has_the_accessor_is_a_failure
t test_after_flip_a_doctor_finding_is_a_failure
t test_after_flip_a_plain_repository_that_lost_its_mount_is_a_failure

if [ "$failures" -ne 0 ]; then
  echo "check191_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check191_test.sh: ok"
