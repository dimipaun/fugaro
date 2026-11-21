#!/usr/bin/env bash
# Tests for scripts/live-checks/check-580.sh, with fake `fugaro`, `gcloud` and
# `git` on PATH: NOTHING here reaches a cloud, a project, a repository or a run,
# no agent runs a live command, and the fakes record every call they are given —
# with the directory it was made in — so a case can assert what the check did and
# what it did NOT do (above all that it never writes: the check launches one run
# and removes it never, and every other command it makes is a read).
#
# The fakes are a small model of one installation and its sandbox: the fake
# `git` answers the checkout's origin, the fake `fugaro` answers the reads
# (version, doctor, the published policy), launches the run the case asked for
# and prints a listing built from the row template the case wrote, and the fake
# `gcloud` serves the job list and the credentials service's own lines. Every
# document a case bends is a file the case wrote — a policy with no profile, two
# profiles, another provider, no claude_accounts or no budget mode; a job list whose
# jobs carry no service URL or a Bitbucket token; a row that did not push, names
# no pull request, ran on another job or had not finished; a service log with no
# git line, no forward line, another credential, or two forward lines whose
# earliest is not the first in the document.
#
# Usage: bash scripts/live-checks/check-580_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-580.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK580_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK580_TEST_HEAD:-1200}")" ;;
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

# One installation the cases read and bend: the names are what the rows quote.
install=chk580-test
gcp_project=chk580-test-proj
region=us-east5
repo=bb-owner/chk580-sandbox
origin=https://bitbucket.org/bb-owner/chk580-sandbox.git
profile=bb
job='fugaro-p-bb-bitbucket-7f3a2b'
other_job='fugaro-p-bb-github-9c1d4e'
run_base=20261117-120000
run_id=$run_base-0580
# credential_origin is the origin a token-based clone leaves behind: a GitHub URL
# whose userinfo carries a credential, split by a `/`, which is where a strip that
# cannot cross one leaves it in place. What it holds is not a credential and opens
# nothing: it is the word the case below asserts never reaches the output, so one
# that appears was printed by this check and by nothing else (Check 480 pins the
# rule this check now shares, scripts/contract_checks_test.go's assertNoCredential).
credential_origin='https://x-access-token:LEAK-CANARY/with-a-slash@github.com/acme/sandbox.git'
credential_words='x-access-token LEAK-CANARY with-a-slash'

# --------------------------------------------------------------------- fakes
write_fake_git() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/git" <<'GIT_EOF'
#!/usr/bin/env bash
# A stand-in for `git`, for scripts/live-checks/check-580.sh's test only. The
# check makes exactly one git call — the checkout's own origin — and this answers
# it. Nothing here touches a repository.
set -euo pipefail
printf 'git %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/git.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
-C)
  shift 2
  ;;
esac
case "${1:-}" in
remote)
  if [ "${2:-}" != "get-url" ]; then
    echo "git: unexpected remote subcommand ${2:-}" >&2
    exit 1
  fi
  case "$mode" in
  git-no-origin)
    echo "error: No such remote 'origin'" >&2
    exit 2
    ;;
  esac
  printf '%s\n' "${FAKE_ORIGIN:-}"
  ;;
*)
  echo "git: unexpected ${1:-}" >&2
  exit 1
  ;;
esac
GIT_EOF
  chmod +x "$1/bin/git"
}

write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check-580.sh's test only. It
# answers the account read, the job list and the credentials service's own lines
# — all three reads, none of them a write — and every call is logged with the
# directory it was made in, so a case can prove the check wrote nothing.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
rid=$(cat "$FAKE_STATE_DIR/run-id" 2>/dev/null || true)
case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    printf '%s\n' "${FAKE_ACCOUNT:-chk580-owner@example.com}"
  fi
  ;;
run)
  if [ "${2:-}" != "jobs" ] || [ "${3:-}" != "list" ]; then
    echo "gcloud: unexpected run subcommand ${2:-} ${3:-}" >&2
    exit 1
  fi
  case "$mode" in
  jobs-fails)
    echo "ERROR: (gcloud.run.jobs.list) PERMISSION_DENIED: the caller does not have permission" >&2
    exit 1
    ;;
  jobs-garbage)
    printf 'not a document\n'
    ;;
  *)
    sed "s/@RID@/$rid/g" "$FAKE_JSON_DIR/${FAKE_JOBS_FILE:-jobs.json}"
    ;;
  esac
  ;;
logging)
  if [ "${2:-}" != "read" ]; then
    echo "gcloud: unexpected logging subcommand ${2:-}" >&2
    exit 1
  fi
  case "$mode" in
  log-fails)
    echo "ERROR: (gcloud.logging.read) PERMISSION_DENIED: the caller does not have permission" >&2
    exit 1
    ;;
  log-empty)
    printf '[]\n'
    ;;
  *)
    # The real `gcloud logging read` answers the NEWEST --limit lines unless
    # --order=asc is asked for, so this stand-in honours both flags: the fixture
    # is written oldest-first, and only a read that says `asc` is handed its head.
    # Without that, a check reading the default answer would decide the run's
    # FIRST model call on a later one, and no case here could catch it.
    limit=200
    order=desc
    while [ $# -gt 0 ]; do
      case "$1" in
      --limit=*) limit=${1#--limit=} ;;
      --limit) limit=${2:-200}; shift ;;
      --order=*) order=${1#--order=} ;;
      --order) order=${2:-desc}; shift ;;
      esac
      shift
    done
    python3 - "$FAKE_JSON_DIR/${FAKE_LOG_FILE:-log.json}" "$limit" "$order" "$rid" <<'PY'
import json, sys
doc = json.loads(open(sys.argv[1]).read().replace("@RID@", sys.argv[4]))
limit = int(sys.argv[2]) if sys.argv[2].isdigit() else len(doc)
if sys.argv[3].lower() != "asc":
    doc = doc[::-1]
json.dump(doc[:limit], sys.stdout)
PY
    ;;
  esac
  ;;
*)
  echo "gcloud: unexpected subcommand ${1:-}" >&2
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
# A stand-in for `fugaro`, for scripts/live-checks/check-580.sh's test only. The
# reads come from files the case wrote; `run` records the run id it was given
# (which is what makes the listing below carry that run) and returns AT LAUNCH
# the way the real one does; and every call is logged with the directory it was
# made in.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
version)
  printf 'fugaro 9.9.9-test (test build)\n'
  ;;
doctor)
  case "$mode" in
  doctor-hangs) sleep 30 ;;
  doctor-garbage) printf 'not a document\n' ;;
  *) cat "$FAKE_JSON_DIR/doctor.json" ;;
  esac
  # The real doctor exits 1 whenever any of its checks is not ok and prints its
  # document all the same: a timeout, an exit status or a document are three
  # different answers, and the check reads them as three.
  exit "${FAKE_DOCTOR_STATUS:-0}"
  ;;
project)
  case "$mode" in
  policy-fails)
    echo "ERROR: fugaro: the published policy could not be read" >&2
    exit 1
    ;;
  policy-garbage)
    printf 'not a document\n'
    ;;
  *)
    cat "$FAKE_JSON_DIR/${FAKE_POLICY_FILE:-policy.json}"
    ;;
  esac
  ;;
run)
  case "$mode" in
  run-fails)
    echo "ERROR: fugaro: ${FAKE_RUN_REPO:-bb-owner/chk580-sandbox} is not enrolled in the project (fugaro project repos add)" >&2
    exit 1
    ;;
  run-hangs) sleep 30 ;;
  esac
  rid=""
  task=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --run-id)
      rid=$2
      shift 2
      ;;
    --task-file)
      task=$2
      shift 2
      ;;
    *) shift ;;
    esac
  done
  printf '%s\n' "$rid" >"$FAKE_STATE_DIR/run-id"
  # The task the launcher was handed, kept where the case can read it: the check
  # removes its scratch directory at the end, the task file with it, so a case
  # that wants to see the brief has to take a copy here.
  if [ -n "$task" ]; then cp "$task" "$FAKE_STATE_DIR/launched-task"; fi
  printf '{"run_id":"%s","launched":true}\n' "$rid"
  ;;
runs)
  case "$mode" in
  listing-fails)
    echo "ERROR: fugaro: the run registry could not be read" >&2
    exit 1
    ;;
  esac
  rid=$(cat "$FAKE_STATE_DIR/run-id" 2>/dev/null || true)
  if [ -z "$rid" ]; then
    printf '[]\n'
    exit 0
  fi
  case "$mode" in
  listing-empty)
    printf '[]\n'
    exit 0
    ;;
  esac
  sed "s/@RID@/$rid/g" "$FAKE_JSON_DIR/${FAKE_ROW_FILE:-row.json}"
  ;;
*)
  echo "fugaro: unexpected ${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# The documents the check reads and the rows it forms a verdict on. One file per
# shape, so a case bends exactly the one it is about.
write_json() { # dir
  mkdir -p "$1/json"
  cat >"$1/json/doctor.json" <<JSON
{"ok":true,"checks":[{"name":"credentials","status":"ok"}],"project":{"name":"$install","gcp_project":"$gcp_project","region":"$region"}}
JSON
  # The published policy of a project that admits this repository to exactly one
  # Bitbucket profile, whose one account is `work`.
  cat >"$1/json/policy.json" <<JSON
{"project":"$install","policy":{"version":3,"allow_repos":["bitbucket/$repo"],
 "budget":{"mode":"enforce","per_run_usd":5,"allowed_models":["claude-sonnet-5"],"max_run_tokens":400000},
 "profiles":{"$profile":{"provider":"bitbucket","cpu":2,"memory":"4Gi","timeout_class":"standard","claude_accounts":["work"]}},
 "accounts":{"work":{"allow_repos":["bitbucket/$repo"]}}}}
JSON
  # The policy variants, each one bend of exactly one fact. `policy-no-profile`
  # gives the profile an allow_repos of its own that does not admit this
  # repository (which is what makes it the profile's list and not the project's);
  # `policy-two-profiles` adds a second profile with the same admission, which
  # leaves which one the repository selected unreadable.
  python3 - "$1/json" "$profile" <<'PY'
import copy, json, os, sys
d = sys.argv[1]
doc = json.load(open(os.path.join(d, "policy.json")))


def out(name, mutate):
    clone = copy.deepcopy(doc)
    mutate(clone)
    json.dump(clone, open(os.path.join(d, name), "w"))


out("policy-no-profile.json",
    lambda x: x["policy"]["profiles"][sys.argv[2]].__setitem__("allow_repos", ["bitbucket/other-owner/other"]))
out("policy-no-accounts.json", lambda x: x["policy"]["profiles"][sys.argv[2]].__setitem__("claude_accounts", []))
def other_provider(x):
    # A profile of ANOTHER provider that writes no allow_repos of its own, so it
    # falls back to the project's list — which names a bitbucket repository no
    # github profile admits (projectpolicy.Admits gates on the provider first).
    x["policy"]["profiles"]["github-only"] = {
        "provider": "github", "cpu": 2, "memory": "4Gi", "timeout_class": "standard"}


out("policy-other-provider.json", other_provider)


def only_other_provider(x):
    other_provider(x)
    x["policy"]["profiles"].pop(sys.argv[2])


out("policy-other-provider-only.json", only_other_provider)
out("policy-no-mode.json", lambda x: x["policy"]["budget"].pop("mode"))
out("policy-not-admitted.json", lambda x: x["policy"].__setitem__("allow_repos", ["bitbucket/other-owner/elsewhere"]))


def two(x):
    x["policy"]["profiles"]["bb-two"] = copy.deepcopy(x["policy"]["profiles"][sys.argv[2]])


out("policy-two-profiles.json", two)
PY
  # The profile's own shared Bitbucket job: the service URL present, no token.
  cat >"$1/json/jobs.json" <<JSON
[{"metadata":{"name":"$job","labels":{"fugaro":"p"}},
  "spec":{"template":{"spec":{"template":{"spec":{"containers":[{"image":"us-east5-docker.pkg.dev/$gcp_project/fugaro/agent:20261117",
   "env":[{"name":"FUGARO_CREDENTIALS_SERVICE_URL","value":"https://fugaro-credentials-abc.a.run.app"},
          {"name":"FUGARO_PROJECT","value":"$install"}]}]}}}}}},
 {"metadata":{"name":"fugaro-p-github-only-abc123"},
  "spec":{"template":{"spec":{"template":{"spec":{"containers":[{"image":"x","env":[]}]}}}}}}]
JSON
  # The profile's job without the service URL: the switch is what makes a
  # Bitbucket shared job reach anything at all, and this is a job that names no
  # service.
  python3 - "$1/json/jobs.json" "$1/json/jobs-no-url.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
spec = doc[0]["spec"]["template"]["spec"]["template"]["spec"]
spec["containers"][0]["env"] = [e for e in spec["containers"][0]["env"] if e["name"] != "FUGARO_CREDENTIALS_SERVICE_URL"]
json.dump(doc, open(sys.argv[2], "w"))
PY
  sed 's/{"name":"FUGARO_PROJECT","value":"'"$install"'"}/{"name":"FUGARO_PROJECT","value":"'"$install"'"},{"name":"FUGARO_BITBUCKET_TOKEN","value":"x"}/' \
    "$1/json/jobs.json" >"$1/json/jobs-token.json"
  # A second job of the profile that carries no service URL: the switch is
  # per-profile, so one job without it is the profile's jobs not being flipped.
  python3 - "$1/json/jobs.json" "$1/json/jobs-two.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
second = json.loads(json.dumps(doc[0]))
second["metadata"]["name"] = "fugaro-p-bb-bitbucket-1a2b3c"
spec = second["spec"]["template"]["spec"]["template"]["spec"]
spec["containers"][0]["env"] = [e for e in spec["containers"][0]["env"] if e["name"] != "FUGARO_CREDENTIALS_SERVICE_URL"]
doc.append(second)
json.dump(doc, open(sys.argv[2], "w"))
PY
  # The same job under the truncated name Terraform gives a long profile and
  # provider (fugaro-p-<profile>-bitbucke-<hash>), found by its labels.
  python3 - "$1/json/jobs.json" "$1/json/jobs-truncated.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
doc[0]["metadata"]["name"] = "fugaro-p-bb-bitbucke-7f3a2b"
doc[0]["metadata"]["labels"] = {"fugaro": "p", "fugaro_profile": "bb", "fugaro_provider": "bitbucket"}
json.dump(doc, open(sys.argv[2], "w"))
PY
  # The run's own row: it ended succeeded, pushed and opened a pull request, and
  # its execution names the profile's shared job.
  cat >"$1/json/row.json" <<JSON
[{"run_id":"@RID@","repo":"$repo","status":"succeeded","terminal":true,"outcome":"ready","pushed":true,
  "pr":42,"pr_url":"https://bitbucket.org/$repo/pull-requests/42",
  "execution":"projects/$gcp_project/locations/$region/jobs/$job/executions/chk580-run-1"}]
JSON
  # The row variants, each one bend of exactly one fact: a run that did not push,
  # one that names no pull request, one that failed, one that ran on another job,
  # one whose record names no execution at all, and one still in flight.
  python3 - "$1/json" "$job" "$other_job" <<'PY'
import copy, json, os, sys
d, job, other = sys.argv[1], sys.argv[2], sys.argv[3]
doc = json.load(open(os.path.join(d, "row.json")))


def out(name, mutate):
    clone = copy.deepcopy(doc)
    mutate(clone[0])
    json.dump(clone, open(os.path.join(d, name), "w"))


out("row-no-push.json", lambda r: r.__setitem__("pushed", False))
out("row-no-pr.json", lambda r: (r.pop("pr"), r.pop("pr_url")))
out("row-failed.json", lambda r: (r.__setitem__("status", "failed"), r.__setitem__("pushed", False)))
out("row-other-job.json", lambda r: r.__setitem__("execution", r["execution"].replace(job, other)))
out("row-no-execution.json", lambda r: r.pop("execution"))
out("row-inflight.json", lambda r: r.__setitem__("terminal", False))
PY
  # The credentials service's own lines for the run: two served git requests and
  # one served model call, whose credential is the profile's `work` account.
  cat >"$1/json/log.json" <<JSON
[{"timestamp":"2026-11-17T12:00:03Z","jsonPayload":{"msg":"credsvc: audit","run":"@RID@","repository":"$repo",
   "job":"$job","caller":"chk580-run@$gcp_project.iam.gserviceaccount.com",
   "credential":"bitbucket-token","purpose":"git","tier":"","status":"ok"}},
 {"timestamp":"2026-11-17T12:00:05Z","jsonPayload":{"msg":"credsvc: audit","run":"@RID@","repository":"$repo",
   "job":"$job","caller":"chk580-run@$gcp_project.iam.gserviceaccount.com",
   "credential":"bitbucket-token","purpose":"git","tier":"","status":"ok"}},
 {"timestamp":"2026-11-17T12:00:09Z","jsonPayload":{"msg":"credsvc: audit","run":"@RID@","repository":"$repo",
   "job":"$job","caller":"chk580-run@$gcp_project.iam.gserviceaccount.com",
   "credential":"work","purpose":"forward","tier":"A","status":"ok"}}]
JSON
  python3 - "$1/json/log.json" "$1/json/log-no-forward.json" <<'PY'
import json, sys
doc = [e for e in json.load(open(sys.argv[1])) if e["jsonPayload"].get("purpose") != "forward"]
json.dump(doc, open(sys.argv[2], "w"))
PY
  python3 - "$1/json/log.json" "$1/json/log-other-credential.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
for e in doc:
    if e["jsonPayload"].get("purpose") == "forward":
        e["jsonPayload"]["credential"] = "anthropic-api-key"
        e["jsonPayload"]["tier"] = "B"
json.dump(doc, open(sys.argv[2], "w"))
PY
  # Two served model calls, the EARLIER one last in the document: the row has to
  # read the run's FIRST call by its own time and not by the document's order.
  python3 - "$1/json/log.json" "$1/json/log-two-forwards.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
switched = json.loads(json.dumps([e for e in doc if e["jsonPayload"].get("purpose") == "forward"][0]))
switched["timestamp"] = "2026-11-17T12:00:20Z"
switched["jsonPayload"]["credential"] = "spare-account"
doc.append(switched)
json.dump(doc, open(sys.argv[2], "w"))
PY
  # A run whose first model calls are the profile's account and whose later ones
  # are not, with the push at the end: read from the run's OWN start the first
  # forward line is the profile's account, and read newest-first (gcloud's own
  # default) the first forward line in the answer belongs to a later call — the
  # reading that makes this row a false FAIL, or a false PASS. The case below
  # bends CHECK580_LOG_LIMIT to 3 so that the two answers really differ.
  python3 - "$1/json/log.json" "$1/json/log-late-switch.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
git_line = [e for e in doc if e["jsonPayload"].get("purpose") == "git"][0]
first = [e for e in doc if e["jsonPayload"].get("purpose") == "forward"][0]
late = json.loads(json.dumps(first))
late["jsonPayload"]["credential"] = "anthropic-api-key"
late["jsonPayload"]["tier"] = "B"


def line(base, stamp):
    e = json.loads(json.dumps(base))
    e["timestamp"] = stamp
    return e


json.dump([line(git_line, "2026-11-17T12:00:03Z"),
           line(first, "2026-11-17T12:00:09Z"),
           line(first, "2026-11-17T12:00:41Z"),
           line(late, "2026-11-17T12:01:10Z"),
           line(late, "2026-11-17T12:01:40Z"),
           line(late, "2026-11-17T12:02:05Z"),
           line(git_line, "2026-11-17T12:02:20Z")],
          open(sys.argv[2], "w"))
PY
  # A git line of the run that was NOT served, and a model line that was: the git
  # row must read the status and not the purpose alone.
  python3 - "$1/json/log.json" "$1/json/log-git-refused.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
for e in doc:
    if e["jsonPayload"].get("purpose") == "git":
        e["jsonPayload"]["status"] = "unavailable"
json.dump(doc, open(sys.argv[2], "w"))
PY
}

# A repository checkout: the check refuses a path that is not one, and the
# repository, the installation and the region are all read from it.
write_checkout() { # dir
  mkdir -p "$1"
  printf 'project: %s\n' "$install" >"$1/fugaro.yaml"
}

prepare() { # dir
  mkdir -p "$1/home" "$1/state" "$1/logs" "$1/tmp"
  write_fake_git "$1"
  write_fake_gcloud "$1"
  write_fake_fugaro "$1"
  write_json "$1"
  # Every document a case bends has to still be a document: a template this file
  # broke with a sed would otherwise read as an installation that wrote nothing,
  # which is a confusing way to learn that the fixture is wrong.
  local f
  for f in "$1"/json/*.json; do
    python3 -c 'import json, sys; json.load(open(sys.argv[1]))' "$f" || fail "prepare: $(basename "$f") is not a valid document"
  done
  write_checkout "$1/sandbox"
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
  mkdir -p "$dir/state" "$dir/logs" "$dir/home" "$dir/tmp"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/git.calls"
  : >"$dir/state/run-id"
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
  # The agent markers are dropped from this test's own environment: the check
  # refuses a coding agent's session by name, and the suite that runs this file
  # may itself be started from one. The case that measures that refusal sets the
  # marker again, and a variable given as an argument wins over `env -u`.
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_MODE="${FAKE_MODE:-default}" \
    FAKE_JSON_DIR="$dir/json" FAKE_STATE_DIR="$dir/state" \
    FAKE_ORIGIN="${FAKE_ORIGIN:-$origin}" FAKE_ACCOUNT="${FAKE_ACCOUNT:-}" \
    FAKE_JOBS_FILE="${FAKE_JOBS_FILE:-jobs.json}" FAKE_LOG_FILE="${FAKE_LOG_FILE:-log.json}" \
    FAKE_ROW_FILE="${FAKE_ROW_FILE:-row.json}" FAKE_POLICY_FILE="${FAKE_POLICY_FILE:-policy.json}" \
    FAKE_DOCTOR_STATUS="${FAKE_DOCTOR_STATUS:-0}" \
    CHECK580_SANDBOX="$dir/sandbox" \
    CHECK580_RUN_ID="$run_base" \
    CHECK580_RUN_SECONDS="${CHECK580_RUN_SECONDS:-20}" \
    CHECK580_POLL_SECONDS="${CHECK580_POLL_SECONDS:-1}" \
    CHECK580_LOG_WAIT_SECONDS="${CHECK580_LOG_WAIT_SECONDS:-2}" \
    CHECK580_LOG_POLL_SECONDS="${CHECK580_LOG_POLL_SECONDS:-1}" \
    CHECK580_DOCTOR_TIMEOUT_SECONDS="${CHECK580_DOCTOR_TIMEOUT_SECONDS:-20}" \
    CHECK580_CLI_TIMEOUT_SECONDS="${CHECK580_CLI_TIMEOUT_SECONDS:-20}" \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

# ------------------------------------------------------------------ assertions
assert_scratch_removed() { # dir what
  [ -n "$scratch" ] || {
    fail "$2: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$2: the scratch directory $scratch is still there"
}

# What this check creates is ONE run, and it deletes nothing: every command it
# gives fugaro or gcloud besides that run is a READ. This is the assertion that
# would catch a diff that made it write.
assert_reads_only() { # dir what
  local fugaro gcloud
  fugaro=$(cat "$1/logs/fugaro.calls")
  gcloud=$(cat "$1/logs/gcloud.calls")
  local word
  for word in "project policy publish" "project repos add" "project repos rm" "update" "setup" "restore" "runs rm" "creds set" "creds rm" "image build"; do
    expect_absent "$fugaro" "$word" "$2: fugaro '$word' is never run"
  done
  for word in "jobs create" "jobs update" "jobs delete" "jobs deploy" "run deploy" "services deploy" "logging write" "secrets versions"; do
    expect_absent "$gcloud" "$word" "$2: gcloud '$word' is never run"
  done
}

assert_no_launch() { # dir what
  expect_absent "$(cat "$1/logs/fugaro.calls")" "fugaro run " "$2: no run is launched"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check-580.sh --sandbox <a sandbox Bitbucket repository checkout>" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  assert_no_launch "$dir" "--help"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, that the run is billable, that it is theirs, and that the
  # branch and pull request it makes stay on the repository.
  expect_contains "$out" "billable" "--help: the cost of a run is stated"
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  expect_contains "$out" "gcloud logging read" "--help: how the service's lines are read back"
  expect_contains "$out" "pull request" "--help: what it creates and does not remove"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK580_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 580 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version row"
  expect_contains "$out" "FACT agent-session: PASS" "dry run: the session guard"
  expect_contains "$out" "FACT belong: PASS" "dry run: the belong guard"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore row"
  expect_contains "$out" "CHECK 580 RESULT END (exit 0)" "dry run: the end line"
  expect_contains "$out" "FACT sandbox: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$err" "BILLABLE" "dry run: the plan names the cost"
  expect_contains "$err" "first purpose 'forward' line" "dry run: the plan names what the model row reads"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "dry run: the installation is not read"
  assert_no_launch "$dir" "dry run"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "logging read" "dry run: the service's lines are not read"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the happy path"
  expect_contains "$out" "FACT sandbox: PASS — $dir/sandbox is a Fugaro repository's checkout of the BITBUCKET repository $repo, and it belongs to the $install installation ($gcp_project, region $region)" "the happy path: the sandbox row"
  expect_contains "$out" "FACT policy: PASS" "the happy path: the policy row"
  expect_contains "$out" "the profile 'bb' (provider bitbucket), which lists the model account(s) work" "the happy path: the profile and its accounts"
  expect_contains "$out" "the published budget mode is 'enforce'" "the happy path: the mode it read"
  expect_contains "$out" "FACT job: PASS — 1 shared Bitbucket job(s) of the profile 'bb' are deployed — $job" "the happy path: the job row"
  expect_contains "$out" "FACT run: PASS" "the happy path: the run row"
  expect_contains "$out" "pushed=yes) and names pull request #42 (https://bitbucket.org/$repo/pull-requests/42)" "the happy path: the row says what the run did"
  expect_contains "$out" "FACT landed: PASS — the run ($run_id) ran on '$job'" "the happy path: the landed row"
  expect_contains "$out" "FACT git: PASS — 2 served git line(s)" "the happy path: the git row"
  expect_contains "$out" "naming the credential(s) bitbucket-token" "the happy path: the git credential it read"
  expect_contains "$out" "FACT model: PASS — the run's first model call ($run_id) was served with the credential 'work', one of the profile 'bb''s claude_accounts (work)" "the happy path: the model row"
  expect_contains "$out" "FACT restore: PASS" "the happy path: the restore row"
  expect_contains "$out" "which is the owner's to settle" "the happy path: what stays behind"
  expect_contains "$out" "FACT scratch-removed: PASS" "the happy path: the scratch row"
  expect_contains "$out" "CHECK 580 RESULT END (exit 0)" "the happy path: the end line"
  assert_scratch_removed "$dir" "the happy path"
  assert_reads_only "$dir" "the happy path"
  # ONE run, launched under the id the check minted, from the checkout's own
  # repository and in the installation the checkout belongs to.
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "run --repo $repo --project $install --run-id $run_id --title Check 580: the Bitbucket shared job --task-file" "the happy path: the launch"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "runs ls --repo $repo --project $install --no-cache --json" "the happy path: the listing it polled"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "run jobs list --project=$gcp_project --region=$region --format=json" "the happy path: the job list"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "jsonPayload.run=\"$run_id\"" "the happy path: the service's lines are read for the run's own id"
  # The read asks for the run's OLDEST lines, and asks back far enough to hold the
  # run's first one: the knob (1h) is the floor here, and the run took no longer.
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "--limit=200 --order=asc" "the happy path: the lines are read oldest-first"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "--freshness=1h" "the happy path: the window is the knob when it is the wider one"
  # The check ran every command from the checkout it was given.
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "doctor --json (in $dir/sandbox)" "the happy path: the reads are made in the checkout"
  # The task file it wrote is the smallest thing that makes a run push a branch
  # and open a pull request, and its first line is the `# Task:` line the pull
  # request title comes from (the fake kept the copy it was handed, because the
  # check removes its scratch directory at the end).
  local task
  task=$dir/state/launched-task
  if [ ! -f "$task" ]; then
    fail "the happy path: the launch was handed no task file"
  else
    expect_contains "$(head -n 1 "$task")" "# Task: Check 580" "the happy path: the task's first line"
    expect_contains "$(cat "$task")" "open a pull request" "the happy path: the task asks for a pull request"
  fi
  # The pull request title and the task line are the same one: the run's own row
  # is what the register quotes, and the title the launch was given says the same.
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "--title Check 580: the Bitbucket shared job" "the happy path: the run's title"
}

test_a_run_the_sandbox_does_not_name_is_refused() {
  local dir=$work/not-bitbucket
  prepare "$dir"
  run_check "$dir" "FAKE_ORIGIN=https://github.com/acme/app.git"
  expect_status "$status" 1 "a GitHub checkout"
  # A guard that stops the check records its reason as the `aborted` row and
  # leaves the row it was reading as one that was not measured — nothing about
  # this installation was read, and the block says so rather than passing it.
  expect_contains "$err" "is not a Bitbucket repository (its host is 'github.com')" "a GitHub checkout: the refusal says which host it read"
  expect_contains "$err" "this check is the BITBUCKET shared-job half" "a GitHub checkout: the refusal says why"
  expect_contains "$out" "FACT aborted: FAIL" "a GitHub checkout: the reason is in the block"
  expect_contains "$out" "FACT sandbox: FAIL — not reached" "a GitHub checkout: the sandbox row was not measured"
  assert_no_launch "$dir" "a GitHub checkout"
  assert_scratch_removed "$dir" "a GitHub checkout"
}

test_a_credential_in_the_origin_is_never_printed() {
  local dir=$work/credentialled-origin
  prepare "$dir"
  run_check "$dir" "FAKE_ORIGIN=$credential_origin"
  expect_status "$status" 1 "a credentialled origin"
  # This is the documented mistake path — a GitHub checkout, the kind a CI or
  # token clone leaves credentialled — and both refusals print the origin, into
  # the block the register quotes and into stderr.
  local word
  for word in $credential_words; do
    expect_absent "$err" "$word" "a credentialled origin: '$word' on stderr"
    expect_absent "$out" "$word" "a credentialled origin: '$word' in the block"
  done
  # The URL itself is kept, minus its userinfo: the refusal still names the
  # repository it refused, and reads the host through the credential.
  expect_contains "$err" "https://github.com/acme/sandbox.git" "a credentialled origin: the URL is kept, without its credential"
  expect_contains "$err" "is not a Bitbucket repository (its host is 'github.com')" "a credentialled origin: the refusal reads the host through the credential"
  expect_contains "$out" "FACT aborted: FAIL" "a credentialled origin: the reason is in the block"
  assert_no_launch "$dir" "a credentialled origin"
  assert_scratch_removed "$dir" "a credentialled origin"
}

test_an_origin_that_names_no_repository_is_refused() {
  local dir=$work/odd-origin
  prepare "$dir"
  run_check "$dir" "FAKE_ORIGIN=https://bitbucket.org/"
  expect_status "$status" 1 "an origin naming no repository"
  expect_contains "$err" "is 'https://bitbucket.org/', which names no repository" "an origin naming no repository: the refusal says what it read"
  assert_no_launch "$dir" "an origin naming no repository"
}

test_a_checkout_with_no_origin_is_refused() {
  local dir=$work/no-origin
  prepare "$dir"
  run_check "$dir" FAKE_MODE=git-no-origin
  expect_status "$status" 1 "a checkout with no origin"
  expect_contains "$err" "names no origin this check could read (git exited 2)" "a checkout with no origin: the refusal says what git answered"
  expect_contains "$err" "No such remote 'origin'" "a checkout with no origin: the reason is quoted"
  assert_no_launch "$dir" "a checkout with no origin"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/no-sandbox
  prepare "$dir"
  run_check "$dir" CHECK580_SANDBOX=
  expect_status "$status" 2 "no --sandbox"
  expect_contains "$err" "Check 580 needs --sandbox" "no --sandbox: the reason"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "no --sandbox: nothing is read"
  assert_no_launch "$dir" "no --sandbox"
}

test_a_path_that_is_not_a_checkout_stops_the_check() {
  local dir=$work/not-a-checkout
  prepare "$dir"
  rm -f "$dir/sandbox/fugaro.yaml"
  run_check "$dir"
  expect_status "$status" 2 "a path that is not a checkout"
  expect_contains "$err" "carries no fugaro.yaml" "a path that is not a checkout: the reason"
  assert_no_launch "$dir" "a path that is not a checkout"
}

test_a_missing_tool_stops_before_anything_is_called() {
  local dir=$work/no-tool
  prepare "$dir"
  # A PATH that holds the fakes for everything but `fugaro`, and the real one
  # nowhere: the machine this test runs on has a real fugaro installed, and a
  # case that only deleted the fake would drive that one.
  mkdir -p "$dir/empty"
  local tool
  for tool in bash mktemp python3 sed grep tr cut head tail cat rm chmod mkdir date sleep kill ls cp; do
    ln -s "$(command -v "$tool")" "$dir/empty/$tool" 2>/dev/null || true
  done
  ln -s "$dir/bin/git" "$dir/empty/git"
  ln -s "$dir/bin/gcloud" "$dir/empty/gcloud"
  # run_check makes the call logs; this case calls the script itself, so the log
  # the assertion below reads is made here — an absent file and an empty one are
  # the same reading, and a missing directory would print a `cat` complaint.
  mkdir -p "$dir/logs"
  : >"$dir/logs/git.calls"
  status=0
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/empty" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_STATE_DIR="$dir/state" \
    CHECK580_SANDBOX="$dir/sandbox" \
    "$dir/empty/bash" "$script" >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  expect_status "$status" 2 "a missing tool"
  expect_contains "$err" "Check 580 needs 'fugaro' and it is not on PATH" "a missing tool: the reason"
  expect_absent "$(cat "$dir/logs/git.calls")" "git " "a missing tool: nothing is read"
}

test_xtrace_is_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- --xtrace
  expect_status "$status" 1 "a traced shell"
  expect_contains "$out" "FACT xtrace: FAIL" "a traced shell: the row"
  expect_contains "$out" "this shell is tracing" "a traced shell: the reason"
  assert_no_launch "$dir" "a traced shell"
  assert_scratch_removed "$dir" "a traced shell"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$out" "FACT agent-session: FAIL" "a coding agent's session: the row"
  expect_contains "$out" "is set, so this is a coding agent's session" "a coding agent's session: the marker is named"
  assert_no_launch "$dir" "a coding agent's session"
}

test_belong_is_refused_by_name() {
  local dir=$work/b-install
  prepare "$dir"
  sed "s/\"name\":\"$install\"/\"name\":\"belong\"/" "$dir/json/doctor.json" >"$dir/json/doctor.tmp"
  mv "$dir/json/doctor.tmp" "$dir/json/doctor.json"
  run_check "$dir"
  expect_status "$status" 1 "an installation named belong"
  expect_contains "$err" "belongs to belong ($gcp_project, region $region)" "an installation named belong: the refusal says what it read"
  expect_contains "$err" "this check is never run there" "an installation named belong: the refusal"
  expect_contains "$out" "FACT aborted: FAIL" "an installation named belong: the reason is in the block"
  assert_no_launch "$dir" "an installation named belong"
}

test_a_sandbox_path_naming_belong_stops_the_check() {
  local dir=$work/b-path
  prepare "$dir"
  mkdir -p "$dir/belong-checkout"
  cp "$dir/sandbox/fugaro.yaml" "$dir/belong-checkout/fugaro.yaml"
  run_check "$dir" CHECK580_SANDBOX="$dir/belong-checkout"
  expect_status "$status" 1 "a path naming belong"
  expect_contains "$out" "FACT aborted: FAIL — a value this check was given or read names belong" "a path naming belong: the guard's row"
  expect_contains "$out" "FACT belong: FAIL — not reached" "a path naming belong: the guard row was not measured"
  expect_contains "$err" "a value this check was given or read names belong" "a path naming belong: the refusal"
  expect_absent "$(cat "$dir/logs/git.calls")" "git " "a path naming belong: nothing is read"
  assert_no_launch "$dir" "a path naming belong"
}

test_a_project_naming_belong_stops_the_check() {
  local dir=$work/b-project
  prepare "$dir"
  sed "s/\"gcp_project\":\"$gcp_project\"/\"gcp_project\":\"belong\"/" "$dir/json/doctor.json" >"$dir/json/doctor.tmp"
  mv "$dir/json/doctor.tmp" "$dir/json/doctor.json"
  run_check "$dir"
  expect_status "$status" 1 "a project naming belong"
  expect_contains "$err" "belongs to $install (belong, region $region)" "a project naming belong: the refusal says what it read"
  assert_no_launch "$dir" "a project naming belong"
}

test_a_checkout_naming_belong_is_refused_by_the_repository_guard() {
  local dir=$work/b-repo
  prepare "$dir"
  run_check "$dir" "FAKE_ORIGIN=https://bitbucket.org/belong/chk580-sandbox.git"
  expect_status "$status" 1 "a repository naming belong"
  expect_contains "$out" "FACT sandbox: FAIL" "a repository naming belong: the row"
  expect_contains "$out" "names belong" "a repository naming belong: the guard"
  assert_no_launch "$dir" "a repository naming belong"
}

test_doctor_exit_1_is_an_answer() {
  local dir=$work/doctor-1
  prepare "$dir"
  # doctor exits 1 whenever any of its own checks is not ok — a warning is
  # enough — and still prints its document: that is an answer, not a failure.
  run_check "$dir" FAKE_DOCTOR_STATUS=1
  expect_status "$status" 0 "a doctor that exits 1"
  expect_contains "$out" "FACT sandbox: PASS" "a doctor that exits 1: the row is measured"
}

test_a_doctor_that_is_not_a_document_stops_the_check() {
  local dir=$work/doctor-garbage
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-garbage
  # A doctor that printed something this check cannot read is not a finding about
  # the release: the sandbox row was NOT measured, and the check says so (exit 2).
  expect_status "$status" 2 "a doctor that is not a document"
  expect_contains "$out" "FACT sandbox: FAIL — fugaro doctor --json answered without a project block" "a doctor that is not a document: the row says what it could not read"
  assert_no_launch "$dir" "a doctor that is not a document"
}

test_a_doctor_that_never_answers_stops_the_check() {
  local dir=$work/doctor-slow
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-hangs CHECK580_DOCTOR_TIMEOUT_SECONDS=2
  expect_status "$status" 2 "a doctor that never answers"
  expect_contains "$out" "FACT sandbox: FAIL — fugaro doctor --json did not answer" "a doctor that never answers: the row"
  assert_no_launch "$dir" "a doctor that never answers"
}

test_a_policy_that_could_not_be_read_leaves_the_rows_unmeasured() {
  local dir=$work/policy-fails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=policy-fails
  expect_status "$status" 2 "a policy that could not be read"
  expect_contains "$out" "FACT policy: FAIL — fugaro project policy show --project $install --json did not answer" "a policy that could not be read: the row"
  expect_contains "$out" "FACT job: FAIL — there is no profile to read a shared job of" "a policy that could not be read: the job row follows it"
  expect_contains "$out" "FACT run: FAIL — nothing is launched without the profile's own shared job" "a policy that could not be read: no run is launched"
  assert_no_launch "$dir" "a policy that could not be read"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "run jobs list" "a policy that could not be read: no job list is read"
  assert_scratch_removed "$dir" "a policy that could not be read"
}

test_a_repository_the_project_does_not_admit_is_not_measured() {
  local dir=$work/not-admitted
  prepare "$dir"
  run_check "$dir" FAKE_POLICY_FILE=policy-not-admitted.json
  expect_status "$status" 2 "a repository the project does not admit"
  expect_contains "$out" "FACT policy: FAIL — the published policy of $install does not admit $repo to the PROJECT" "a repository the project does not admit: the row"
  expect_contains "$out" "fugaro project repos add" "a repository the project does not admit: the fix is named"
  assert_no_launch "$dir" "a repository the project does not admit"
}

test_a_repository_no_profile_admits_is_a_finding() {
  local dir=$work/no-profile
  prepare "$dir"
  run_check "$dir" FAKE_POLICY_FILE=policy-no-profile.json
  expect_status "$status" 1 "a repository no profile admits"
  expect_contains "$out" "FACT policy: FAIL — $repo is admitted to the project and to NO profile of $install's published policy" "a repository no profile admits: the row"
  assert_no_launch "$dir" "a repository no profile admits"
}

test_a_repository_two_profiles_admit_is_not_measured() {
  local dir=$work/two-profiles
  prepare "$dir"
  run_check "$dir" FAKE_POLICY_FILE=policy-two-profiles.json
  expect_status "$status" 2 "a repository two profiles admit"
  expect_contains "$out" "FACT policy: FAIL — 2 profiles of $install's published policy admit $repo" "a repository two profiles admit: the row"
  expect_contains "$out" "which one its own fugaro.yaml selects is not a fact of the policy" "a repository two profiles admit: why it refuses"
  assert_no_launch "$dir" "a repository two profiles admit"
}

test_a_profile_of_another_provider_does_not_admit_the_repository() {
  local dir=$work/other-provider
  prepare "$dir"
  run_check "$dir" FAKE_POLICY_FILE=policy-other-provider.json
  expect_status "$status" 0 "a profile of another provider beside the one that admits it"
  expect_contains "$out" "FACT policy: PASS — $repo is admitted to the profile 'bb' (provider bitbucket)" "a profile of another provider: the ONE profile that admits it"
  expect_absent "$out" "github-only" "a profile of another provider: it is not read as the one that admits the repository"
  assert_scratch_removed "$dir" "a profile of another provider beside the one that admits it"
}

test_a_policy_whose_only_profile_serves_another_provider_is_a_finding() {
  local dir=$work/other-provider-only
  prepare "$dir"
  run_check "$dir" FAKE_POLICY_FILE=policy-other-provider-only.json
  expect_status "$status" 1 "a policy whose only profile serves another provider"
  expect_contains "$out" "FACT policy: FAIL — $repo is admitted to the project and to NO profile of $install's published policy" "a policy whose only profile serves another provider: no profile admits it"
  assert_no_launch "$dir" "a policy whose only profile serves another provider"
}

test_a_profile_with_no_claude_accounts_is_a_finding() {
  local dir=$work/no-accounts
  prepare "$dir"
  run_check "$dir" FAKE_POLICY_FILE=policy-no-accounts.json
  expect_status "$status" 1 "a profile with no claude_accounts"
  expect_contains "$out" "FACT policy: FAIL — the profile 'bb' admits $repo and lists no claude_accounts" "a profile with no claude_accounts: the row"
  expect_contains "$out" "holds no Claude OAuth token (F17)" "a profile with no claude_accounts: why it matters"
  assert_no_launch "$dir" "a profile with no claude_accounts"
}

test_a_policy_with_no_budget_mode_is_not_measured() {
  local dir=$work/no-mode
  prepare "$dir"
  run_check "$dir" FAKE_POLICY_FILE=policy-no-mode.json
  expect_status "$status" 2 "a policy with no budget mode"
  expect_contains "$out" "FACT policy: FAIL — $repo is on the profile 'bb' and the policy document carries no budget mode" "a policy with no budget mode: the row"
  expect_contains "$out" "a mode the document does not carry is neither" "a policy with no budget mode: why it is not a finding"
  assert_no_launch "$dir" "a policy with no budget mode"
}

test_no_shared_job_of_the_profile_is_not_measured() {
  local dir=$work/no-shared-job
  prepare "$dir"
  python3 - "$dir/json/jobs.json" "$dir/json/jobs-only-other.json" <<'PY'
import json, sys
doc = [j for j in json.load(open(sys.argv[1])) if not j["metadata"]["name"].startswith("fugaro-p-bb-")]
json.dump(doc, open(sys.argv[2], "w"))
PY
  run_check "$dir" FAKE_JOBS_FILE=jobs-only-other.json
  expect_status "$status" 2 "no shared job of the profile"
  expect_contains "$out" "FACT job: FAIL — no job of the installation is named 'fugaro-p-bb-bitbucket…'" "no shared job of the profile: the row"
  expect_contains "$out" "serves no shared Bitbucket job" "no shared job of the profile: what it could not measure"
  assert_no_launch "$dir" "no shared job of the profile"
}

test_a_shared_job_without_the_service_url_is_a_finding() {
  local dir=$work/no-url
  prepare "$dir"
  run_check "$dir" FAKE_JOBS_FILE=jobs-no-url.json
  expect_status "$status" 1 "a shared job without the service URL"
  expect_contains "$out" "FACT job: FAIL — 0 of 1 shared Bitbucket job(s) of the profile 'bb' carry FUGARO_CREDENTIALS_SERVICE_URL" "a shared job without the service URL: the row"
  expect_contains "$out" "a Bitbucket shared job holds NO token" "a shared job without the service URL: why it matters"
  assert_no_launch "$dir" "a shared job without the service URL"
}

test_a_shared_job_that_carries_a_bitbucket_token_is_a_finding() {
  local dir=$work/token
  prepare "$dir"
  run_check "$dir" FAKE_JOBS_FILE=jobs-token.json
  expect_status "$status" 1 "a shared job carrying a Bitbucket token"
  expect_contains "$out" "FACT job: FAIL — 1 of the 1 shared Bitbucket job(s) of the profile 'bb' carry FUGARO_BITBUCKET_TOKEN" "a shared job carrying a token: the row"
  expect_contains "$out" "a Bitbucket shared job mounts no token at all (D2302)" "a shared job carrying a token: the decision it breaks"
  expect_contains "$out" "$job" "a shared job carrying a token: the jobs it read are named"
  assert_no_launch "$dir" "a shared job carrying a token"
}

test_one_job_of_the_profile_without_the_url_is_a_finding() {
  local dir=$work/two-jobs
  prepare "$dir"
  run_check "$dir" FAKE_JOBS_FILE=jobs-two.json
  expect_status "$status" 1 "one of the profile's jobs without the URL"
  expect_contains "$out" "FACT job: FAIL — 1 of 2 shared Bitbucket job(s) of the profile 'bb' carry FUGARO_CREDENTIALS_SERVICE_URL" "one job without the URL: the row"
  expect_contains "$out" "the profile's own switch and it is turned on for every job of the profile at once" "one job without the URL: the switch is per profile"
  assert_no_launch "$dir" "one of the profile's jobs without the URL"
}

test_a_job_with_a_truncated_name_is_found_by_its_labels() {
  local dir=$work/truncated-job
  prepare "$dir"
  run_check "$dir" FAKE_JOBS_FILE=jobs-truncated.json
  expect_contains "$out" "FACT job: PASS" "a truncated job name: found by the profile and provider labels"
}

test_a_jobs_list_that_could_not_be_read_is_not_measured() {
  local dir=$work/jobs-fail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=jobs-fails
  expect_status "$status" 2 "a job list that could not be read"
  expect_contains "$out" "FACT job: FAIL — gcloud run jobs list did not answer" "a job list that could not be read: the row"
  expect_contains "$out" "a read that did not answer is not an installation with none" "a job list that could not be read: what it refuses to conclude"
  assert_no_launch "$dir" "a job list that could not be read"
}

test_a_jobs_list_that_is_not_a_document_is_not_measured() {
  local dir=$work/jobs-garbage
  prepare "$dir"
  run_check "$dir" FAKE_MODE=jobs-garbage
  expect_status "$status" 2 "a job list that is not a document"
  expect_contains "$out" "FACT job: FAIL — no job of the installation is named 'fugaro-p-bb-bitbucket…'" "a job list that is not a document: the row"
  assert_no_launch "$dir" "a job list that is not a document"
}

test_the_happy_path_when_the_project_names_another_region() {
  local dir=$work/region
  prepare "$dir"
  sed "s/\"region\":\"$region\"/\"region\":\"europe-west1\"/" "$dir/json/doctor.json" >"$dir/json/doctor.tmp"
  mv "$dir/json/doctor.tmp" "$dir/json/doctor.json"
  run_check "$dir"
  expect_status "$status" 0 "a project in another region"
  expect_contains "$out" "region europe-west1" "a project in another region: the region it read from the checkout"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "run jobs list --project=$gcp_project --region=europe-west1" "a project in another region: the jobs are read in that region"
}

test_a_launch_that_failed_leaves_the_rows_unmeasured() {
  local dir=$work/launch-fails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=run-fails
  expect_status "$status" 2 "a launch that failed"
  expect_contains "$out" "FACT run: FAIL — fugaro run for $repo exited 1 and never launched a run" "a launch that failed: the row"
  expect_contains "$out" "is not enrolled in the project" "a launch that failed: the launcher's own sentence"
  expect_contains "$out" "FACT landed: FAIL — no terminal row of this check's own run was read" "a launch that failed: the landed row"
  expect_contains "$out" "FACT model: FAIL — no terminal row of this check's own run was read" "a launch that failed: the served rows"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "logging read" "a launch that failed: the service's lines are not read"
  assert_scratch_removed "$dir" "a launch that failed"
}

test_a_launch_that_never_answers_is_not_measured() {
  local dir=$work/launch-hangs
  prepare "$dir"
  run_check "$dir" FAKE_MODE=run-hangs CHECK580_RUN_SECONDS=2
  expect_status "$status" 2 "a launch that never answers"
  expect_contains "$out" "FACT run: FAIL — fugaro run did not answer inside 2s and was ended" "a launch that never answers: the row"
  expect_contains "$out" "it may or may not have launched the run ($run_id)" "a launch that never answers: what it refuses to conclude"
  expect_contains "$out" "Read the listing yourself" "a launch that never answers: what to do"
}

test_a_listing_that_could_not_be_read_leaves_the_rows_unmeasured() {
  local dir=$work/listing-fails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=listing-fails CHECK580_RUN_SECONDS=2
  expect_status "$status" 2 "a listing that could not be read"
  expect_contains "$out" "FACT run: FAIL — the run ($run_id) does not appear in 'fugaro runs ls --repo $repo' after 2s" "a listing that could not be read: the row"
  assert_scratch_removed "$dir" "a listing that could not be read"
}

test_a_run_the_listing_never_names_is_not_measured() {
  local dir=$work/not-listed
  prepare "$dir"
  run_check "$dir" FAKE_MODE=listing-empty CHECK580_RUN_SECONDS=2
  expect_status "$status" 2 "a run the listing never names"
  expect_contains "$out" "FACT run: FAIL — the run ($run_id) does not appear" "a run the listing never names: the row"
  expect_contains "$out" "it was launched but its row is not in the listing" "a run the listing never names: what it refuses to conclude"
}

test_a_run_that_had_not_finished_is_not_measured() {
  local dir=$work/inflight
  prepare "$dir"
  run_check "$dir" FAKE_ROW_FILE=row-inflight.json CHECK580_RUN_SECONDS=2
  expect_status "$status" 2 "a run still in flight"
  expect_contains "$out" "FACT run: FAIL — the run ($run_id) had not reached a final status after 2s: status=succeeded" "a run still in flight: the row"
  expect_contains "$out" "A row still in flight is not an answer about the credentials service" "a run still in flight: what it refuses to conclude"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "logging read" "a run still in flight: the service's lines are not read"
}

test_a_run_that_did_not_push_is_a_finding() {
  local dir=$work/no-push
  prepare "$dir"
  run_check "$dir" FAKE_ROW_FILE=row-no-push.json
  expect_status "$status" 1 "a run that did not push"
  expect_contains "$out" "FACT run: FAIL — the run ($run_id) of $repo did not end with a push and a pull request: status=succeeded, outcome=ready, pushed=no, pr=42" "a run that did not push: the row"
  expect_contains "$out" "Read 'fugaro runs show $run_id --repo $repo' before anything else" "a run that did not push: what to do"
}

test_a_run_with_no_pull_request_is_a_finding() {
  local dir=$work/no-pr
  prepare "$dir"
  run_check "$dir" FAKE_ROW_FILE=row-no-pr.json
  expect_status "$status" 1 "a run with no pull request"
  expect_contains "$out" "FACT run: FAIL — the run ($run_id) of $repo did not end with a push and a pull request: status=succeeded, outcome=ready, pushed=yes, pr=none (the row names no pull request)" "a run with no pull request: the row"
}

test_a_run_that_failed_is_a_finding() {
  local dir=$work/failed
  prepare "$dir"
  run_check "$dir" FAKE_ROW_FILE=row-failed.json
  expect_status "$status" 1 "a run that failed"
  expect_contains "$out" "FACT run: FAIL — the run ($run_id) of $repo did not end with a push and a pull request: status=failed" "a run that failed: the row"
  expect_contains "$out" "reaches every credential through the credentials service" "a run that failed: what it means on this shape"
}

test_a_run_that_landed_on_another_job_is_a_finding() {
  local dir=$work/other-job
  prepare "$dir"
  run_check "$dir" FAKE_ROW_FILE=row-other-job.json
  expect_status "$status" 1 "a run on another job"
  expect_contains "$out" "FACT landed: FAIL — the run ($run_id) ran on '$other_job', which is not one of the profile 'bb''s shared Bitbucket jobs" "a run on another job: the row"
  expect_contains "$out" "a run lands on a shared job only when the repository's own fugaro.yaml SELECTS the profile" "a run on another job: why it matters"
  # The served rows still run: the service's own lines are read for the run that
  # did launch, whatever job it landed on.
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "logging read" "a run on another job: the service's lines are still read"
}

test_a_row_that_names_no_execution_is_not_measured() {
  local dir=$work/no-execution
  prepare "$dir"
  run_check "$dir" FAKE_ROW_FILE=row-no-execution.json
  expect_status "$status" 2 "a row naming no execution"
  expect_contains "$out" "FACT landed: FAIL — the row of the run ($run_id) names no execution" "a row naming no execution: the row"
  expect_contains "$out" "the execution path is the only place that job is written down" "a row naming no execution: why it cannot be measured"
}

test_a_log_read_that_failed_leaves_the_served_rows_unmeasured() {
  local dir=$work/log-fails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=log-fails
  expect_status "$status" 2 "a log read that failed"
  expect_contains "$out" "FACT git: FAIL — the service's own lines for the run ($run_id) could not be read" "a log read that failed: the git row"
  expect_contains "$out" "PERMISSION_DENIED" "a log read that failed: what gcloud said"
  expect_contains "$out" "a read that did not answer is NOT the service having written no line" "a log read that failed: what it refuses to conclude"
  expect_contains "$out" "FACT model: FAIL — the service's own lines for the run ($run_id) could not be read" "a log read that failed: the model row follows it"
  assert_scratch_removed "$dir" "a log read that failed"
}

test_a_run_the_service_wrote_no_line_for_is_a_finding() {
  local dir=$work/log-empty
  prepare "$dir"
  run_check "$dir" FAKE_MODE=log-empty
  expect_status "$status" 1 "a run the service wrote no line for"
  expect_contains "$out" "FACT git: FAIL — the service wrote NO served git line (purpose 'git', status ok) for the run ($run_id) of $repo, while the run pushed and opened a pull request" "a run with no line: the git row"
  expect_contains "$out" "a Bitbucket shared job holds no token at all (D2302)" "a run with no line: the decision it breaks"
  expect_contains "$out" "The purposes the service's own lines for this run carry: <none>" "a run with no line: what it read instead"
}

test_a_run_with_no_served_forward_line_is_a_finding() {
  local dir=$work/no-forward
  prepare "$dir"
  run_check "$dir" FAKE_LOG_FILE=log-no-forward.json
  expect_status "$status" 1 "a run with no served model line"
  expect_contains "$out" "FACT git: PASS" "a run with no served model line: the git row passes"
  expect_contains "$out" "FACT model: FAIL — the service wrote no served model line (purpose 'forward', status ok)" "a run with no served model line: the model row"
  expect_contains "$out" "the job's own mounts, or a credential this service does not serve" "a run with no served model line: where else it could have come from"
}

test_a_first_model_call_with_another_credential_is_a_finding() {
  local dir=$work/other-credential
  prepare "$dir"
  run_check "$dir" FAKE_LOG_FILE=log-other-credential.json
  expect_status "$status" 1 "a first model call with another credential"
  expect_contains "$out" "FACT model: FAIL — the run's first model call ($run_id) was served with the credential 'anthropic-api-key', which is NOT one of the profile 'bb''s claude_accounts (work)" "another credential: the row"
  expect_contains "$out" "the project's paid key above all" "another credential: the case it is about"
  expect_contains "$out" "The credential|tier|purpose the service's lines for this run name: anthropic-api-key|B|forward" "another credential: the lines it read are named"
}

test_the_first_model_call_is_read_by_its_time() {
  local dir=$work/two-forwards
  prepare "$dir"
  # The document's LAST line is the run's FIRST model call: the row has to read
  # the earliest served forward line by its own timestamp, not the first one it
  # happens to walk past.
  run_check "$dir" FAKE_LOG_FILE=log-two-forwards.json
  expect_status "$status" 0 "two served model calls"
  expect_contains "$out" "FACT model: PASS — the run's first model call ($run_id) was served with the credential 'work', one of the profile 'bb''s claude_accounts (work)" "two served model calls: the earliest is judged"
  expect_absent "$out" "served with the credential 'spare-account'" "two served model calls: the later switch is not the first call"
}

test_the_first_model_call_is_read_from_the_runs_own_start() {
  local dir=$work/late-switch
  prepare "$dir"
  # A run of seven lines whose first forward line is the profile's account and
  # whose last three are the paid key's, read three at a time: `gcloud logging
  # read` answers the NEWEST three unless `--order=asc` says otherwise, and that
  # answer's first forward line is a later call's credential, which is the false
  # FAIL this row must never take. CHECK580_LOG_AGE is bent below the run's own
  # duration too, so the window has to come from the launch and not from the knob.
  run_check "$dir" FAKE_LOG_FILE=log-late-switch.json CHECK580_LOG_LIMIT=3 CHECK580_LOG_AGE=1m
  expect_status "$status" 0 "a run that switched credential after its first calls"
  expect_contains "$out" "FACT model: PASS — the run's first model call ($run_id) was served with the credential 'work'" "a run that switched credential after its first calls: the FIRST call decides the row"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "--limit=3 --order=asc" "the run's oldest lines are the ones asked for"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "--freshness=" "the window is asked for"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "--freshness=1m" "a knob narrower than the run is widened to the launch, not used as written"
}

test_a_git_line_that_was_not_served_is_not_the_git_row() {
  local dir=$work/git-refused
  prepare "$dir"
  run_check "$dir" FAKE_LOG_FILE=log-git-refused.json
  expect_status "$status" 1 "a git line that was not served"
  expect_contains "$out" "FACT git: FAIL — the service wrote NO served git line" "a git line that was not served: the row reads the status and not the purpose alone"
  expect_contains "$out" "The purposes the service's own lines for this run carry: forward git" "a git line that was not served: the purposes it did carry"
}

test_a_line_of_another_run_is_not_read() {
  local dir=$work/other-run
  prepare "$dir"
  # Every line of the service belongs to ANOTHER run: the check reads only the
  # lines of the run id it launched, and this is the case that proves it.
  python3 - "$dir/json/log.json" "$dir/json/log-other-run.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
for e in doc:
    e["jsonPayload"]["run"] = "20261117-110000-9999"
json.dump(doc, open(sys.argv[2], "w"))
PY
  run_check "$dir" FAKE_LOG_FILE=log-other-run.json
  expect_status "$status" 1 "lines of another run"
  expect_contains "$out" "FACT git: FAIL — the service wrote NO served git line" "lines of another run: they are not the run's own"
  expect_contains "$out" "The purposes the service's own lines for this run carry: <none>" "lines of another run: nothing of the run's own was read"
}

test_kept_scratch_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  # Keeping the scratch directory is the owner asking for it, and the check says
  # where it is rather than removing it: the row reads PASS and the directory is
  # still there for them to read.
  run_check "$dir" CHECK580_KEEP_SCRATCH=1
  expect_status "$status" 0 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: PASS — kept on request (CHECK580_KEEP_SCRATCH is set), at " "a kept scratch directory: the row says where it is"
  expect_contains "$err" "CHECK580_KEEP_SCRATCH is set" "a kept scratch directory: the note"
  [ -n "$scratch" ] || fail "a kept scratch directory: the check named no scratch directory"
  if [ -n "$scratch" ] && [ -d "$scratch" ]; then
    rm -rf "$scratch"
  else
    fail "a kept scratch directory: the directory was removed although CHECK580_KEEP_SCRATCH is set"
  fi
}

test_an_unknown_argument_is_refused() {
  local dir=$work/unknown
  prepare "$dir"
  run_check "$dir" -- --wat
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --wat" "an unknown argument: the reason"
  assert_no_launch "$dir" "an unknown argument"
}

test_the_belong_guard_is_named_in_every_block() {
  local dir=$work/block-keys
  prepare "$dir"
  run_check "$dir"
  # Every key the register quotes is in the block, in one order, whatever
  # happened: a key the run never reached reads FAIL — not reached.
  local key
  for key in fugaro-version xtrace agent-session belong sandbox policy job run landed git model restore scratch-removed; do
    expect_contains "$out" "FACT $key:" "the block: the $key row is printed"
  done
  expect_absent "$out" "FAIL — not reached" "the block: nothing is missing on the happy path"
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i (each case works in its own directory under
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
t test_a_run_the_sandbox_does_not_name_is_refused
t test_a_credential_in_the_origin_is_never_printed
t test_an_origin_that_names_no_repository_is_refused
t test_a_checkout_with_no_origin_is_refused
t test_a_missing_sandbox_is_refused
t test_a_path_that_is_not_a_checkout_stops_the_check
t test_a_missing_tool_stops_before_anything_is_called
t test_xtrace_is_refused
t test_a_coding_agents_session_is_refused
t test_belong_is_refused_by_name
t test_a_sandbox_path_naming_belong_stops_the_check
t test_a_project_naming_belong_stops_the_check
t test_a_checkout_naming_belong_is_refused_by_the_repository_guard
t test_doctor_exit_1_is_an_answer
t test_a_doctor_that_is_not_a_document_stops_the_check
t test_a_doctor_that_never_answers_stops_the_check
t test_a_policy_that_could_not_be_read_leaves_the_rows_unmeasured
t test_a_repository_the_project_does_not_admit_is_not_measured
t test_a_repository_no_profile_admits_is_a_finding
t test_a_repository_two_profiles_admit_is_not_measured
t test_a_profile_of_another_provider_does_not_admit_the_repository
t test_a_policy_whose_only_profile_serves_another_provider_is_a_finding
t test_a_profile_with_no_claude_accounts_is_a_finding
t test_a_policy_with_no_budget_mode_is_not_measured
t test_no_shared_job_of_the_profile_is_not_measured
t test_a_shared_job_without_the_service_url_is_a_finding
t test_a_shared_job_that_carries_a_bitbucket_token_is_a_finding
t test_one_job_of_the_profile_without_the_url_is_a_finding
t test_a_jobs_list_that_could_not_be_read_is_not_measured
t test_a_jobs_list_that_is_not_a_document_is_not_measured
t test_the_happy_path_when_the_project_names_another_region
t test_a_launch_that_failed_leaves_the_rows_unmeasured
t test_a_launch_that_never_answers_is_not_measured
t test_a_listing_that_could_not_be_read_leaves_the_rows_unmeasured
t test_a_run_the_listing_never_names_is_not_measured
t test_a_run_that_had_not_finished_is_not_measured
t test_a_run_that_did_not_push_is_a_finding
t test_a_run_with_no_pull_request_is_a_finding
t test_a_run_that_failed_is_a_finding
t test_a_run_that_landed_on_another_job_is_a_finding
t test_a_row_that_names_no_execution_is_not_measured
t test_a_log_read_that_failed_leaves_the_served_rows_unmeasured
t test_a_run_the_service_wrote_no_line_for_is_a_finding
t test_a_run_with_no_served_forward_line_is_a_finding
t test_a_first_model_call_with_another_credential_is_a_finding
t test_the_first_model_call_is_read_by_its_time
t test_the_first_model_call_is_read_from_the_runs_own_start
t test_a_git_line_that_was_not_served_is_not_the_git_row
t test_a_line_of_another_run_is_not_read
t test_kept_scratch_is_reported
t test_a_job_with_a_truncated_name_is_found_by_its_labels
t test_an_unknown_argument_is_refused
t test_the_belong_guard_is_named_in_every_block

if [ "$failures" -ne 0 ]; then
  echo "check-580_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-580_test.sh: ok"
