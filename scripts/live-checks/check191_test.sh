#!/usr/bin/env bash
# Tests for scripts/live-checks/check191.sh, with fake `fugaro`, `gcloud`,
# `curl` and `git` on PATH: NOTHING here reaches a cloud, a project, a Bitbucket
# repository or the network, no agent runs a live command, and the fake tools
# record every argument they are given so the test can assert that no command
# ever named anything but the sandbox installation's project and that no token
# ever appears in an argument or in the output.
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 600)" ;;
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

# --------------------------------------------------------------------- fakes
# The fakes keep their state in ONE directory ($FAKE_*_STATE), so what the
# check launches is what the fake `gcloud` then answers audit lines and job
# documents about: a run the check started has a run id, and every later read
# of that run's lines answers about it and no other.
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check191.sh's test only.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE_LOG_DIR/fugaro.calls"
state=$FAKE_FUGARO_STATE
mode=${FAKE_FUGARO_MODE:-ok}

flag() { # flag NAME args...
  local name=$1
  shift
  while [ $# -gt 0 ]; do
    case "$1" in
    "$name")
      shift
      printf '%s' "${1:-}"
      return 0
      ;;
    esac
    shift
  done
  return 0
}
has() {
  local a
  for a in "$@"; do [ "$a" = "$1" ] && return 0; done
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
  # Faithful to internal/cli/doctor.go's emitDoctor for the two shapes the check
  # reads: the `project:` line on stdout, and --json's `checks` array.
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
  # `project policy show --json`: {project, policy}, the published node.
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
  printf '%s\t%s\n' "$rid" "$kind" >>"$state/runs"
  printf '{"run_id":"%s","repo":"%s","status":"queued"}\n' "$rid" "$(flag --repo "$@")"
  ;;
runs)
  python3 - "$state" "$mode" <<'PY'
import json, os, sys
state, mode = sys.argv[1], sys.argv[2]
runs = []
path = os.path.join(state, "runs")
lines = open(path).read().splitlines() if os.path.exists(path) else []
for line in lines:
    rid, kind = line.split("\t")
    if kind == "ended":
        row = {"run": "workspace-a-repo-a/" + rid, "run_id": rid, "repo": "workspace-a/repo-a",
               "status": "halted", "terminal": True, "pushed": False, "reason": "the run has ended: its outcome is written"}
    elif mode == "run-failed" and kind == "work":
        row = {"run": "workspace-a-repo-a/" + rid, "run_id": rid, "repo": "workspace-a/repo-a",
               "status": "failed", "terminal": True, "pushed": False, "failure_class": "agent"}
    else:
        row = {"run": "workspace-a-repo-a/" + rid, "run_id": rid, "repo": "workspace-a/repo-a",
               "status": "succeeded", "terminal": True, "pushed": kind != "review", "pr": 7,
               "pr_url": "https://bitbucket.org/workspace-a/repo-a/pull-requests/7"}
    runs.append(row)
print(json.dumps({"project": "fugaro", "runs": runs, "totals": {}}))
PY
  ;;
logs)
  rid=${1:-}
  kind=$(sed -n "s/^$rid\t//p" "$state/runs" 2>/dev/null | head -1)
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

Every subcommand this check may call is listed with the flags it may pass; a
call to a subcommand that is not here, or with a flag that is not in its list,
fails with exit 2, so an invented or misspelled flag cannot hide in any call and
a WRITE subcommand is not callable at all.
"""
import json, os, sys

STATE = os.environ["FAKE_GCLOUD_STATE"]
LOG = os.environ["FAKE_LOG_DIR"]
mode = os.environ.get("FAKE_GCLOUD_MODE", "ok")

with open(os.path.join(LOG, "gcloud.calls"), "a") as fh:
    fh.write("gcloud " + " ".join(sys.argv[1:]) + "\n")

args = sys.argv[1:]
ALLOWED = {
    ("auth", "print-identity-token"): ("--format",),
    ("auth", "print-access-token"): (),
    ("run", "jobs", "list"): ("--project", "--region", "--format"),
    ("secrets", "get-iam-policy"): ("--project", "--format"),
    ("logging", "read"): ("--project", "--freshness", "--limit", "--format"),
}
positionals = [a for a in args if not a.startswith("-")]
sub = None
for n in (3, 2):
    if tuple(positionals[:n]) in ALLOWED:
        sub = tuple(positionals[:n])
        break
if sub is None:
    sys.stderr.write("ERROR: (gcloud) unknown command: %s\n" % " ".join(args))
    sys.exit(2)
for a in args:
    if a.startswith("--") and a.split("=")[0] not in ALLOWED[sub]:
        sys.stderr.write("ERROR: (gcloud) unrecognized argument: %s\n" % a)
        sys.exit(2)


def flag(name, default=""):
    for a in args:
        if a.startswith(name + "="):
            return a[len(name) + 1:]
    return default


def has(name):
    return name in args


PROJECT = flag("--project")
if PROJECT and PROJECT != "fugaro-dev":
    sys.stderr.write("ERROR: (gcloud) this check runs on fugaro-dev only\n")
    sys.exit(1)


def state(name, default):
    path = os.path.join(STATE, name)
    if os.path.exists(path):
        return open(path).read()
    return default


def emit(text):
    sys.stdout.write(text)


if sub == ("auth", "print-identity-token"):
    emit("FAKE-IDENTITY-TOKEN\n")
elif sub == ("auth", "print-access-token"):
    emit("FAKE-ACCESS-TOKEN\n")
elif sub == ("run", "jobs", "list"):
    if flag("--region") != "us-east5":
        sys.stderr.write("ERROR: (gcloud) this check reads us-east5 only\n")
        sys.exit(1)
    emit(state("jobs.json", "[]\n"))
elif sub == ("secrets", "get-iam-policy"):
    if not positionals or positionals[-1] != "bitbucket-token":
        sys.stderr.write("ERROR: (gcloud) this check reads bitbucket-token only\n")
        sys.exit(1)
    emit(state("secret-policy.json", '{"bindings":[]}\n'))
elif sub == ("logging", "read"):
    # The audit lines of every run the fake `fugaro` launched: one `ok` line for
    # a run that worked, one refusal line per class the check's attempts are
    # expected to be refused with. The ended run's refusal is NOT audited (the
    # service answers it before there is a verified identity), so it gets none.
    runs = []
    path = os.path.join(STATE, "runs")
    if os.path.exists(path):
        for line in open(path).read().splitlines():
            rid, kind = line.split("\t")
            runs.append((rid, kind))
    classes = ["other_repository", "other_ref", "tag_ref", "ref_delete",
               "other_pull_request", "call_not_allowed", "call_not_allowed"]
    out = []
    for rid, kind in runs:
        if kind == "ended":
            continue
        out.append({"jsonPayload": {"message": "credsvc: audit", "run": rid,
                                    "repository": "bitbucket/workspace-a/repo-a",
                                    "job": "fugaro-workspace-a-repo-a-web-0123456789ab",
                                    "caller": "fugaro-b-1@fugaro-dev.iam.gserviceaccount.com",
                                    "credential": "bitbucket-token", "purpose": "github_read",
                                    "tier": "", "status": "ok"}})
        want = classes if kind == "work" else ["no_run_branch"]
        if mode == "missing-tag-refusal" and kind == "work":
            want = [c for c in want if c != "tag_ref"]
        if mode == "no-review-refusal" and kind == "review":
            want = []
        for cls in want:
            out.append({"jsonPayload": {"message": "credsvc: audit", "run": rid,
                                        "repository": "bitbucket/workspace-a/repo-a",
                                        "job": "fugaro-workspace-a-repo-a-web-0123456789ab",
                                        "caller": "fugaro-b-1@fugaro-dev.iam.gserviceaccount.com",
                                        "credential": "bitbucket-token", "purpose": "git",
                                        "tier": "", "status": cls}})
    emit(json.dumps(out) + "\n")
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_curl() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A stand-in for `curl`, for scripts/live-checks/check191.sh's test only: the
# outcome node the check writes before it launches the ended-run probe, and the
# read-back and delete that follow it. It never leaves the process.
set -euo pipefail
printf 'curl %s\n' "$*" >>"$FAKE_LOG_DIR/curl.calls"
mode=${FAKE_CURL_MODE:-ok}
method=GET
url=""
for a in "$@"; do
  case "$a" in
  -X) ;;
  PUT | DELETE) method=$a ;;
  http://* | https://*) url=$a ;;
  esac
done
case "$url" in
*"/outcomes/"*)
  case "$method" in
  PUT)
    if [ "$mode" = outcome-refused ]; then
      echo "curl: (22) The requested URL returned error: 403" >&2
      exit 22
    fi
    ;;
  DELETE) ;;
  GET) printf '{"status":"succeeded","requestedBy":"check191"}\n' ;;
  esac
  ;;
esac
CURL_EOF
  chmod +x "$1/bin/curl"
}

write_fake_git() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/git" <<'GIT_EOF'
#!/usr/bin/env bash
# A stand-in for `git`: the check uses it only to read the sandbox checkout's
# own remote, and every call is recorded.
set -euo pipefail
printf 'git %s\n' "$*" >>"$FAKE_LOG_DIR/git.calls"
case "${1:-}" in
remote) echo "https://bitbucket.org/workspace-a/repo-a.git" ;;
esac
GIT_EOF
  chmod +x "$1/bin/git"
}

# ------------------------------------------------------------------- the runs
run191() { # dir extra-env...
  local dir=$1
  shift
  mkdir -p "$dir/state" "$dir/logs" "$dir/home/.config/fugaro/projects"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/curl.calls"
  : >"$dir/logs/git.calls"
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
    CHECK191_WAIT_SECONDS=0 CHECK191_POLL_SECONDS=0 \
    ${1+"$@"} \
    bash "$script" >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

run_copy() { # dir script extra-env...
  local dir=$1 copy=$2
  shift 2
  mkdir -p "$dir/state" "$dir/logs" "$dir/home/.config/fugaro/projects"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/curl.calls"
  : >"$dir/logs/git.calls"
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
    CHECK191_WAIT_SECONDS=0 CHECK191_POLL_SECONDS=0 \
    ${1+"$@"} \
    bash "$copy" >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

prepare() { # dir
  mkdir -p "$1/home" "$1/tmp" "$1/state"
  write_fake_fugaro "$1"
  write_fake_gcloud "$1"
  write_fake_curl "$1"
  write_fake_git "$1"
  # The published node: the repository is admitted with proxy: true.
  cat >"$1/state/project-policy.json" <<'POLICY_EOF'
{"project":"fugaro","policy":{"bitbucket_token":{"allow_repos":["bitbucket/workspace-a/repo-a"],"proxy":true}}}
POLICY_EOF
  # doctor's --json answer, with the repository's own checks: the after-flip
  # check reads bitbucket-direct out of it.
  cat >"$1/state/doctor.json" <<'DOCTOR_EOF'
{"ok":true,"checks":[{"id":"bitbucket-direct","ok":true},{"id":"creds-service","ok":true}],"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}
DOCTOR_EOF
  # The repository's jobs, as `gcloud run jobs list --format=json` answers them:
  # the v1 Job shape, whose container env and service account are at
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
  # The secret's own IAM policy: after the flip the job accounts hold no reading
  # role on it.
  cat >"$1/state/secret-policy.json" <<'IAM_EOF'
{"bindings":[{"role":"roles/secretmanager.secretAccessor","members":["serviceAccount:fugaro-credentials@fugaro-dev.iam.gserviceaccount.com"]}]}
IAM_EOF
}

# --------------------------------------------------------------- the tests
test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run191 "$dir" CHECK191_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 191 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version"
  expect_contains "$out" "FACT project-region: PASS — fugaro-dev/us-east5" "dry run: the hard-coded project"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker fact"
  expect_contains "$out" "FACT restore: PASS — nothing of this check's was created" "dry run: nothing to delete"
  expect_contains "$out" "CHECK 191 RESULT END (exit 0)" "dry run: the end line"
  expect_contains "$err" "the throwaway run itself" "dry run: the plan names the run it would launch"
  # Nothing was launched, written or deleted.
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run " "dry run: no run launched"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "logging read" "dry run: no audit read"
  expect_absent "$(cat "$dir/logs/curl.calls")" "PUT" "dry run: no outcome written"
  [ -n "$scratch" ] || fail "dry run: the script did not say where its scratch directory is"
  if [ -d "$scratch" ]; then fail "dry run: the scratch directory $scratch survived"; fi
}

test_project_guard() {
  local dir=$work/other
  prepare "$dir"
  run191 "$dir" FAKE_FUGARO_MODE=other CHECK191_DRY_RUN=1
  expect_status "$status" 1 "project guard"
  expect_contains "$err" "not the fugaro-dev installation" "project guard: the stop"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "project guard: nothing was read of another project"
  if [ -d "$scratch" ]; then fail "project guard: the scratch directory survived"; fi
}

test_repo_guard() {
  local dir=$work/norepo
  prepare "$dir"
  run191 "$dir" CHECK191_REPO=belong/app CHECK191_DRY_RUN=1
  expect_status "$status" 1 "repo guard"
  expect_contains "$err" "belong" "repo guard: the stop names what it refused"
  if [ -d "$scratch" ]; then fail "repo guard: the scratch directory survived"; fi
}

# The red step, as the plan states it: the project guard is what stops a run
# pointed at another installation. The copy below has the guard's own stop
# removed, and the only thing that can then stop it is the guard — so this test
# fails the moment the guard stops being load-bearing (for instance when the
# project line stops being read at all, or a later step's failure is mistaken
# for the guard's).
test_the_project_guard_is_what_stops_a_foreign_installation() {
  local dir=$work/guard
  prepare "$dir"
  local copy=$dir/check191-noguard.sh
  sed '/not the fugaro-dev installation/d' "$script" >"$copy" 2>/dev/null || true
  if cmp -s "$script" "$copy"; then
    fail "red step: the copy is the script: the guard's own stop line was not found to remove"
    return 0
  fi
  run_copy "$dir" "$copy" FAKE_FUGARO_MODE=other CHECK191_DRY_RUN=1
  # Without the guard the run goes on: it reads the published node of a project
  # the check never should have looked at.
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "policy show" "red step: the run went past the guard"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run191 "$dir"
  expect_status "$status" 0 "happy path"
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
    "FACT refusal-before-pack: PASS" \
    "FACT review-run: PASS" \
    "FACT ended-identity: PASS" \
    "FACT restore: PASS" \
    "FACT scratch-removed: PASS"; do
    expect_contains "$out" "$want" "happy path: $want"
  done
  # The outcome node the ended-run probe needs was written and then deleted, and
  # the read-back of the delete is the fact.
  local calls
  calls=$(cat "$dir/logs/curl.calls")
  expect_contains "$calls" "-X PUT" "happy path: the outcome was written"
  expect_contains "$calls" "-X DELETE" "happy path: the outcome was deleted"
  expect_contains "$calls" "/outcomes/" "happy path: the node is the outcome the service reads"
  # No token, no credential value, and no write of any cloud state but that node.
  expect_absent "$out" "FAKE-ACCESS-TOKEN" "happy path: the access token is not in the block"
  expect_absent "$out" "FAKE-BITBUCKET-TOKEN" "happy path: no Bitbucket token"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "--project=fugaro-dev" "happy path: every gcloud call names the installation's project"
}

test_a_run_that_did_not_succeed_is_a_failure() {
  local dir=$work/runfailed
  prepare "$dir"
  run191 "$dir" FAKE_FUGARO_MODE=run-failed
  expect_status "$status" 1 "a failed run"
  expect_contains "$out" "FACT run-outcome: FAIL" "a failed run: the fact"
}

test_a_forbidden_attempt_that_was_not_audited_is_a_failure() {
  local dir=$work/unaudited
  prepare "$dir"
  run191 "$dir" FAKE_GCLOUD_MODE=missing-tag-refusal
  expect_status "$status" 1 "an unaudited refusal"
  expect_contains "$out" "FACT refusal-tag-ref: FAIL" "an unaudited refusal: the fact names the class that is missing"
}

test_a_forbidden_attempt_the_run_did_not_report_is_a_failure() {
  local dir=$work/unreported
  prepare "$dir"
  run191 "$dir" FAKE_FUGARO_MODE=unreported
  expect_status "$status" 1 "an unreported refusal"
  expect_contains "$out" "FACT refusal-tag-ref: FAIL" "an unreported refusal: the fact names the attempt the run reported as allowed"
}

test_the_review_runs_push_refusal_is_asserted() {
  local dir=$work/review
  prepare "$dir"
  run191 "$dir" FAKE_GCLOUD_MODE=no-review-refusal
  expect_status "$status" 1 "the review run's push"
  expect_contains "$out" "FACT review-run: FAIL" "the review run's push: the fact"
}

test_an_ended_run_that_was_served_is_a_failure() {
  local dir=$work/endedserved
  prepare "$dir"
  # The outcome write is refused: the check then cannot make the probe at all,
  # and says so rather than recording the row as measured.
  run191 "$dir" FAKE_CURL_MODE=outcome-refused
  expect_status "$status" 2 "an outcome that could not be written"
  expect_contains "$out" "FACT ended-identity: FAIL" "an unwritable outcome: the fact"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro run --repo workspace-a/repo-a --run-id chk191-ended" "an unwritable outcome: no probe was launched"
}

test_after_flip_dry_run() {
  local dir=$work/afterdry
  prepare "$dir"
  run191 "$dir" CHECK191_DRY_RUN=1
  status=0
  env PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_GCLOUD_STATE="$dir/state" FAKE_FUGARO_STATE="$dir/state" \
    CHECK191_REPO=workspace-a/repo-a CHECK191_PLAIN_REPO=workspace-a/plain \
    CHECK191_WAIT_SECONDS=0 CHECK191_POLL_SECONDS=0 \
    bash "$script" --after-flip --dry-run >"$dir/out192" 2>"$dir/err192" || status=$?
  local out192 err192
  out192=$(cat "$dir/out192")
  err192=$(cat "$dir/err192")
  expect_status "$status" 0 "after-flip dry run"
  expect_contains "$out192" "CHECK 192 RESULT" "after-flip dry run: the block"
  expect_contains "$out192" "FACT mount: PASS" "after-flip dry run: the mount row is planned"
  expect_contains "$out192" "FACT accessor: PASS" "after-flip dry run: the accessor row is planned"
  expect_contains "$err192" "gcloud secrets get-iam-policy bitbucket-token" "after-flip dry run: the accessor read is printed"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "after-flip dry run: nothing was read"
}

test_after_flip_happy_path() {
  local dir=$work/after
  prepare "$dir"
  run192 "$dir"
  expect_status "$status" 0 "after-flip"
  for want in \
    "FACT jobs: PASS" \
    "FACT mount: PASS" \
    "FACT accessor: PASS" \
    "FACT doctor: PASS" \
    "FACT run-still-works: PASS" \
    "FACT plain-repo: PASS" \
    "FACT restore: PASS"; do
    expect_contains "$out" "$want" "after-flip: $want"
  done
  expect_contains "$out" "FACT mount: PASS — 1 job of workspace-a/repo-a carries FUGARO_REMOTE and none mounts bitbucket-token" \
    "after-flip: the mount row counts the repository's jobs"
}

test_after_flip_a_job_that_still_mounts_the_token_is_a_failure() {
  local dir=$work/mounted
  prepare "$dir"
  python3 - "$dir/state/jobs.json" <<'PY'
import json, sys
path = sys.argv[1]
jobs = json.load(open(path))
spec = jobs[0]["spec"]["template"]["spec"]["template"]["spec"]
spec["containers"][0]["env"].append({"name": "FUGARO_BITBUCKET_TOKEN",
                                    "valueSource": {"secretKeyRef": {"secret": "bitbucket-token", "version": "latest"}}})
open(path, "w").write(json.dumps(jobs))
PY
  run192 "$dir"
  expect_status "$status" 1 "a job that still mounts the token"
  expect_contains "$out" "FACT mount: FAIL" "a stale mount: the fact"
}

test_after_flip_an_account_that_still_has_the_accessor_is_a_failure() {
  local dir=$work/accessor
  prepare "$dir"
  cat >"$dir/state/secret-policy.json" <<'IAM_EOF'
{"bindings":[{"role":"roles/secretmanager.secretAccessor","members":["serviceAccount:fugaro-credentials@fugaro-dev.iam.gserviceaccount.com","serviceAccount:fugaro-b-1@fugaro-dev.iam.gserviceaccount.com"]}]}
IAM_EOF
  run192 "$dir"
  expect_status "$status" 1 "an account that still holds the accessor"
  expect_contains "$out" "FACT accessor: FAIL" "a live accessor: the fact"
}

test_after_flip_a_doctor_finding_is_a_failure() {
  local dir=$work/doctorwarn
  prepare "$dir"
  cat >"$dir/state/doctor.json" <<'DOCTOR_EOF'
{"ok":false,"checks":[{"id":"bitbucket-direct","ok":false,"severity":"warning","problem":"workspace-a/repo-a is proxied and flipped, and fugaro-workspace-a-repo-a-web-0123456789ab still mounts bitbucket-token"}],"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}
DOCTOR_EOF
  run192 "$dir"
  expect_status "$status" 1 "doctor's bitbucket-direct warning"
  expect_contains "$out" "FACT doctor: FAIL" "a doctor finding: the fact"
}

run192() { # dir extra-env...
  local dir=$1
  shift
  mkdir -p "$dir/state" "$dir/logs" "$dir/home/.config/fugaro/projects"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/curl.calls"
  : >"$dir/logs/git.calls"
  status=0
  env \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_GCLOUD_STATE="$dir/state" \
    FAKE_FUGARO_STATE="$dir/state" FAKE_CURL_STATE="$dir/state" \
    CHECK191_REPO=workspace-a/repo-a CHECK191_PLAIN_REPO=workspace-a/plain \
    CHECK191_WAIT_SECONDS=0 CHECK191_POLL_SECONDS=0 \
    ${1+"$@"} \
    bash "$script" --after-flip >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i (each test works in its own directory under
# $work), CHECK_TEST_ONLY=name runs that one test alone (for a solo rerun of a
# flaky-looking one). Neither set: all run.
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
t test_project_guard
t test_repo_guard
t test_the_project_guard_is_what_stops_a_foreign_installation
t test_happy_path
t test_a_run_that_did_not_succeed_is_a_failure
t test_a_forbidden_attempt_that_was_not_audited_is_a_failure
t test_a_forbidden_attempt_the_run_did_not_report_is_a_failure
t test_the_review_runs_push_refusal_is_asserted
t test_an_ended_run_that_was_served_is_a_failure
t test_after_flip_dry_run
t test_after_flip_happy_path
t test_after_flip_a_job_that_still_mounts_the_token_is_a_failure
t test_after_flip_an_account_that_still_has_the_accessor_is_a_failure
t test_after_flip_a_doctor_finding_is_a_failure

if [ "$failures" -ne 0 ]; then
  echo "check191_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check191_test.sh: ok"
