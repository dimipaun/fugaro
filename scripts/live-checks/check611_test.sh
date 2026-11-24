#!/usr/bin/env bash
# Tests for scripts/live-checks/check611.sh, with fake `fugaro`, `gcloud` and
# `git` on PATH: NOTHING here reaches a cloud, a project, a repository or a run,
# no agent runs a live command, and the fakes record every call they are given —
# with the directory it was made in — so a case can assert what the check did and
# what it did NOT do (above all that it never writes: it launches ONE review and
# creates nothing else, and every other command it makes is a read).
#
# The fakes are a small model of one installation and its sandbox: the fake `git`
# answers the checkout's origin, the fake `fugaro` answers the reads (version,
# doctor, the published policy, the run's own row, the verdict), launches the
# review the case asked for and returns the job and account it resolved, and the
# fake `gcloud` serves the job list and the credentials service's own lines.
# `gh` and `curl` are on PATH too and do nothing but record that they were called:
# this check makes no Bitbucket API call of its own — it holds no token — and the
# cases below assert both logs are empty.
#
# Every document a case bends is a file the case wrote — a policy that defines a
# profile, does not broker the repository, or carries no budget mode; a job list whose implicit
# job carries the Bitbucket token or a secret, or is not there at all; a launch
# document that named another job or another account; a row that pushed, read
# another pull request or had not finished; a verdict about another repository; a
# service log with no git line, no model line, another credential or two model
# lines whose earliest is not the first in the document.
#
# Three cases mutate the CHECK SCRIPT ITSELF (a copy under the case's directory)
# and assert that the mutant's block and exit status really differ from what the
# suite expects: an assertion that a row reads FAIL is only worth making if a copy
# whose row reads PASS is caught.
#
# Usage: bash scripts/live-checks/check611_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check611.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK611_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK611_TEST_HEAD:-1200}")" ;;
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
install=chk611-test
gcp_project=chk611-test-proj
region=us-east5
repo=bb-owner/chk611-sandbox
origin=https://bitbucket.org/bb-owner/chk611-sandbox.git
base_image=us-east5-docker.pkg.dev/chk611-test-proj/fugaro/agent:20261118
# The name the release derives for the installation's IMPLICIT review-only job of
# provider bitbucket: gcp.ProjectJobName(config.DefaultJobProfile, "bitbucket",
# config.ReviewVariantAccount("")+config.ReviewVariantSuffix) — which is
# "fugaro-p-standard-bitbucket-default-review" truncated to fit 49 characters
# together with its 12-hex hash, so the "-review" it was built from is cut to
# "-r" and then trimmed. Pinned here as the value the check's own reader computes:
# a change to the derivation changes both, and this constant is what says so.
bb_job=fugaro-p-standard-bitbucket-default-491922f654ef
gh_job=fugaro-p-standard-github-default-rev-04644ca962ed
# A job of some other shape: a per-repository or per-profile job, which is what
# the launch must NOT resolve a review onto.
other_job=fugaro-p-bb-bitbucket-7f3a2b
run_slug=$install/chk611-run-1
run_id=20261118-090000-0611
pr=42
# credential_origin is the origin a token-based clone leaves behind: a GitHub URL
# whose userinfo carries a credential, split by a `/`, which is where a strip that
# cannot cross one leaves it in place. What it holds is not a credential and opens
# nothing: it is the word the case below asserts never reaches the output, so one
# that appears was printed by this check and by nothing else (Check 480 pins the
# rule this check shares, scripts/contract_checks_test.go's assertNoCredential).
credential_origin='https://x-access-token:LEAK-CANARY/with-a-slash@github.com/acme/sandbox.git'
credential_words='x-access-token LEAK-CANARY with-a-slash'

# --------------------------------------------------------------------- fakes
write_fake_git() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/git" <<'GIT_EOF'
#!/usr/bin/env bash
# A stand-in for `git`, for scripts/live-checks/check611.sh's test only. The
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

# gh and curl are on PATH and are instruments of the "never called" assertion: this
# check holds no Bitbucket token and makes no provider API call, so a call to
# either is a reading this check must not be making. They record and fail.
write_fake_provider_tools() { # dir
  mkdir -p "$1/bin"
  local tool
  for tool in gh curl; do
    cat >"$1/bin/$tool" <<TOOL_EOF
#!/usr/bin/env bash
# A stand-in for \`$tool\`, for scripts/live-checks/check611.sh's test only. The
# check never calls it: it holds no Bitbucket token, so it makes no provider API
# call, and this records one if that ever changes.
set -euo pipefail
printf '$tool %s (in %s)\n' "\$*" "\$PWD" >>"\$FAKE_LOG_DIR/$tool.calls"
echo "$tool: this check makes no provider API call" >&2
exit 1
TOOL_EOF
    chmod +x "$1/bin/$tool"
  done
}

write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check611.sh's test only. It
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
    printf '%s\n' "${FAKE_ACCOUNT:-chk611-owner@example.com}"
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
    # Without that, a check reading the default answer would decide the review's
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
# A stand-in for `fugaro`, for scripts/live-checks/check611.sh's test only. The
# reads come from files the case wrote; `review` records the run id it minted
# (which is what makes the listing below carry that run) and returns the LAUNCH
# document the real one prints — the job the review resolved and the account it
# runs as; `review show` prints the verdict artifact. Every call is logged with
# the directory it was made in.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}
# The run slug `review show` is asked for, in the shape the real launcher names one:
# the sentences that quote it are literals here, so the fake needs its own.
run_slug=${FAKE_RUN_SLUG:-chk611-test/chk611-run-1}
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
review)
  if [ "${2:-}" = "show" ]; then
    case "$mode" in
    artifact-fails)
      echo "fugaro: the run $run_slug has no review artifact: a run that is still going, a review whose signature could not be made, or a run that is not a review. See 'fugaro diagnose'" >&2
      exit 1
      ;;
    artifact-garbage)
      printf 'not a document\n'
      ;;
    *)
      cat "$FAKE_JSON_DIR/${FAKE_ARTIFACT_FILE:-artifact.json}"
      ;;
    esac
    exit 0
  fi
  case "$mode" in
  review-hangs) sleep 30 ;;
  review-fails)
    echo "ERROR: fugaro: ${FAKE_RUN_REPO:-bb-owner/chk611-sandbox} is not enrolled in the project (fugaro project repos add)" >&2
    exit 1
    ;;
  esac
  rid=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --pr) rid=$2; shift 2 ;;
    *) shift ;;
    esac
  done
  # The launcher mints the run's id itself; this is it.
  printf '%s\n' "${FAKE_RUN_ID:-20261118-090000-0611}" >"$FAKE_STATE_DIR/run-id"
  cat "$FAKE_JSON_DIR/${FAKE_LAUNCH_FILE:-launch.json}"
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
{"ok":true,"checks":[{"name":"credentials","status":"ok"}],
 "project":{"name":"$install","gcp_project":"$gcp_project","region":"$region",
  "base_images":{"base":"$base_image"}}}
JSON
  # The published policy of the plan's sandbox: the repository is admitted to the
  # project and to NO profile (the document defines none), the node brokers its
  # Bitbucket access with proxy: true, the budget is enforced, and the accounts
  # block admits the repository to the default account (F4, U2).
  cat >"$1/json/policy.json" <<JSON
{"project":"$install","policy":{"version":3,"allow_repos":["bitbucket/$repo"],
 "budget":{"mode":"enforce","per_run_usd":5,"allowed_models":["claude-sonnet-5"],"max_run_tokens":400000},
 "bitbucket_token":{"allow_repos":["bitbucket/$repo"],"proxy":true},
 "accounts":{"default":{"allow_repos":["bitbucket/$repo"]}}}}
JSON
  # The policy variants, each one bend of exactly one fact.
  python3 - "$1/json" <<'PY'
import copy, json, os, sys
d = sys.argv[1]
doc = json.load(open(os.path.join(d, "policy.json")))


def out(name, mutate):
    clone = copy.deepcopy(doc)
    mutate(clone)
    json.dump(clone, open(os.path.join(d, name), "w"))


# A profile that admits THIS repository: a repository a profile admits has a
# shared project job, and a review of it runs on that profile's review variant —
# which is the case this check must refuse rather than measure.
out("policy-with-profile.json", lambda x: x["policy"].__setitem__("profiles", {
    "bb": {"provider": "bitbucket", "cpu": 2, "memory": "4Gi",
           "timeout_class": "standard", "allow_repos": ["bitbucket/bb-owner/chk611-sandbox"],
           "claude_accounts": ["default"]}}))
# A profile of ANOTHER provider that writes no allow_repos of its own: it falls
# back to the project's list, and projectpolicy.Admits gates on the provider
# first, so it does not admit this Bitbucket repository.
out("policy-other-provider.json", lambda x: x["policy"].__setitem__("profiles", {
    "gh": {"provider": "github", "cpu": 2, "memory": "4Gi", "timeout_class": "standard"}}))
out("policy-no-proxy.json", lambda x: x["policy"]["bitbucket_token"].__setitem__("proxy", False))
out("policy-token-not-admitted.json", lambda x: x["policy"]["bitbucket_token"].__setitem__(
    "allow_repos", ["bitbucket/other-owner/elsewhere"]))
out("policy-no-mode.json", lambda x: x["policy"]["budget"].pop("mode"))
out("policy-not-admitted.json", lambda x: x["policy"].__setitem__(
    "allow_repos", ["bitbucket/other-owner/elsewhere"]))
out("policy-no-accounts.json", lambda x: x["policy"].__setitem__("accounts", {}))
out("policy-other-account.json", lambda x: x["policy"].__setitem__(
    "accounts", {"spare": {"allow_repos": ["bitbucket/bb-owner/chk611-sandbox"]}}))
PY
  # The installation's project jobs: the two implicit review-only jobs this
  # release deploys with no profile (bitbucket and github), each with the
  # service's address and no credential, on the installation's BASE image.
  cat >"$1/json/jobs.json" <<JSON
[{"metadata":{"name":"$bb_job","labels":{"fugaro":"managed","fugaro_profile":"standard"}},
  "spec":{"template":{"spec":{"template":{"spec":{"containers":[{"image":"$base_image",
   "env":[{"name":"FUGARO_CREDENTIALS_SERVICE_URL","value":"https://fugaro-credentials-abc.a.run.app"},
          {"name":"FUGARO_PROJECT","value":"$install"}]}]}}}}}},
 {"metadata":{"name":"$gh_job","labels":{"fugaro":"managed","fugaro_profile":"standard"}},
  "spec":{"template":{"spec":{"template":{"spec":{"containers":[{"image":"$base_image",
   "env":[{"name":"FUGARO_CREDENTIALS_SERVICE_URL","value":"https://fugaro-credentials-abc.a.run.app"}]}]}}}}}}]
JSON
  # The job variants, each one bend of exactly one fact: no service address, a
  # Bitbucket token, the secret-env list, a secret key reference, a secret volume
  # and the two Vertex names — plus a list whose implicit Bitbucket job is not
  # there at all, and one that carries a third job of another shape (which is what
  # U4 reads).
  python3 - "$1/json" "$bb_job" <<'PY'
import copy, json, os, sys
d, job = sys.argv[1], sys.argv[2]
doc = json.load(open(os.path.join(d, "jobs.json")))


def spec_of(x):
    return x["spec"]["template"]["spec"]["template"]["spec"]


def envs(x):
    return spec_of(x)["containers"][0]["env"]


def out(name, mutate):
    """One bend of the FIRST job of the list, which is the implicit Bitbucket one."""
    clone = copy.deepcopy(doc)
    mutate(clone[0])
    json.dump(clone, open(os.path.join(d, name), "w"))


def out_list(name, mutate):
    """One bend of the list itself."""
    clone = copy.deepcopy(doc)
    mutate(clone)
    json.dump(clone, open(os.path.join(d, name), "w"))


def drop_url(x):
    spec_of(x)["containers"][0]["env"] = [e for e in envs(x) if e["name"] != "FUGARO_CREDENTIALS_SERVICE_URL"]


out("jobs-no-url.json", drop_url)
out("jobs-token.json", lambda x: envs(x).append({"name": "FUGARO_BITBUCKET_TOKEN", "value": "x"}))
out("jobs-secretenvs.json", lambda x: envs(x).append({"name": "FUGARO_SECRET_ENVS", "value": "[]"}))
out("jobs-secretref.json", lambda x: envs(x).append(
    {"name": "ANTHROPIC_API_KEY", "valueFrom": {"secretKeyRef": {"name": "anthropic-api-key", "key": "latest"}}}))
out("jobs-secretvolume.json", lambda x: spec_of(x).__setitem__(
    "volumes", [{"name": "anthropic", "secret": {"secretName": "anthropic-api-key"}}]))
out("jobs-vertex.json", lambda x: (envs(x).append({"name": "CLOUD_ML_REGION", "value": "us-east5"}),
                                   envs(x).append({"name": "ANTHROPIC_VERTEX_PROJECT_ID", "value": "p"})))
out("jobs-other-image.json", lambda x: spec_of(x)["containers"][0].__setitem__(
    "image", "us-east5-docker.pkg.dev/p/fugaro/agent:repo-built"))
out_list("jobs-missing.json", lambda x: x.pop(0))
out_list("jobs-third.json", lambda x: x.append({
    "metadata": {"name": "fugaro-p-bb-bitbucket-7f3a2b"},
    "spec": {"template": {"spec": {"template": {"spec": {"containers": [
        {"image": "x", "env": [{"name": "FUGARO_CREDENTIALS_SERVICE_URL", "value": "u"}]}]}}}}}}))
out_list("jobs-empty.json", lambda x: x.clear())
PY
  # The launch's own document: the review resolved the installation's implicit
  # review-only job of provider bitbucket, as the default account.
  cat >"$1/json/launch.json" <<JSON
{"project":"$install","run":"$run_slug","repo":"$repo","run_id":"$run_id","pr":$pr,
 "status":"launched","job":"$bb_job","account":"default",
 "execution":"projects/$gcp_project/locations/$region/jobs/$bb_job/executions/chk611-run-1",
 "recipe":"review-only"}
JSON
  python3 - "$1/json" "$other_job" <<'PY'
import copy, json, os, sys
d, other = sys.argv[1], sys.argv[2]
doc = json.load(open(os.path.join(d, "launch.json")))


def out(name, mutate):
    clone = copy.deepcopy(doc)
    mutate(clone)
    json.dump(clone, open(os.path.join(d, name), "w"))


out("launch-other-job.json", lambda x: x.__setitem__("job", other))
out("launch-other-account.json", lambda x: x.__setitem__("account", "spare"))
out("launch-already.json", lambda x: x.__setitem__("status", "already-launched"))
out("launch-odd-status.json", lambda x: x.__setitem__("status", "refused"))
out("launch-other-pr.json", lambda x: x.__setitem__("pr", 7))
out("launch-no-run.json", lambda x: (x.pop("run_id"), x.pop("run")))
PY
  # The run's own row: it ended succeeded with outcome reviewed, pushed nothing
  # and names the pull request it read, on the job the launch resolved.
  cat >"$1/json/row.json" <<JSON
[{"run_id":"@RID@","repo":"$repo","status":"succeeded","terminal":true,"outcome":"reviewed","pushed":false,
  "pr":$pr,"recipe":"review-only",
  "execution":"projects/$gcp_project/locations/$region/jobs/$bb_job/executions/chk611-run-1"}]
JSON
  # The row variants, each one bend of exactly one fact.
  python3 - "$1/json" <<'PY'
import copy, json, os, sys
d = sys.argv[1]
doc = json.load(open(os.path.join(d, "row.json")))


def out(name, mutate):
    clone = copy.deepcopy(doc)
    mutate(clone[0])
    json.dump(clone, open(os.path.join(d, name), "w"))


out("row-pushed.json", lambda r: r.__setitem__("pushed", True))
out("row-coded.json", lambda r: r.__setitem__("outcome", "ready"))
out("row-failed.json", lambda r: (r.__setitem__("status", "failed"),
                                  r.__setitem__("outcome", ""),
                                  r.__setitem__("halt", {"kind": "bootstrap", "detail": "the checkout failed: no credential"})))
out("row-other-pr.json", lambda r: r.__setitem__("pr", 7))
out("row-inflight.json", lambda r: r.__setitem__("terminal", False))
out("row-other-run.json", lambda r: r.__setitem__("run_id", "20261118-080000-0611"))
PY
  # The verdict the review recorded.
  cat >"$1/json/artifact.json" <<JSON
{"schema_version":1,"review_run":"$run_slug","created_at":"2026-11-18T09:04:00Z",
 "repository":"$repo","pr":$pr,"head_sha":"2f1a4c7d9e0b3a58","base":"main",
 "reviewer":{"account":"default","model":"claude-sonnet-5","effort":"high"},
 "verdict":"request_changes","overview":"One test does not assert what it names.",
 "strengths":["the new route is refused on an unknown key"],
 "findings":[{"severity":"major","title":"a refused key is dropped","file":"a.go","line":12}]}
JSON
  python3 - "$1/json" <<'PY'
import copy, json, os, sys
d = sys.argv[1]
doc = json.load(open(os.path.join(d, "artifact.json")))


def out(name, mutate):
    clone = copy.deepcopy(doc)
    mutate(clone)
    json.dump(clone, open(os.path.join(d, name), "w"))


out("artifact-other-repo.json", lambda x: x.__setitem__("repository", "bb-owner/elsewhere"))
out("artifact-other-pr.json", lambda x: x.__setitem__("pr", 7))
json.dump([], open(os.path.join(d, "artifact-list.json"), "w"))
PY
  # The credentials service's own lines for the review run: two served git
  # requests (the clone of the pull request's head) and one served model call,
  # whose credential is the default account the policy admits.
  cat >"$1/json/log.json" <<JSON
[{"timestamp":"2026-11-18T09:00:03Z","jsonPayload":{"msg":"credsvc: audit","run":"@RID@","repository":"$repo",
   "job":"$bb_job","caller":"chk611-run@$gcp_project.iam.gserviceaccount.com",
   "credential":"bitbucket-token","purpose":"git","tier":"","status":"ok"}},
 {"timestamp":"2026-11-18T09:00:05Z","jsonPayload":{"msg":"credsvc: audit","run":"@RID@","repository":"$repo",
   "job":"$bb_job","caller":"chk611-run@$gcp_project.iam.gserviceaccount.com",
   "credential":"bitbucket-token","purpose":"git","tier":"","status":"ok"}},
 {"timestamp":"2026-11-18T09:00:09Z","jsonPayload":{"msg":"credsvc: audit","run":"@RID@","repository":"$repo",
   "job":"$bb_job","caller":"chk611-run@$gcp_project.iam.gserviceaccount.com",
   "credential":"default","purpose":"forward","tier":"A","status":"ok"}}]
JSON
  python3 - "$1/json" <<'PY'
import json, os, sys
d = sys.argv[1]
doc = json.load(open(os.path.join(d, "log.json")))


def write(name, entries):
    json.dump(entries, open(os.path.join(d, name), "w"))


def audits(entries, purpose):
    return [e for e in entries if e["jsonPayload"].get("purpose") == purpose]


def clone(e, **fields):
    c = json.loads(json.dumps(e))
    c["jsonPayload"].update(fields)
    return c


write("log-no-git.json", [e for e in doc if e["jsonPayload"].get("purpose") != "git"])
write("log-no-forward.json", audits(doc, "git"))
write("log-other-credential.json", [clone(e, credential="spare-account", tier="B")
                                    if e["jsonPayload"].get("purpose") == "forward" else e for e in doc])
# The project's own key, under either of the two names this release uses for it:
# not an account of the node's list, and a PASS with its own sentence.
write("log-project-key.json", [clone(e, credential="anthropic-api-key--project", tier="B")
                               if e["jsonPayload"].get("purpose") == "forward" else e for e in doc])
write("log-provider-key.json", [clone(e, credential="provider-key--project", tier="B")
                                if e["jsonPayload"].get("purpose") == "forward" else e for e in doc])
# The run's git traffic served, but the git line NOT ok: the row has to read the
# status and not the purpose alone.
write("log-git-refused.json", [clone(e, status="unavailable")
                               if e["jsonPayload"].get("purpose") == "git" else e for e in doc])
# Two served model calls, the EARLIER one LAST in the document: the row has to
# read the run's FIRST call by its own time and not by the document's order.
first = audits(doc, "forward")[0]
earlier = clone(first, credential="default", tier="A")
earlier["timestamp"] = "2026-11-18T09:00:07Z"
later = clone(first, credential="spare-account", tier="B")
later["timestamp"] = "2026-11-18T09:00:41Z"
write("log-two-forwards.json", audits(doc, "git") + [later, earlier])
# A run whose first model calls are the default account and whose later ones are
# not, with the git line at the end: read from the run's OWN start the first
# forward line is the account, and read newest-first (gcloud's own default) the
# first forward line in the answer is NOT — the reading that makes this row a
# false PASS or a false FAIL. The case bends CHECK611_LOG_LIMIT to 3 so the two
# answers really differ.
write("log-late-switch.json", [
    clone(audits(doc, "git")[0], timestamp="2026-11-18T09:00:03Z"),
    clone(first, timestamp="2026-11-18T09:00:09Z"),
    clone(first, timestamp="2026-11-18T09:00:41Z"),
    clone(later, timestamp="2026-11-18T09:01:10Z"),
    clone(later, timestamp="2026-11-18T09:01:40Z"),
    clone(later, timestamp="2026-11-18T09:02:05Z"),
    clone(audits(doc, "git")[0], timestamp="2026-11-18T09:02:20Z")])
PY
}

# A repository checkout: the check refuses a path that is not one, and the
# repository, the installation and the region are all read from it. The workflow
# it declares is the premise U1 reads.
write_checkout() { # dir
  mkdir -p "$1"
  cat >"$1/fugaro.yaml" <<YAML
project: $install
workflows:
  default:
    checkout: image
YAML
}

prepare() { # dir
  mkdir -p "$1/home" "$1/state" "$1/logs" "$1/tmp"
  write_fake_git "$1"
  write_fake_provider_tools "$1"
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

run_script() { # run_script SCRIPT DIR ENV... [-- ARGS...]
  local run_script_path=$1 dir=$2
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
  mkdir -p "$dir/state" "$dir/logs" "$dir/home" "$dir/tmp"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/git.calls"
  : >"$dir/logs/gh.calls"
  : >"$dir/logs/curl.calls"
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
    FAKE_RUN_ID="${FAKE_RUN_ID:-$run_id}" \
    FAKE_JOBS_FILE="${FAKE_JOBS_FILE:-jobs.json}" FAKE_LOG_FILE="${FAKE_LOG_FILE:-log.json}" \
    FAKE_ROW_FILE="${FAKE_ROW_FILE:-row.json}" FAKE_POLICY_FILE="${FAKE_POLICY_FILE:-policy.json}" \
    FAKE_LAUNCH_FILE="${FAKE_LAUNCH_FILE:-launch.json}" \
    FAKE_ARTIFACT_FILE="${FAKE_ARTIFACT_FILE:-artifact.json}" \
    FAKE_DOCTOR_STATUS="${FAKE_DOCTOR_STATUS:-0}" \
    CHECK611_SANDBOX="$dir/sandbox" \
    CHECK611_PR="$pr" \
    CHECK611_RUN_SECONDS="${CHECK611_RUN_SECONDS:-3}" \
    CHECK611_POLL_SECONDS="${CHECK611_POLL_SECONDS:-1}" \
    CHECK611_LOG_WAIT_SECONDS="${CHECK611_LOG_WAIT_SECONDS:-2}" \
    CHECK611_LOG_POLL_SECONDS="${CHECK611_LOG_POLL_SECONDS:-1}" \
    CHECK611_DOCTOR_TIMEOUT_SECONDS="${CHECK611_DOCTOR_TIMEOUT_SECONDS:-10}" \
    CHECK611_CLI_TIMEOUT_SECONDS="${CHECK611_CLI_TIMEOUT_SECONDS:-10}" \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$run_script_path" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

run_check() { # run_check DIR ENV... [-- ARGS...]
  run_script "$script" "$@"
}

# ------------------------------------------------------------------ assertions
assert_scratch_removed() { # dir what
  [ -n "$scratch" ] || {
    fail "$2: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$2: the scratch directory $scratch is still there"
}

# What this check creates is ONE review, and it deletes nothing: every command it
# gives fugaro or gcloud besides that launch is a READ. This is the assertion that
# would catch a diff that made it write.
assert_reads_only() { # dir what
  local fugaro gcloud
  fugaro=$(cat "$1/logs/fugaro.calls")
  gcloud=$(cat "$1/logs/gcloud.calls")
  local word
  for word in "project policy publish" "project repos add" "project repos rm" "update" "setup" "restore" "runs rm" "creds set" "creds rm" "image build" "review --pr $pr --mutate"; do
    expect_absent "$fugaro" "$word" "$2: fugaro '$word' is never run"
  done
  for word in "jobs create" "jobs update" "jobs delete" "jobs deploy" "run deploy" "services deploy" "logging write" "secrets versions"; do
    expect_absent "$gcloud" "$word" "$2: gcloud '$word' is never run"
  done
  # This check holds no Bitbucket token, so it makes no provider API call of its
  # own: the only identity anywhere in its path is the one Cloud Run gave the job
  # it launched.
  expect_absent "$(cat "$1/logs/gh.calls")" "gh " "$2: gh is never called"
  expect_absent "$(cat "$1/logs/curl.calls")" "curl " "$2: curl is never called"
}

# A coding run is never launched: the one launch this check makes is a review.
assert_no_launch() { # dir what
  expect_absent "$(cat "$1/logs/fugaro.calls")" "fugaro run " "$2: no coding run is launched"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check611.sh --sandbox <a sandbox Bitbucket repository checkout> --pr <the pull request to review>" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  assert_no_launch "$dir" "--help"
  assert_reads_only "$dir" "--help"
  # The header is what a person reads before running it: it has to say, where they
  # will see it, that the review is billable, that it is theirs, and that a review
  # pushes nothing — so the Bitbucket repository ends as it began.
  expect_contains "$out" "billable" "--help: the cost of the review is stated"
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  expect_contains "$out" "gcloud logging read" "--help: how the service's lines are read back"
  expect_contains "$out" "opens nothing and comments on nothing" "--help: what it does not leave behind"
  expect_contains "$out" "touches the repository" "--help: what it does not leave behind"
  expect_contains "$out" "U1" "--help: the UNVERIFIED items it answers are named"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK611_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 611 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version row"
  expect_contains "$out" "FACT xtrace: PASS" "dry run: the trace guard"
  expect_contains "$out" "FACT agent-session: PASS" "dry run: the session guard"
  expect_contains "$out" "FACT belong: PASS" "dry run: the belong guard"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore row"
  expect_contains "$out" "FACT launch: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$out" "FACT U4: INFO — planned" "dry run: the UNVERIFIED rows are planned too"
  expect_contains "$out" "CHECK 611 RESULT END (exit 0)" "dry run: the end line"
  expect_contains "$err" "BILLABLE" "dry run: the plan names the cost"
  expect_contains "$err" "first purpose 'forward' line" "dry run: the plan names what the model row reads"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "dry run: the installation is not read"
  assert_no_launch "$dir" "dry run"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "review --pr" "dry run: no review is launched"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "logging read" "dry run: the service's lines are not read"
  assert_scratch_removed "$dir" "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the happy path"
  expect_contains "$out" "FACT sandbox: PASS — $dir/sandbox is a Fugaro repository's checkout of the BITBUCKET repository $repo" "the happy path: the sandbox row"
  expect_contains "$out" "it belongs to the $install installation ($gcp_project, region $region)" "the happy path: the installation it read"
  expect_contains "$out" "is #$pr" "the happy path: the pull request it reviews"
  expect_contains "$out" "FACT policy: PASS" "the happy path: the policy row"
  expect_contains "$out" "defines 0 profile(s) and none of them admits it" "the happy path: the profile-less premise"
  expect_contains "$out" "admits it with proxy: true" "the happy path: the brokering it needs"
  expect_contains "$out" "published budget mode is 'enforce'" "the happy path: the mode it read"
  expect_contains "$out" "FACT launch: PASS — fugaro review --pr $pr answered 'launched' and resolved the run $run_slug on the job '$bb_job' as the account 'default'" "the happy path: the launch row"
  expect_contains "$out" "FACT run: PASS — the review run ($run_id) of $repo#$pr ended 'succeeded' with outcome 'reviewed', pushed nothing" "the happy path: the run row"
  expect_contains "$out" "FACT job: PASS" "the happy path: the job row"
  expect_contains "$out" "carries FUGARO_CREDENTIALS_SERVICE_URL and no credential of any kind" "the happy path: what the job mounts"
  expect_contains "$out" "FACT review: PASS" "the happy path: the verdict row"
  expect_contains "$out" "verdict='request_changes'" "the happy path: the verdict it read back"
  expect_contains "$out" "reviewer account='default' model='claude-sonnet-5'" "the happy path: the reviewer it read back"
  expect_contains "$out" "FACT git: PASS — 2 served git line(s)" "the happy path: the git row"
  expect_contains "$out" "naming the credential(s) bitbucket-token" "the happy path: the git credential it read"
  expect_contains "$out" "FACT model: PASS — the review run's first model call ($run_id) was served with the credential 'default', one of the accounts the published node admits for $repo" "the happy path: the model row"
  expect_contains "$out" "FACT U1: PASS" "the happy path: U1"
  expect_contains "$out" "FACT U2: PASS" "the happy path: U2"
  expect_contains "$out" "FACT U3: PASS" "the happy path: U3"
  expect_contains "$out" "FACT U4: PASS" "the happy path: U4"
  expect_contains "$out" "every one of them is the IMPLICIT review-only job this release derives" "the happy path: what U4 read"
  expect_contains "$out" "FACT restore: PASS" "the happy path: the restore row"
  expect_contains "$out" "left nothing ON the repository, because a review pushes no branch" "the happy path: what stays behind"
  expect_contains "$out" "FACT scratch-removed: PASS" "the happy path: the scratch row"
  expect_contains "$out" "CHECK 611 RESULT END (exit 0)" "the happy path: the end line"
  assert_scratch_removed "$dir" "the happy path"
  assert_reads_only "$dir" "the happy path"
  assert_no_launch "$dir" "the happy path"
  # The ONE review, launched from the checkout's own repository in the
  # installation the checkout belongs to, and the reads that follow it.
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "review --pr $pr --project $install --json (in $dir/sandbox)" "the happy path: the one launch"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "runs ls --repo $repo --pr $pr --project $install --no-cache --json" "the happy path: the listing it polled"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "review show $run_slug --project $install --json" "the happy path: the verdict it read back"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "doctor --json (in $dir/sandbox)" "the happy path: the reads are made in the checkout"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "run jobs list --project=$gcp_project --region=$region --format=json" "the happy path: the job list"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "jsonPayload.run=\"$run_id\"" "the happy path: the service's lines are read for the run's own id"
  # The read asks for the run's OLDEST lines, and asks back far enough to hold the
  # run's first one: the knob (1h) is the floor here, and the review took no
  # longer than it.
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "--limit=200 --order=asc" "the happy path: the lines are read oldest-first"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "--freshness=1h" "the happy path: the window is the knob when it is the wider one"
}

test_a_checkout_that_is_not_bitbucket_is_refused() {
  local dir=$work/not-bitbucket
  prepare "$dir"
  run_check "$dir" "FAKE_ORIGIN=https://github.com/acme/app.git"
  expect_status "$status" 1 "a GitHub checkout"
  # A guard that stops the check records its reason as the `aborted` row and
  # leaves the row it was reading as one that was not measured — nothing about
  # this installation was read, and the block says so rather than passing it.
  expect_contains "$err" "is not a Bitbucket repository (its host is 'github.com')" "a GitHub checkout: the refusal says which host it read"
  expect_contains "$err" "this check is the BITBUCKET half" "a GitHub checkout: the refusal says why"
  expect_contains "$out" "FACT aborted: FAIL" "a GitHub checkout: the reason is in the block"
  expect_contains "$out" "FACT sandbox: FAIL — not reached" "a GitHub checkout: the sandbox row was not measured"
  expect_absent "$out" "FACT policy: PASS" "a GitHub checkout: nothing is measured about the installation"
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
  run_check "$dir" CHECK611_SANDBOX=
  expect_status "$status" 2 "no sandbox at all"
  expect_contains "$err" "Check 611 needs --sandbox" "no sandbox at all: the reason"
  assert_no_launch "$dir" "no sandbox at all"
}

test_a_path_that_is_not_a_checkout_stops_the_check() {
  local dir=$work/not-a-checkout
  prepare "$dir"
  run_check "$dir" CHECK611_SANDBOX="$dir" CHECK611_KEEP_SCRATCH=
  expect_status "$status" 2 "a path that is not a checkout"
  expect_contains "$err" "carries no fugaro.yaml" "a path that is not a checkout: the reason"
  assert_no_launch "$dir" "a path that is not a checkout"
}

test_a_pull_request_that_is_not_a_number_is_refused() {
  local dir=$work/odd-pr
  prepare "$dir"
  run_check "$dir" CHECK611_PR=abc
  expect_status "$status" 2 "a pull request that is not a number"
  expect_contains "$err" "which is not a number" "a pull request that is not a number: the reason"
  assert_no_launch "$dir" "a pull request that is not a number"
}

test_xtrace_is_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "a traced shell"
  expect_contains "$out" "FACT xtrace: FAIL" "a traced shell: the guard is in the block"
  expect_contains "$err" "this shell is tracing (bash -x)" "a traced shell: the reason"
  expect_contains "$err" "prints its own RESULT block" "a traced shell: what to do instead"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "a traced shell: nothing is read first"
  assert_no_launch "$dir" "a traced shell"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$out" "FACT agent-session: FAIL" "a coding agent's session: the guard is in the block"
  expect_contains "$err" "CLAUDECODE is set, so this is a coding agent's session" "a coding agent's session: the marker is named"
  expect_contains "$err" "no agent runs any step of it" "a coding agent's session: whose check it is"
  assert_no_launch "$dir" "a coding agent's session"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" CHECK611_SANDBOX="$dir/belong-sandbox"
  expect_status "$status" 1 "a sandbox naming belong"
  expect_contains "$out" "FACT belong: FAIL" "a sandbox naming belong: the guard is in the block"
  expect_contains "$err" "names belong: this check is never run there" "a sandbox naming belong: the reason"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "a sandbox naming belong: nothing is read first"
  assert_no_launch "$dir" "a sandbox naming belong"
}

test_belong_is_refused_where_it_is_read_from_the_installation() {
  # The DIRECTORY the case works in must not name belong either: the guard reads
  # the path this check was given, and a work directory that said belong would
  # trip it before the installation was ever read.
  local dir=$work/odd-install
  prepare "$dir"
  # The path is clean and the INSTALLATION is not: the guard runs a second time
  # over what the checkout itself said, so a sandbox clone of a belong
  # installation is refused before anything is launched there.
  python3 - "$dir/json/doctor.json" <<'PY'
import json, sys
p = sys.argv[1]
doc = json.load(open(p))
doc["project"]["name"] = "belong"
json.dump(doc, open(p, "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "an installation naming belong"
  expect_contains "$err" "belongs to belong" "an installation naming belong: the refusal names it"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "review --pr" "an installation naming belong: no review is launched"
  assert_no_launch "$dir" "an installation naming belong"
}

test_belong_is_refused_in_the_repository_name() {
  local dir=$work/odd-repo-name
  prepare "$dir"
  run_check "$dir" "FAKE_ORIGIN=https://bitbucket.org/acme/belong-sandbox.git"
  expect_status "$status" 1 "a repository naming belong"
  expect_contains "$err" "belongs to $install ($gcp_project, region $region) and acme/belong-sandbox: this check is never run there" "a repository naming belong: the reason"
  assert_no_launch "$dir" "a repository naming belong"
}

test_a_policy_that_could_not_be_read_leaves_the_rows_unmeasured() {
  local dir=$work/policy-fails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=policy-fails
  expect_status "$status" 2 "a policy that could not be read"
  expect_contains "$out" "FACT policy: FAIL — fugaro project policy show --project $install --json did not answer" "a policy that could not be read: the row"
  expect_contains "$out" "FACT launch: FAIL — nothing is launched without the repository's own brokering" "a policy that could not be read: nothing is launched"
  expect_contains "$out" "FACT restore: PASS" "a policy that could not be read: nothing was created"
  assert_no_launch "$dir" "a policy that could not be read"
  assert_scratch_removed "$dir" "a policy that could not be read"
}

test_a_repository_the_project_does_not_admit_is_not_measured() {
  local dir=$work/not-admitted
  prepare "$dir"
  run_check "$dir" FAKE_POLICY_FILE=policy-not-admitted.json
  expect_status "$status" 2 "a repository the project does not admit"
  expect_contains "$out" "FACT policy: FAIL — the published policy of $install does not admit $repo to the PROJECT" "a repository the project does not admit: the row"
  expect_contains "$out" "fugaro project repos add" "a repository the project does not admit: what to do about it"
  assert_no_launch "$dir" "a repository the project does not admit"
}

test_a_repository_a_profile_admits_is_a_finding() {
  local dir=$work/profile
  prepare "$dir"
  # The premise of the plan's sandbox is a repository with NO profiles block. A
  # repository a profile admits has a shared project job, and a review of it runs
  # on that profile's review variant — so this is a FAIL and not a reading of the
  # implicit job: the sandbox is the wrong one.
  run_check "$dir" FAKE_POLICY_FILE=policy-with-profile.json
  expect_status "$status" 1 "a repository a profile admits"
  expect_contains "$out" "FACT policy: FAIL — 1 profile(s) of $install's published policy admit $repo ('bb' among them)" "a repository a profile admits: the row"
  expect_contains "$out" "not the installation's implicit review-only job, which is what this check is about" "a repository a profile admits: why it matters"
  expect_contains "$out" "the plan's premise, the sandbox repository with no profiles block" "a repository a profile admits: what to do about it"
  assert_no_launch "$dir" "a repository a profile admits"
}

test_a_profile_of_another_provider_does_not_admit_the_repository() {
  local dir=$work/other-provider
  prepare "$dir"
  # A profile of another provider that writes no allow_repos of its own falls
  # back to the project's list — and projectpolicy.Admits gates on the provider
  # first, so the repository is still on no profile and the check is right to
  # run: this is the case that says the reading is not "list contains the repo".
  run_check "$dir" FAKE_POLICY_FILE=policy-other-provider.json
  expect_status "$status" 0 "a profile of another provider"
  expect_contains "$out" "FACT policy: PASS" "a profile of another provider: the row passes"
  expect_contains "$out" "the document defines 1 profile(s) and none of them admits it" "a profile of another provider: what it read"
}

test_a_repository_the_node_does_not_broker_is_a_finding() {
  local dir=$work/no-proxy
  prepare "$dir"
  run_check "$dir" FAKE_POLICY_FILE=policy-no-proxy.json
  expect_status "$status" 1 "a node that does not broker the repository"
  expect_contains "$out" "FACT policy: FAIL — $repo is on no profile (which is this check's premise), and the published node does not broker its Bitbucket access: its bitbucket_token block reads proxy=no" "a node that does not broker: the row"
  expect_contains "$out" "a review launched there would open the repository with no token and stop at its first git call" "a node that does not broker: the decision it breaks"
  assert_no_launch "$dir" "a node that does not broker"
}

test_a_node_that_does_not_admit_the_repository_is_a_finding() {
  local dir=$work/token-not-admitted
  prepare "$dir"
  run_check "$dir" FAKE_POLICY_FILE=policy-token-not-admitted.json
  expect_status "$status" 1 "a node that does not admit the repository"
  expect_contains "$out" "allow_repos no for this repository" "a node that does not admit the repository: the row reads both halves"
  assert_no_launch "$dir" "a node that does not admit the repository"
}

test_a_policy_with_no_budget_mode_is_not_measured() {
  local dir=$work/no-mode
  prepare "$dir"
  run_check "$dir" FAKE_POLICY_FILE=policy-no-mode.json
  expect_status "$status" 2 "a policy with no budget mode"
  expect_contains "$out" "FACT policy: FAIL — $repo is on no profile and the policy document carries no budget mode (it reads '<none>')" "a policy with no budget mode: the row"
  expect_contains "$out" "a mode the document does not carry is neither" "a policy with no budget mode: why it is not a finding"
  assert_no_launch "$dir" "a policy with no budget mode"
}

test_a_doctor_that_is_not_a_document_stops_the_check() {
  local dir=$work/doctor-garbage
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-garbage
  expect_status "$status" 2 "a doctor that is not a document"
  expect_contains "$out" "FACT sandbox: FAIL — the doctor document" "a doctor that is not a document: the row"
  assert_no_launch "$dir" "a doctor that is not a document"
}

test_a_doctor_that_never_answers_stops_the_check() {
  local dir=$work/doctor-hangs
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-hangs CHECK611_DOCTOR_TIMEOUT_SECONDS=2
  expect_status "$status" 2 "a doctor that never answers"
  expect_contains "$out" "FACT sandbox: FAIL — fugaro doctor --json did not answer" "a doctor that never answers: the row"
  expect_contains "$out" "every row below is about it" "a doctor that never answers: what it refuses to conclude"
  assert_scratch_removed "$dir" "a doctor that never answers"
}

test_a_doctor_exit_1_is_an_answer() {
  local dir=$work/doctor-warning
  prepare "$dir"
  # The real doctor exits 1 whenever any of its checks is not ok and prints its
  # document all the same: a warning is not a document that could not be read.
  run_check "$dir" FAKE_DOCTOR_STATUS=1
  expect_status "$status" 0 "a doctor that exited 1 with a document"
  expect_contains "$out" "FACT sandbox: PASS" "a doctor that exited 1 with a document: the row passes"
}

test_a_launch_that_failed_leaves_the_rows_unmeasured() {
  local dir=$work/launch-fails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=review-fails
  expect_status "$status" 2 "a launch that failed"
  expect_contains "$out" "FACT launch: FAIL — fugaro review --pr $pr for $repo exited 1 and launched nothing" "a launch that failed: the row"
  expect_contains "$out" "is not enrolled in the project" "a launch that failed: the launcher's own sentence is carried"
  expect_contains "$out" "FACT run: FAIL — no review was launched, so there is no row to read" "a launch that failed: nothing is read about a run"
  expect_contains "$out" "FACT restore: PASS — no review was launched, so nothing was created" "a launch that failed: nothing was created"
  assert_no_launch "$dir" "a launch that failed"
}

test_a_launch_that_never_answers_is_not_measured() {
  local dir=$work/launch-hangs
  prepare "$dir"
  run_check "$dir" FAKE_MODE=review-hangs CHECK611_CLI_TIMEOUT_SECONDS=2
  expect_status "$status" 2 "a launch that never answered"
  expect_contains "$out" "FACT launch: FAIL — fugaro review --pr $pr did not answer inside 2s" "a launch that never answered: the row"
  expect_contains "$out" "it may or may not have launched the review" "a launch that never answered: what it refuses to conclude"
  assert_scratch_removed "$dir" "a launch that never answered"
}

test_a_launch_on_another_job_is_a_finding() {
  local dir=$work/other-job
  prepare "$dir"
  # The launch document names a job that is not the installation's implicit
  # review-only job of provider bitbucket: the review ran on something else — a
  # per-repository or per-profile job — and the whole check is about the
  # implicit one.
  run_check "$dir" FAKE_LAUNCH_FILE=launch-other-job.json
  expect_status "$status" 1 "a review on another job"
  expect_contains "$out" "FACT launch: FAIL — the launch resolved the job '$other_job'" "a review on another job: the row"
  expect_contains "$out" "the installation's IMPLICIT review-only job of provider bitbucket is named '$bb_job'" "a review on another job: the name the release derives"
  expect_contains "$out" "A review that resolved another job ran on a per-repository job or on a profile's variant" "a review on another job: why it matters"
  expect_absent "$out" "FACT U4: FAIL" "a review on another job: U4 is still read off the job list it got"
}

test_a_launch_as_another_account_is_a_finding() {
  local dir=$work/other-account
  prepare "$dir"
  run_check "$dir" FAKE_LAUNCH_FILE=launch-other-account.json
  expect_status "$status" 1 "a review as another account"
  expect_contains "$out" "FACT launch: FAIL — the launch resolved the account 'spare'" "a review as another account: the row"
  expect_contains "$out" "the implicit review-only job of provider bitbucket serves ONE account: the default one" "a review as another account: which one"
  expect_contains "$out" "FACT U3: FAIL — the review ran as the account 'spare' and not as the default one" "a review as another account: U3 follows it"
}

test_a_launch_that_is_already_launched_reads_the_same_run() {
  local dir=$work/already
  prepare "$dir"
  # A second `fugaro review` for the same pull request answers already-launched
  # and names the same run: the row is about the review, not about who asked.
  run_check "$dir" FAKE_LAUNCH_FILE=launch-already.json
  expect_status "$status" 0 "a launch that was already done"
  expect_contains "$out" "fugaro review --pr $pr answered 'already-launched'" "a launch that was already done: the row"
  expect_contains "$out" "FACT run: PASS" "a launch that was already done: the run row still reads"
}

test_a_launch_document_that_is_not_one_is_not_measured() {
  local dir=$work/odd-launch
  prepare "$dir"
  run_check "$dir" FAKE_LAUNCH_FILE=launch-odd-status.json
  expect_status "$status" 2 "a launch document with an odd status"
  expect_contains "$out" "FACT launch: FAIL — the launch document reads status='refused'" "a launch document with an odd status: the row"
  expect_contains "$out" "a document it cannot read as one is nothing about that review" "a launch document with an odd status: why it is not a finding"
}

test_a_launch_document_about_another_pull_request_is_a_finding() {
  local dir=$work/launch-other-pr
  prepare "$dir"
  run_check "$dir" FAKE_LAUNCH_FILE=launch-other-pr.json
  expect_status "$status" 1 "a launch document about another pull request"
  expect_contains "$out" "FACT launch: FAIL — the launch document is about $repo#7, and this check asked for a review of $repo#$pr" "a launch document about another pull request: the row"
  expect_contains "$out" "a launch this check cannot say it asked for" "a launch document about another pull request: why"
  expect_contains "$out" "FACT job: PASS" "a launch document about another pull request: the rows below are still read, by the run id the document names"
}

test_a_launch_document_with_no_run_id_is_not_measured() {
  local dir=$work/no-run-id
  prepare "$dir"
  run_check "$dir" FAKE_LAUNCH_FILE=launch-no-run.json
  expect_status "$status" 2 "a launch document with no run id"
  expect_contains "$out" "FACT launch: FAIL — the launch document names no run id" "a launch document with no run id: the row"
  expect_contains "$out" "leaves every row below unmeasured" "a launch document with no run id: why"
}

test_a_listing_that_could_not_be_read_leaves_the_rows_unmeasured() {
  local dir=$work/listing-fails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=listing-fails
  expect_status "$status" 2 "a listing that could not be read"
  expect_contains "$out" "FACT run: FAIL — the review run ($run_id) does not appear in 'fugaro runs ls --pr $pr --repo $repo' after " "a listing that could not be read: the row"
  expect_contains "$out" "(the last read exited 1)" "a listing that could not be read: the row says the read failed"
  expect_contains "$out" "FACT review: FAIL — no terminal row of this check's own review run was read" "a listing that could not be read: the verdict is not read either"
  expect_contains "$out" "FACT git: FAIL — no terminal row" "a listing that could not be read: nor the service's lines"
}

test_a_run_the_listing_never_names_is_not_measured() {
  local dir=$work/other-run-row
  prepare "$dir"
  run_check "$dir" FAKE_ROW_FILE=row-other-run.json
  expect_status "$status" 2 "a run the listing never names"
  expect_contains "$out" "FACT run: FAIL — the review run ($run_id) does not appear in" "a run the listing never names: the row"
  expect_contains "$out" "fugaro runs show $run_id --repo $repo" "a run the listing never names: what to read instead"
}

test_a_run_that_had_not_finished_is_not_measured() {
  local dir=$work/inflight
  prepare "$dir"
  run_check "$dir" FAKE_ROW_FILE=row-inflight.json CHECK611_RUN_SECONDS=2
  expect_status "$status" 2 "a run that had not finished"
  expect_contains "$out" "FACT run: FAIL — the review run ($run_id) had not reached a final status after 2s" "a run that had not finished: the row"
  expect_contains "$out" "raise CHECK611_RUN_SECONDS" "a run that had not finished: what to do"
}

test_a_run_that_failed_is_a_finding() {
  local dir=$work/failed
  prepare "$dir"
  run_check "$dir" FAKE_ROW_FILE=row-failed.json
  expect_status "$status" 1 "a review run that failed"
  expect_contains "$out" "FACT run: FAIL — the review run ($run_id) of $repo#$pr did not end as a review that finished: status=failed" "a review run that failed: the row"
  expect_contains "$out" "its git traffic and its model calls both go through the credentials service" "a review run that failed: why the job's plainness matters"
  expect_contains "$out" "halt: the checkout failed: no credential" "a review run that failed: the halt it read"
  # The rows about the run are read all the same: a failed review still has its
  # own job, and the service's lines for it are what say whether its calls landed.
  expect_contains "$out" "FACT job: PASS" "a review run that failed: the job is still read"
  expect_contains "$out" "FACT model: PASS" "a review run that failed: the service's lines are still read"
}

test_a_run_that_pushed_is_a_finding() {
  local dir=$work/pushed
  prepare "$dir"
  run_check "$dir" FAKE_ROW_FILE=row-pushed.json
  expect_status "$status" 1 "a review run that pushed"
  expect_contains "$out" "pushed=yes (a review pushes nothing)" "a review run that pushed: the row"
}

test_a_run_that_read_another_pull_request_is_a_finding() {
  local dir=$work/other-pr
  prepare "$dir"
  run_check "$dir" FAKE_ROW_FILE=row-other-pr.json
  expect_status "$status" 1 "a review run that names another pull request"
  expect_contains "$out" "pr=7" "a review run that names another pull request: the row"
}

test_a_base_image_that_is_not_the_jobs_image_is_a_finding() {
  local dir=$work/other-image
  prepare "$dir"
  run_check "$dir" FAKE_JOBS_FILE=jobs-other-image.json
  expect_status "$status" 1 "a job on another image"
  expect_contains "$out" "FACT U1: FAIL — the review ran on '$bb_job', whose container image is us-east5-docker.pkg.dev/p/fugaro/agent:repo-built" "a job on another image: U1"
  expect_contains "$out" "deployed with the base image (infra's implicitReviewVariant takes lc.BaseImage(BaseKind))" "a job on another image: the rule it breaks"
}

test_an_account_that_does_not_admit_the_repository_is_a_finding() {
  local dir=$work/no-account
  prepare "$dir"
  run_check "$dir" FAKE_POLICY_FILE=policy-no-accounts.json
  expect_status "$status" 1 "an accounts block that admits nobody"
  expect_contains "$out" "FACT U2: FAIL — the default account's entry of $install's published accounts block does NOT admit $repo" "an accounts block that admits nobody: U2"
  expect_contains "$out" "the gap F4 names" "an accounts block that admits nobody: the gap"
}

test_an_installation_that_holds_no_project_job_is_a_finding() {
  local dir=$work/no-jobs
  prepare "$dir"
  run_check "$dir" FAKE_JOBS_FILE=jobs-empty.json
  expect_status "$status" 1 "an installation with no project job"
  expect_contains "$out" "FACT U4: FAIL — the installation holds NO project job at all" "an installation with no project job: U4"
  expect_contains "$out" "it is what this check's own review ran on" "an installation with no project job: why it cannot be so"
}

test_a_project_job_of_another_shape_is_a_finding() {
  local dir=$work/third-job
  prepare "$dir"
  # A third project job that is not the release's implicit review-only job: U4 is
  # "a live terraform apply with no project jobs", and a job with no profile
  # behind it is either one this release does not name or a leftover.
  run_check "$dir" FAKE_JOBS_FILE=jobs-third.json
  expect_status "$status" 1 "a project job of another shape"
  expect_contains "$out" "FACT U4: FAIL — the installation's project jobs are $other_job,$bb_job,$gh_job, and not all of them are the implicit review-only job this release's naming derives" "a project job of another shape: U4"
  expect_contains "$out" "a leftover of a profile that is gone" "a project job of another shape: what it could be"
}

test_a_jobs_list_that_could_not_be_read_is_not_measured() {
  local dir=$work/jobs-fails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=jobs-fails
  expect_status "$status" 2 "a job list that could not be read"
  expect_contains "$out" "FACT job: FAIL — gcloud run jobs list did not answer" "a job list that could not be read: the row"
  expect_contains "$out" "a read that did not answer is not a job that mounts nothing" "a job list that could not be read: what it refuses to conclude"
  expect_contains "$out" "FACT U4: FAIL — the installation's job list or the policy's profiles block was not read" "a job list that could not be read: U4 follows"
}

test_a_jobs_list_that_is_not_a_document_is_not_measured() {
  local dir=$work/jobs-garbage
  prepare "$dir"
  run_check "$dir" FAKE_MODE=jobs-garbage
  expect_status "$status" 2 "a job list that is not a document"
  expect_contains "$out" "FACT job: FAIL — gcloud run jobs list printed a document this check could not read" "a job list that is not a document: the row"
}

test_a_job_that_is_not_in_the_list_is_a_finding() {
  local dir=$work/job-missing
  prepare "$dir"
  run_check "$dir" FAKE_JOBS_FILE=jobs-missing.json
  expect_status "$status" 1 "the job the review ran on is not there"
  expect_contains "$out" "FACT job: FAIL — the job '$bb_job' the review ran on is not in the installation's job list" "the job is not there: the row"
  expect_contains "$out" "the project jobs it did list are $gh_job" "the job is not there: what it did list"
  expect_contains "$out" "FACT U1: FAIL — the job's image or the installation's base image was not read" "the job is not there: U1 is left unmeasured"
}

test_a_job_without_the_service_url_is_a_finding() {
  local dir=$work/job-no-url
  prepare "$dir"
  run_check "$dir" FAKE_JOBS_FILE=jobs-no-url.json
  expect_status "$status" 1 "a review job with no service address"
  expect_contains "$out" "FACT job: FAIL — the job '$bb_job' the review ran on carries no FUGARO_CREDENTIALS_SERVICE_URL" "no service address: the row"
  expect_contains "$out" "the service's address is the ONLY way its clone and its model calls reach anything" "no service address: why"
}

test_a_job_that_carries_a_bitbucket_token_is_a_finding() {
  local dir=$work/job-token
  prepare "$dir"
  run_check "$dir" FAKE_JOBS_FILE=jobs-token.json
  expect_status "$status" 1 "a review job that carries a Bitbucket token"
  expect_contains "$out" "FACT job: FAIL — the job '$bb_job' the review ran on names FUGARO_BITBUCKET_TOKEN" "a Bitbucket token: the row"
  expect_contains "$out" "a Bitbucket review runs on a job that mounts NO Bitbucket token (D2302, RJ8)" "a Bitbucket token: the rule"
}

test_a_job_that_carries_the_secret_env_list_is_a_finding() {
  local dir=$work/job-secretenvs
  prepare "$dir"
  run_check "$dir" FAKE_JOBS_FILE=jobs-secretenvs.json
  expect_status "$status" 1 "a review job that names FUGARO_SECRET_ENVS"
  expect_contains "$out" "FACT job: FAIL — the job '$bb_job' the review ran on carries a SECRET (FUGARO_SECRET_ENVS is named" "the secret-env list: the row"
  expect_contains "$out" "the review-only variant mounts nothing at all, by construction" "the secret-env list: the rule"
}

test_a_job_that_mounts_a_secret_key_is_a_finding() {
  local dir=$work/job-secretref
  prepare "$dir"
  run_check "$dir" FAKE_JOBS_FILE=jobs-secretref.json
  expect_status "$status" 1 "a review job that mounts a secret key"
  expect_contains "$out" "1 secret reference(s) — a valueFrom.secretKeyRef" "a secret key: the row counts it"
}

test_a_job_that_mounts_a_secret_volume_is_a_finding() {
  local dir=$work/job-secretvolume
  prepare "$dir"
  run_check "$dir" FAKE_JOBS_FILE=jobs-secretvolume.json
  expect_status "$status" 1 "a review job that mounts a secret volume"
  expect_contains "$out" "or a volume whose source is a secret" "a secret volume: the row reads both shapes"
  expect_contains "$out" "1 secret reference(s)" "a secret volume: it is counted"
}

test_a_verdict_that_could_not_be_read_is_not_measured() {
  local dir=$work/no-artifact
  prepare "$dir"
  run_check "$dir" FAKE_MODE=artifact-fails
  expect_status "$status" 2 "a review whose artifact could not be read"
  expect_contains "$out" "FACT review: FAIL — fugaro review show $run_slug exited 1" "no artifact: the row"
  expect_contains "$out" "has no review artifact" "no artifact: the command's own sentence is carried"
  expect_contains "$out" "this check will not read a missing artifact as a verdict" "no artifact: what it refuses to conclude"
}

test_a_verdict_that_is_not_a_document_is_not_measured() {
  local dir=$work/artifact-garbage
  prepare "$dir"
  run_check "$dir" FAKE_MODE=artifact-garbage FAKE_ARTIFACT_FILE=artifact-list.json
  expect_status "$status" 2 "a verdict that is not a document"
  expect_contains "$out" "FACT review: FAIL — fugaro review show --json exited 0 and printed a document that is not a verdict object" "a verdict that is not a document: the row"
}

test_a_verdict_about_another_repository_is_a_finding() {
  local dir=$work/artifact-other-repo
  prepare "$dir"
  run_check "$dir" FAKE_ARTIFACT_FILE=artifact-other-repo.json
  expect_status "$status" 1 "a verdict about another repository"
  expect_contains "$out" "FACT review: FAIL — the verdict read back from the review run ($run_id) is about bb-owner/elsewhere" "a verdict about another repository: the row"
  expect_contains "$out" "a verdict is acted on against the pull request and the head it names" "a verdict about another repository: why"
}

test_a_verdict_about_another_pull_request_is_a_finding() {
  local dir=$work/artifact-other-pr
  prepare "$dir"
  run_check "$dir" FAKE_ARTIFACT_FILE=artifact-other-pr.json
  expect_status "$status" 1 "a verdict about another pull request"
  expect_contains "$out" "is about $repo#7, and this check reviewed $repo#$pr" "a verdict about another pull request: the row"
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

test_a_review_the_service_wrote_no_git_line_for_is_a_finding() {
  local dir=$work/log-no-git
  prepare "$dir"
  run_check "$dir" FAKE_LOG_FILE=log-no-git.json
  expect_status "$status" 1 "a review the service wrote no git line for"
  expect_contains "$out" "FACT git: FAIL — the service wrote NO served git line (purpose 'git', status ok) for the review run ($run_id) of $repo, while that run finished and its verdict was read back" "no git line: the git row"
  expect_contains "$out" "a run whose git traffic was served by something else is a job that still holds a token" "no git line: the decision it breaks"
  expect_contains "$out" "The purposes the service's own lines for this run carry: forward" "no git line: what it read instead"
}

test_a_git_line_that_was_not_served_is_not_the_git_row() {
  local dir=$work/git-refused
  prepare "$dir"
  run_check "$dir" FAKE_LOG_FILE=log-git-refused.json
  expect_status "$status" 1 "a git line that was not served"
  expect_contains "$out" "FACT git: FAIL — the service wrote NO served git line" "a git line that was not served: the row reads the status and not the purpose alone"
  expect_contains "$out" "The purposes the service's own lines for this run carry: forward git" "a git line that was not served: the purposes it did carry"
}

test_a_review_with_no_served_model_line_is_a_finding() {
  local dir=$work/no-forward
  prepare "$dir"
  run_check "$dir" FAKE_LOG_FILE=log-no-forward.json
  expect_status "$status" 1 "a review with no served model line"
  expect_contains "$out" "FACT git: PASS" "a review with no served model line: the git row passes"
  expect_contains "$out" "FACT model: FAIL — the service wrote no served model line (purpose 'forward', status ok) for the review run ($run_id)" "a review with no served model line: the model row"
  expect_contains "$out" "its model calls can only have been answered by the credentials service's own forward" "a review with no served model line: why"
}

test_a_vertex_installation_with_no_forward_line_is_not_a_finding() {
  local dir=$work/vertex
  prepare "$dir"
  # On `agent.auth: vertex` the agent's model calls do not go through the
  # credentials service at all, so no forward line is the expected reading: U3's
  # Vertex half is exactly this, read from the job's own environment.
  run_check "$dir" FAKE_JOBS_FILE=jobs-vertex.json FAKE_LOG_FILE=log-no-forward.json
  expect_status "$status" 0 "a Vertex installation with no forward line"
  expect_contains "$out" "FACT model: PASS — the service wrote no served model line (purpose 'forward') for the review run ($run_id), and the job it ran on ('$bb_job') carries the two names an \`agent.auth: vertex\` run needs" "a Vertex installation: the row"
  expect_contains "$out" "FACT U3: PASS" "a Vertex installation: U3 reads the job's own environment"
  expect_contains "$out" "reading 'yes'" "a Vertex installation: what U3 read"
}

test_a_first_model_call_under_the_projects_own_key_passes() {
  local dir=$work/project-key
  prepare "$dir"
  # The project's own Anthropic API key is not an account of the node's list, and
  # a call served with it is not a finding: it is U3's API-key case, read from the
  # outside.
  run_check "$dir" FAKE_LOG_FILE=log-project-key.json
  expect_status "$status" 0 "a first model call under the project's own key"
  expect_contains "$out" "FACT model: PASS — the review run's first model call ($run_id) was served with the credential 'anthropic-api-key--project'" "the project's own key: the row"
  expect_contains "$out" "which is not a Claude account but the project's own key" "the project's own key: why it is not a finding"
}

test_a_first_model_call_under_the_provider_key_passes() {
  local dir=$work/provider-key
  prepare "$dir"
  run_check "$dir" FAKE_LOG_FILE=log-provider-key.json
  expect_status "$status" 0 "a first model call under the provider key"
  expect_contains "$out" "was served with the credential 'provider-key--project'" "the provider key: the row"
}

test_a_first_model_call_with_another_credential_is_a_finding() {
  local dir=$work/other-credential
  prepare "$dir"
  run_check "$dir" FAKE_LOG_FILE=log-other-credential.json
  expect_status "$status" 1 "a first model call with another credential"
  expect_contains "$out" "FACT model: FAIL — the review run's first model call ($run_id) was served with the credential 'spare-account', which is neither one of the accounts the node admits for $repo (default)" "another credential: the row"
  expect_contains "$out" "spare-account|B|forward" "another credential: the lines it read are named"
}

test_the_first_model_call_is_read_by_its_time() {
  local dir=$work/two-forwards
  prepare "$dir"
  # The document's LAST model line is the run's FIRST call: the row has to read
  # the earliest served forward line by its own timestamp, not the first one it
  # happens to walk past.
  run_check "$dir" FAKE_LOG_FILE=log-two-forwards.json
  expect_status "$status" 0 "two served model calls"
  expect_contains "$out" "FACT model: PASS — the review run's first model call ($run_id) was served with the credential 'default'" "two served model calls: the earliest is judged"
  expect_absent "$out" "served with the credential 'spare-account'" "two served model calls: the later switch is not the first call"
}

test_the_first_model_call_is_read_from_the_runs_own_start() {
  local dir=$work/late-switch
  prepare "$dir"
  # A run of seven lines whose first model line is the default account and whose
  # last three are another credential's, read three at a time: `gcloud logging
  # read` answers the NEWEST three unless `--order=asc` says otherwise, and that
  # answer's first forward line is a later call's credential — the false FAIL this
  # row must never take. CHECK611_LOG_AGE is bent below the run's own duration
  # too, so the window has to come from the launch and not from the knob.
  run_check "$dir" FAKE_LOG_FILE=log-late-switch.json CHECK611_LOG_LIMIT=3 CHECK611_LOG_AGE=1m
  expect_status "$status" 0 "a review that switched credential after its first calls"
  expect_contains "$out" "FACT model: PASS — the review run's first model call ($run_id) was served with the credential 'default'" "the FIRST call decides the row"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "--limit=3 --order=asc" "the run's oldest lines are the ones asked for"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "--freshness=" "the window is asked for"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "--freshness=1m" "a knob narrower than the run is widened to the launch, not used as written"
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
    e["jsonPayload"]["run"] = "20261118-080000-9999"
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
  run_check "$dir" CHECK611_KEEP_SCRATCH=1
  expect_status "$status" 0 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: PASS — kept on request (CHECK611_KEEP_SCRATCH is set), at " "a kept scratch directory: the row says where it is"
  expect_contains "$err" "CHECK611_KEEP_SCRATCH is set" "a kept scratch directory: the note"
  [ -n "$scratch" ] || fail "a kept scratch directory: the check named no scratch directory"
  if [ -n "$scratch" ] && [ -d "$scratch" ]; then
    rm -rf "$scratch"
  else
    fail "a kept scratch directory: the directory was removed although CHECK611_KEEP_SCRATCH is set"
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

test_every_row_the_register_quotes_is_in_the_block() {
  local dir=$work/block-keys
  prepare "$dir"
  run_check "$dir"
  # Every key the register quotes is in the block, in one order, whatever
  # happened: a key the run never reached reads FAIL — not reached.
  local key
  for key in fugaro-version xtrace agent-session belong sandbox policy launch run job review git model U1 U2 U3 U4 restore scratch-removed; do
    expect_contains "$out" "FACT $key:" "the block: the $key row is printed"
  done
  expect_absent "$out" "FAIL — not reached" "the block: nothing is missing on the happy path"
}

# ------------------------------------------------------- mutations of the check
# A case that asserts a row reads FAIL is worth nothing if a copy whose row reads
# PASS goes unnoticed: each case below runs a mutant of check611.sh whose one row
# was bent, and asserts that the mutant's block really does read PASS and that its
# exit status really does differ from what the case above expects. A suite of
# mutations is what says those assertions are load-bearing.
mutate() { # mutate DIR SED... > the copy's path on stdout
  local dir=$1
  shift
  local copy=$dir/mutated611.sh
  sed "$@" "$script" >"$copy"
  printf '%s' "$copy"
}

test_mutation_a_job_that_carries_a_token_always_passes() {
  local dir=$work/mutation-token
  prepare "$dir"
  local copy
  copy=$(mutate "$dir" 's|^\( *\)fact job FAIL \("the job .*names FUGARO_BITBUCKET_TOKEN\)|\1fact job PASS \2|')
  if ! grep -q 'fact job PASS "the job .*names FUGARO_BITBUCKET_TOKEN' "$copy"; then
    fail "mutation: the sed found no place to break the Bitbucket-token row"
    return 0
  fi
  run_script "$copy" "$dir" FAKE_JOBS_FILE=jobs-token.json
  expect_contains "$out" "FACT job: PASS" "mutation: the broken row reports PASS, so test_a_job_that_carries_a_bitbucket_token_is_a_finding is not vacuous"
  expect_status "$status" 0 "mutation: a job that carries a token and is reported as fine ends the check as one that measured everything — which is exactly what the case above refuses"
}

test_mutation_a_review_with_no_git_line_always_passes() {
  local dir=$work/mutation-git
  prepare "$dir"
  local copy
  copy=$(mutate "$dir" 's|^\( *\)fact git FAIL \("the service wrote NO served git line\)|\1fact git PASS \2|')
  if ! grep -q 'fact git PASS "the service wrote NO served git line' "$copy"; then
    fail "mutation: the sed found no place to break the git row"
    return 0
  fi
  run_script "$copy" "$dir" FAKE_LOG_FILE=log-no-git.json
  expect_contains "$out" "FACT git: PASS" "mutation: the broken row reports PASS, so test_a_review_the_service_wrote_no_git_line_for_is_a_finding is not vacuous"
  expect_status "$status" 0 "mutation: a review whose git traffic went unserved and is reported as fine ends the check as one that measured everything"
}

test_mutation_the_belong_guard_stops_stopping() {
  local dir=$work/mutation-belong
  prepare "$dir"
  local copy
  # The guard's stop sits in the *belong*) arm of its case, not at the start of
  # the line, so the anchor names the arm.
  copy=$(mutate "$dir" 's|^\( *\)\*belong\*) stop \("a value this check was given or read names belong\)|\1*belong*) : \2|')
  if ! grep -q '\*belong\*) : "a value this check was given or read names belong' "$copy"; then
    fail "mutation: the sed found no place to disarm the belong guard"
    return 0
  fi
  run_script "$copy" "$dir" CHECK611_SANDBOX="$dir/belong-sandbox"
  expect_contains "$out" "FACT belong: PASS" "mutation: the disarmed guard reports PASS, so test_belong_is_refused_by_name is not vacuous"
  expect_status "$status" 2 "mutation: with the guard disarmed the run goes on and dies on the path it was given, which is a different end than the case above expects"
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
t test_a_checkout_that_is_not_bitbucket_is_refused
t test_a_credential_in_the_origin_is_never_printed
t test_an_origin_that_names_no_repository_is_refused
t test_a_checkout_with_no_origin_is_refused
t test_a_missing_sandbox_is_refused
t test_a_path_that_is_not_a_checkout_stops_the_check
t test_a_pull_request_that_is_not_a_number_is_refused
t test_xtrace_is_refused
t test_a_coding_agents_session_is_refused
t test_belong_is_refused_by_name
t test_belong_is_refused_where_it_is_read_from_the_installation
t test_belong_is_refused_in_the_repository_name
t test_a_policy_that_could_not_be_read_leaves_the_rows_unmeasured
t test_a_repository_the_project_does_not_admit_is_not_measured
t test_a_repository_a_profile_admits_is_a_finding
t test_a_profile_of_another_provider_does_not_admit_the_repository
t test_a_repository_the_node_does_not_broker_is_a_finding
t test_a_node_that_does_not_admit_the_repository_is_a_finding
t test_a_policy_with_no_budget_mode_is_not_measured
t test_a_doctor_that_is_not_a_document_stops_the_check
t test_a_doctor_that_never_answers_stops_the_check
t test_a_doctor_exit_1_is_an_answer
t test_a_launch_that_failed_leaves_the_rows_unmeasured
t test_a_launch_that_never_answers_is_not_measured
t test_a_launch_on_another_job_is_a_finding
t test_a_launch_as_another_account_is_a_finding
t test_a_launch_that_is_already_launched_reads_the_same_run
t test_a_launch_document_that_is_not_one_is_not_measured
t test_a_launch_document_about_another_pull_request_is_a_finding
t test_a_launch_document_with_no_run_id_is_not_measured
t test_a_listing_that_could_not_be_read_leaves_the_rows_unmeasured
t test_a_run_the_listing_never_names_is_not_measured
t test_a_run_that_had_not_finished_is_not_measured
t test_a_run_that_failed_is_a_finding
t test_a_run_that_pushed_is_a_finding
t test_a_run_that_read_another_pull_request_is_a_finding
t test_a_base_image_that_is_not_the_jobs_image_is_a_finding
t test_an_account_that_does_not_admit_the_repository_is_a_finding
t test_an_installation_that_holds_no_project_job_is_a_finding
t test_a_project_job_of_another_shape_is_a_finding
t test_a_jobs_list_that_could_not_be_read_is_not_measured
t test_a_jobs_list_that_is_not_a_document_is_not_measured
t test_a_job_that_is_not_in_the_list_is_a_finding
t test_a_job_without_the_service_url_is_a_finding
t test_a_job_that_carries_a_bitbucket_token_is_a_finding
t test_a_job_that_carries_the_secret_env_list_is_a_finding
t test_a_job_that_mounts_a_secret_key_is_a_finding
t test_a_job_that_mounts_a_secret_volume_is_a_finding
t test_a_verdict_that_could_not_be_read_is_not_measured
t test_a_verdict_that_is_not_a_document_is_not_measured
t test_a_verdict_about_another_repository_is_a_finding
t test_a_verdict_about_another_pull_request_is_a_finding
t test_a_log_read_that_failed_leaves_the_served_rows_unmeasured
t test_a_review_the_service_wrote_no_git_line_for_is_a_finding
t test_a_git_line_that_was_not_served_is_not_the_git_row
t test_a_review_with_no_served_model_line_is_a_finding
t test_a_vertex_installation_with_no_forward_line_is_not_a_finding
t test_a_first_model_call_under_the_projects_own_key_passes
t test_a_first_model_call_under_the_provider_key_passes
t test_a_first_model_call_with_another_credential_is_a_finding
t test_the_first_model_call_is_read_by_its_time
t test_the_first_model_call_is_read_from_the_runs_own_start
t test_a_line_of_another_run_is_not_read
t test_kept_scratch_is_reported
t test_an_unknown_argument_is_refused
t test_every_row_the_register_quotes_is_in_the_block
t test_mutation_a_job_that_carries_a_token_always_passes
t test_mutation_a_review_with_no_git_line_always_passes
t test_mutation_the_belong_guard_stops_stopping

if [ "$failures" -ne 0 ]; then
  echo "check611_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check611_test.sh: ok"
