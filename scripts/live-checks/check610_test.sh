#!/usr/bin/env bash
# Tests for scripts/live-checks/check610.sh, with fake `fugaro`, `gh`, `gcloud`
# and `git` on PATH: NOTHING here reaches a cloud, a project, a repository or a
# run, no agent runs a live command, and the fakes record every call they are
# given — with the directory it was made in — so a case can assert what the check
# did and what it did NOT do. The check opens ONE throwaway pull request and
# launches ONE review; both are writes to a real repository, which is why every
# refusal below is measured here rather than by running the check for real.
#
# The fakes are a small model of one installation and its sandbox: the fake `git`
# answers the checkout's origin, its status, its branch and the fetch, branch,
# commit and push the check makes in it; the fake `gh` answers the default branch,
# the pull request it opens and the number read back; the fake `fugaro` answers
# version, doctor, config, the published policy, the launch, the listing polled
# and the review artifact; and the fake `gcloud` serves the account, the job list
# and the credentials service's own lines.
#
# Every document a case bends is a file the case wrote: a doctor whose project is
# another installation or carries no base image; a config with no workflow, a
# cloned one or a second non-baked one, or no auth at all; a policy with no
# profiles block, with one, one that does not admit the repository, or no budget
# mode; a job list with no implicit review-only job, two of them,
# another hash on its stem, one without the service URL or with a secret, one on
# another image, or one with a coding project job, a profile's own review variant or
# the Bitbucket implicit job beside it; a launch on another job, as another account or
# with no run named; a row that had not finished, failed, or names another pull
# request; an artifact about another run, another pull request, another account or
# no verdict; and a log whose first served model call is another credential, of
# another run, or absent.
#
# Usage: bash scripts/live-checks/check610_test.sh
#        CHECK_TEST_ONLY=test_happy_path bash scripts/live-checks/check610_test.sh
#        CHECK_TEST_SHARD=0/3 bash scripts/live-checks/check610_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check610.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK610_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK610_TEST_HEAD:-1400}")" ;;
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
# expect_row: assert on the ONE row's own text. Every row is a sentence the
# register quotes, so a case reads the row it is about rather than the block.
expect_row() { # key needle what
  local row
  row=$(printf '%s\n' "$out" | grep -m1 "^FACT $1: " || true)
  if [ -z "$row" ]; then
    fail "$3: the block carries no FACT $1 row"
    return 0
  fi
  expect_contains "$row" "$2" "$3"
}

# One installation the cases read and bend: the names are what the rows quote.
install=chk610-test
gcp_project=chk610-test-proj
region=us-east5
repo=acme-owner/chk610-sandbox
origin=https://github.com/acme-owner/chk610-sandbox.git
base_image="us-east5-docker.pkg.dev/$gcp_project/fugaro/base@sha256:aa11bb22cc33dd44"
other_image="us-east5-docker.pkg.dev/$gcp_project/fugaro/other@sha256:dd44ee55ff66aa77"
# The job names are the real derived ones, never invented: an implicit review-only
# job is what infra.ImplicitReviewJobName derives from the triple (gcp.derive cuts
# the readable part to 36 characters before the twelve hex of the hash, so the github
# job's account segment reads `default-rev` and never `default-review`), a coding job
# of a project is what gcp.ProjectJobName derives, and a per-repository job is
# gcp.JobName's. TestLiveCheck610NamesTheImplicitReviewJobs pins the two implicit
# names to infra, so a naming change breaks that pin rather than leaving this suite
# passing against names no installation has.
job="fugaro-p-standard-github-default-rev-04644ca962ed"
bitbucket_job="fugaro-p-standard-bitbucket-default-491922f654ef"
coding_job="fugaro-p-standard-github-0ca96e64b636"
profile_review_job="fugaro-p-standard-github-work-review-1f24aece23df"
per_repo_job="fugaro-acme-owner-chk610-sandbox-chk-105651dae500"
base_branch=main
run_base=20261118-120000
run_id=$run_base-0610
run_slug=chk610-sandbox/$run_id
branch=chk610-$run_base
pr=7
head_sha=abc1234def5678
# credentialed_origin is the origin a token-based clone leaves behind: a GitHub URL
# whose userinfo carries a credential, split by a `/`, which is where a strip that
# cannot cross one would leave it in place. What it holds is not a credential and
# opens nothing: it is the word the case below asserts never reaches the output,
# so one that appears was printed by this check and by nothing else. What the
# strip leaves of it — `https://github.com/` — names no repository, which is the
# refusal whose text reaches both stderr and the block.
credentialed_origin='https://x-access-token:LEAK-CANARY/with-a-slash@github.com/'
credential_words='x-access-token LEAK-CANARY with-a-slash'

# --------------------------------------------------------------------- fakes
write_fake_git() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/git" <<'GIT_EOF'
#!/usr/bin/env bash
# A stand-in for `git`, for scripts/live-checks/check610_test.sh only: it answers
# the checkout's origin and status, the branch it is on, and the fetch, branch,
# commit, push and pull-back the check makes in that checkout. It touches no
# repository: what it "pushes" goes nowhere. Every call is logged with the
# directory it was made in, so a case can assert both what the check ran and that
# the checkout was left back on the branch it was found on.
set -euo pipefail
printf 'git %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/git.calls"
mode=${FAKE_MODE:-default}
while [ $# -gt 0 ]; do
  case "$1" in
  -C | -c) shift 2 ;;
  *) break ;;
  esac
done
case "${1:-}" in
remote)
  case "$mode" in
  git-no-origin)
    echo "error: No such remote 'origin'" >&2
    exit 2
    ;;
  esac
  printf '%s\n' "${FAKE_ORIGIN:-}"
  ;;
status) printf '%s' "${FAKE_DIRTY:-}" ;;
symbolic-ref)
  case "$mode" in
  git-nobranch) exit 1 ;;
  esac
  printf '%s\n' "${FAKE_BRANCH:-main}"
  ;;
rev-parse) printf 'abc1234\n' ;;
fetch)
  case "$mode" in
  git-fetch-fails)
    echo "fatal: could not fetch the branch" >&2
    exit 128
    ;;
  esac
  ;;
checkout)
  case "$mode" in
  git-branch-fails)
    echo "fatal: a branch named 'chk610' already exists" >&2
    exit 128
    ;;
  esac
  ;;
add) ;;
commit)
  case "$mode" in
  git-commit-fails)
    echo "fatal: nothing to commit" >&2
    exit 1
    ;;
  esac
  ;;
push)
  case "$mode" in
  git-push-fails)
    echo "fatal: the remote rejected the push" >&2
    exit 1
    ;;
  esac
  ;;
*)
  echo "git: unexpected ${1:-}" >&2
  exit 1
  ;;
esac
GIT_EOF
  chmod +x "$1/bin/git"
}

write_fake_gh() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gh" <<'GH_EOF'
#!/usr/bin/env bash
# A stand-in for `gh`, for scripts/live-checks/check610_test.sh only: the default
# branch of the repository, the throwaway pull request the check opens and the
# number it reads back. It opens nothing.
set -euo pipefail
printf 'gh %s\n' "$*" >>"$FAKE_LOG_DIR/gh.calls"
mode=${FAKE_MODE:-default}
case "${1:-} ${2:-}" in
"repo view")
  case "$mode" in
  gh-base-fails)
    echo "gh: could not resolve to a Repository" >&2
    exit 1
    ;;
  esac
  printf '%s\n' "${FAKE_BASE_BRANCH:-main}"
  ;;
"pr create")
  case "$mode" in
  gh-pr-fails)
    echo "gh: could not create pull request" >&2
    exit 1
    ;;
  esac
  printf 'https://github.com/%s/pull/%s\n' "${FAKE_REPO:-acme-owner/chk610-sandbox}" "${FAKE_PR:-7}"
  ;;
"pr view")
  case "$mode" in
  gh-pr-nonumber)
    printf 'not-a-number\n'
    exit 0
    ;;
  esac
  printf '%s\n' "${FAKE_PR:-7}"
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
# A stand-in for `gcloud`, for scripts/live-checks/check610_test.sh only. Three
# reads and no write: the account, the job list, and the credentials service's own
# lines. The lines are served as the fixture wrote them — the run's own id is
# substituted for @RID@ and the newest lines are cut to the --limit asked for, the
# way `--order=asc` answers — so the FILTERING by run is the reader's and a case
# can prove the reader did it.
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
case "${1:-} ${2:-}" in
"config get-value")
  printf '%s\n' "${FAKE_ACCOUNT:-}"
  ;;
"run jobs")
  case "$mode" in
  jobs-fails)
    echo "ERROR: (gcloud.run.jobs.list) PERMISSION_DENIED" >&2
    exit 1
    ;;
  jobs-garbage)
    printf 'this is not a document\n'
    exit 0
    ;;
  esac
  cat "$FAKE_JSON_DIR/${FAKE_JOBS_FILE:-jobs.json}"
  ;;
"logging read")
  case "$mode" in
  log-fails)
    echo "ERROR: (gcloud.logging.read) the log could not be read" >&2
    exit 1
    ;;
  esac
  limit=""
  for a in "$@"; do
    case "$a" in
    --limit=*) limit=${a#--limit=} ;;
    esac
  done
  sed "s|@RID@|${FAKE_RUN_ID:-}|g" "$FAKE_JSON_DIR/${FAKE_LOG_FILE:-log.json}" >"$FAKE_STATE_DIR/log.served"
  python3 - "$FAKE_STATE_DIR/log.served" "$limit" <<'PY'
import json, sys
path, limit = sys.argv[1], sys.argv[2]
doc = json.load(open(path))
if limit.isdigit():
    doc = doc[: int(limit)]
json.dump(doc, sys.stdout)
PY
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
# A stand-in for `fugaro`, for scripts/live-checks/check610_test.sh only. The
# reads answer the case's fixture, and the ONE write — `review --pr N` — records
# the run id it "launched" so the listing polled afterwards can hold that run's
# own row, and prints the launch document the case wrote. Nothing here reaches a
# cloud: no execution exists and no review runs.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
version) printf 'fugaro version 9.9.9 (test)\n' ;;
doctor)
  case "$mode" in
  doctor-hangs) sleep 300 ;;
  esac
  cat "$FAKE_JSON_DIR/${FAKE_DOCTOR_FILE:-doctor.json}"
  exit "${FAKE_DOCTOR_STATUS:-0}"
  ;;
config)
  case "$mode" in
  config-fails)
    echo "fugaro: the checkout's config could not be resolved" >&2
    exit 1
    ;;
  esac
  cat "$FAKE_JSON_DIR/${FAKE_CONFIG_FILE:-config.json}"
  ;;
project)
  case "$mode" in
  policy-fails)
    echo "fugaro: the published policy could not be read" >&2
    exit 1
    ;;
  esac
  cat "$FAKE_JSON_DIR/${FAKE_POLICY_FILE:-policy.json}"
  ;;
review)
  if [ "${2:-}" = show ]; then
    case "$mode" in
    show-fails)
      echo "fugaro: no review was recorded for that run" >&2
      exit 1
      ;;
    esac
    sed "s|@RID@|${FAKE_RUN_ID:-}|g" "$FAKE_JSON_DIR/${FAKE_ARTIFACT_FILE:-artifact.json}"
    exit 0
  fi
  case "$mode" in
  review-hangs) sleep 300 ;;
  review-fails)
    echo "fugaro: this repository resolves a review-only variant of a shared project job, and this repository has none" >&2
    exit 1
    ;;
  esac
  printf '%s' "${FAKE_RUN_ID:-}" >"$FAKE_STATE_DIR/run-id"
  sed "s|@RID@|${FAKE_RUN_ID:-}|g" "$FAKE_JSON_DIR/${FAKE_LAUNCH_FILE:-launch.json}"
  ;;
runs)
  case "$mode" in
  listing-fails)
    echo "fugaro: the listing could not be read" >&2
    exit 1
    ;;
  esac
  rid=$(cat "$FAKE_STATE_DIR/run-id" 2>/dev/null || true)
  sed "s|@RID@|${rid:-}|g" "$FAKE_JSON_DIR/${FAKE_ROW_FILE:-row.json}"
  ;;
*)
  echo "fugaro: unexpected ${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}
# ------------------------------------------------------------------ fixtures
# Every document a case bends is written here, from one model of the installation:
# a doctor naming the project and its base image, a config resolving agent.auth
# and one baked workflow, a profile-less policy admitting the repository to the
# project and to the default account, a job list whose only project job is the
# implicit review-only job, a launch document, a listing with the run's own row,
# the review artifact of that run, and the credentials service's lines for it.
write_json() { # dir
  mkdir -p "$1/json"
  python3 - "$1/json" "$base_image" "$other_image" "$run_slug" "$head_sha" <<'PY'
import copy, json, os, sys

out_dir, base_image, other_image, run_slug, head_sha = sys.argv[1:6]
implicit = "fugaro-p-standard-github-default-rev-04644ca962ed"
implicit_stem = "fugaro-p-standard-github-default-rev"
bitbucket_implicit = "fugaro-p-standard-bitbucket-default-491922f654ef"
coding = "fugaro-p-standard-github-0ca96e64b636"
profile_review = "fugaro-p-standard-github-work-review-1f24aece23df"
per_repo = "fugaro-acme-owner-chk610-sandbox-chk-105651dae500"


def out(name, obj):
    with open(os.path.join(out_dir, name), "w") as fh:
        json.dump(obj, fh, indent=1)
        fh.write("\n")


def clone(obj):
    return copy.deepcopy(obj)


def configure(*pairs):
    return {"values": [{"path": p, "value": v} for p, v in pairs]}


def envs(*names):
    return [{"name": n, "value": "placeholder"} for n in names]


def job(name, image, env, volumes=None):
    """One `gcloud run jobs list` entry, in the shape gcloud answers with."""
    return {"metadata": {"name": name},
            "spec": {"template": {"spec": {"template": {"spec": {
                "containers": [{"image": image, "env": env}],
                "volumes": volumes or []}}}}}}


def line(run, timestamp, credential, purpose="forward", status="ok", tier="standard"):
    return {"timestamp": timestamp,
            "jsonPayload": {"run": run, "msg": "credsvc: audit", "status": status,
                            "credential": credential, "tier": tier, "purpose": purpose,
                            "job": implicit, "caller": implicit}}


def row(status="succeeded", terminal=True, outcome="reviewed", **extra):
    r = {"run_id": "@RID@", "repo": "acme-owner/chk610-sandbox", "workflow": "chk",
         "recipe": "review", "status": status, "terminal": terminal, "outcome": outcome,
         "pushed": False, "pr": 7,
         "pr_url": "https://github.com/acme-owner/chk610-sandbox/pull/7",
         "execution": "projects/chk610-test-proj/locations/us-east5/jobs/%s/executions/chk610-run" % implicit}
    r.update(extra)
    return [r]


# ------------------------------------------------------------------- doctor
doctor = {"project": {"name": "chk610-test", "gcp_project": "chk610-test-proj",
                      "region": "us-east5",
                      "base_images": {"base": base_image}}}
out("doctor.json", doctor)
d = clone(doctor)
d["project"]["base_images"] = {}
out("doctor-nobase.json", d)
d = clone(doctor)
d["project"] = {}
out("doctor-noproject.json", d)
d = clone(doctor)
d["project"]["name"] = "belong"
out("doctor-belong.json", d)
# A doctor whose document this check cannot read as an installation at all: it
# answers no project block, and the row says which of the two it read.
out("doctor-no-document.json", [])

# ------------------------------------------------------------------- config
config = configure(
                   ("agent.auth", "vertex"),
                   ("agent.model", "claude-sonnet-5"),
                   ("workflows.chk.checkout", "baked"),
                   ("timeouts.total", "3h"))
out("config.json", config)
out("config-no-workflow.json", configure(("agent.auth", "vertex")))
out("config-clone.json", configure(("agent.auth", "vertex"),
                                   ("workflows.chk.checkout", "clone")))
out("config-nonbaked-two.json", configure(("agent.auth", "vertex"),
                                          ("workflows.chk.checkout", "baked"),
                                          ("workflows.other.checkout", "shared")))
out("config-no-auth.json", configure(("workflows.chk.checkout", "baked")))
out("config-cascade.json", configure(("agent.auth", "cascade"),
                                     ("workflows.chk.checkout", "baked")))

# ------------------------------------------------------------------- policy
policy = {"policy": {"allow_repos": ["github/acme-owner/chk610-sandbox"],
                     "accounts": {"default": {"allow_repos": ["github/acme-owner/chk610-sandbox"]}},
                     "budget": {"mode": "observe"}}}
out("policy.json", policy)
p = clone(policy)
p["policy"]["profiles"] = {"shared": {"provider": "github",
                                      "allow_repos": ["github/acme-owner/chk610-sandbox"]}}
out("policy-profiles.json", p)
p = clone(policy)
del p["policy"]["budget"]
out("policy-no-mode.json", p)
p = clone(policy)
p["policy"]["allow_repos"] = []
out("policy-not-admitted.json", p)
p = clone(policy)
p["policy"]["accounts"]["default"]["allow_repos"] = []
out("policy-default-not-admitted.json", p)

# --------------------------------------------------------------------- jobs
review_job = job(implicit, base_image, envs("FUGARO_CREDENTIALS_SERVICE_URL", "FUGARO_GITHUB_APP_ID"))
coding_job = job(coding, base_image, envs("FUGARO_CREDENTIALS_SERVICE_URL", "FUGARO_SECRET_ENVS"))
out("jobs.json", [job(per_repo, other_image, envs("FUGARO_SECRET_ENVS")), review_job])
out("jobs-coding.json", [job(per_repo, other_image, envs("FUGARO_SECRET_ENVS")), review_job, coding_job])
out("jobs-no-implicit.json", [job(per_repo, other_image, envs("FUGARO_SECRET_ENVS"))])
out("jobs-two-implicit.json", [review_job,
                               job("%s-111111111111" % implicit_stem, base_image,
                                   envs("FUGARO_CREDENTIALS_SERVICE_URL"))])
# Another hash on the same stem is not the job infra derives: the check reads a job
# of the derivation, never one that merely starts like it.
out("jobs-other-hash.json", [job("%s-abcdefabcdef" % implicit_stem, base_image,
                                 envs("FUGARO_CREDENTIALS_SERVICE_URL"))])
# The credentials service on and a Bitbucket repository recorded deploys the
# Bitbucket implicit review-only job beside the github one: two derivations of one
# row, both review variants, and U4 counts neither as a job of a profile.
out("jobs-bitbucket-implicit.json", [job(per_repo, other_image, envs("FUGARO_SECRET_ENVS")),
                                     review_job,
                                     job(bitbucket_implicit, base_image,
                                         envs("FUGARO_CREDENTIALS_SERVICE_URL"))])
# A profile's own review variant: a project job that is not one of the two implicit
# names, however much of the word `review` its name carries.
out("jobs-profile-review.json", [job(per_repo, other_image, envs("FUGARO_SECRET_ENVS")),
                                 review_job,
                                 job(profile_review, base_image,
                                     envs("FUGARO_CREDENTIALS_SERVICE_URL"))])
out("jobs-no-url.json", [job(implicit, base_image, envs("FUGARO_GITHUB_APP_ID"))])
out("jobs-secrets.json", [job(implicit, base_image,
                              envs("FUGARO_CREDENTIALS_SERVICE_URL", "FUGARO_SECRET_ENVS"))])
out("jobs-secretvolume.json", [job(implicit, base_image,
                                   envs("FUGARO_CREDENTIALS_SERVICE_URL"),
                                   volumes=[{"name": "creds", "secret": {"secret": "claude"}}])])
out("jobs-other-image.json", [job(implicit, other_image, envs("FUGARO_CREDENTIALS_SERVICE_URL"))])

# ------------------------------------------------------------------- launch
launch = {"run": run_slug, "run_id": "@RID@", "repo": "acme-owner/chk610-sandbox",
          "status": "launched", "job": implicit, "account": "default",
          "execution": "projects/chk610-test-proj/locations/us-east5/jobs/%s/executions/chk610-run" % implicit,
          "pr": 7}
out("launch.json", launch)
d = clone(launch)
d["job"] = coding
out("launch-other-job.json", d)
d = clone(launch)
d["account"] = "work"
out("launch-named-account.json", d)
d = clone(launch)
d["job"] = None
out("launch-no-job.json", d)
d = clone(launch)
d["run"] = None
d["run_id"] = None
out("launch-no-run.json", d)

# ---------------------------------------------------------------------- row
out("row.json", row())
out("row-inflight.json", row(status="running", terminal=False, outcome=None))
out("row-failed.json", row(status="failed", outcome="draft",
                           halt={"detail": "the review stopped at its first model call"}))
out("row-other-pr.json", row(pr=9, pr_url="https://github.com/acme-owner/chk610-sandbox/pull/9"))
out("row-other-run.json", [dict(row()[0], run_id="20261118-115959-0000")])

# ----------------------------------------------------------------- artifact
artifact = {"schema_version": "1", "review_run": "@RID@", "repository": run_slug,
            "pr": 7, "head_sha": head_sha, "base": "main", "verdict": "comment",
            "reviewer": {"account": "default", "model": "claude-sonnet-5"},
            "issues": ["one finding"]}
out("artifact.json", artifact)
d = clone(artifact)
d["review_run"] = "20261118-115959-0000"
out("artifact-other-run.json", d)
d = clone(artifact)
d["pr"] = 9
out("artifact-other-pr.json", d)
d = clone(artifact)
d["reviewer"]["account"] = "work"
out("artifact-other-account.json", d)
d = clone(artifact)
d["verdict"] = None
out("artifact-no-verdict.json", d)
out("artifact-no-artifact.json", [])

# ---------------------------------------------------------------------- log
out("log.json", [line("@RID@", "2026-11-18T12:00:10Z", "default"),
                 line("@RID@", "2026-11-18T12:00:40Z", "github-app", purpose="git")])
out("log-no-forward.json", [line("@RID@", "2026-11-18T12:00:40Z", "github-app", purpose="git")])
out("log-other-credential.json", [line("@RID@", "2026-11-18T12:00:10Z", "work"),
                                  line("@RID@", "2026-11-18T12:00:40Z", "default")])
# Two served model calls, and the earliest by ITS OWN TIMESTAMP is the default
# account's: a read that took the document's first forward line would take the
# later switch's credential instead.
out("log-two-forwards.json", [line("@RID@", "2026-11-18T12:00:30Z", "work"),
                              line("@RID@", "2026-11-18T12:00:10Z", "default")])
out("log-other-run.json", [line("20261118-115959-0000", "2026-11-18T12:00:10Z", "work")])
PY
}
write_checkout() { # dir
  mkdir -p "$1"
  cat >"$1/fugaro.yaml" <<'YAML_EOF'
project: chk610-test
workflows:
  chk:
    checkout: baked
YAML_EOF
}

# The one mutation of the checkout this check reads literally: a repository whose
# own fugaro.yaml carries the key, which is never the sandbox RJ9 names.
write_checkout_with_profiles() { # dir
  write_checkout "$1"
  cat >>"$1/fugaro.yaml" <<'YAML_EOF'
profiles:
  shared:
    provider: github
YAML_EOF
}

prepare() { # dir
  mkdir -p "$1/home" "$1/state" "$1/logs" "$1/tmp"
  write_fake_git "$1"
  write_fake_gh "$1"
  write_fake_gcloud "$1"
  write_fake_fugaro "$1"
  write_json "$1"
  # Every document a case bends has to still be a document: a template this file
  # broke would otherwise read as an installation that wrote nothing, which is a
  # confusing way to learn that the fixture is wrong.
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
  : >"$dir/logs/gh.calls"
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
    FAKE_ORIGIN="${FAKE_ORIGIN:-$origin}" FAKE_DIRTY="${FAKE_DIRTY:-}" \
    FAKE_BRANCH="${FAKE_BRANCH:-$base_branch}" FAKE_ACCOUNT="${FAKE_ACCOUNT:-}" \
    FAKE_REPO="${FAKE_REPO:-$repo}" FAKE_PR="${FAKE_PR:-$pr}" \
    FAKE_BASE_BRANCH="${FAKE_BASE_BRANCH:-$base_branch}" FAKE_RUN_ID="${FAKE_RUN_ID:-$run_id}" \
    FAKE_DOCTOR_STATUS="${FAKE_DOCTOR_STATUS:-0}" \
    FAKE_DOCTOR_FILE="${FAKE_DOCTOR_FILE:-doctor.json}" \
    FAKE_CONFIG_FILE="${FAKE_CONFIG_FILE:-config.json}" \
    FAKE_POLICY_FILE="${FAKE_POLICY_FILE:-policy.json}" \
    FAKE_JOBS_FILE="${FAKE_JOBS_FILE:-jobs.json}" FAKE_LOG_FILE="${FAKE_LOG_FILE:-log.json}" \
    FAKE_ROW_FILE="${FAKE_ROW_FILE:-row.json}" FAKE_LAUNCH_FILE="${FAKE_LAUNCH_FILE:-launch.json}" \
    FAKE_ARTIFACT_FILE="${FAKE_ARTIFACT_FILE:-artifact.json}" \
    CHECK610_SANDBOX="$dir/sandbox" \
    CHECK610_RUN_ID="$run_base" \
    CHECK610_RUN_SECONDS="${CHECK610_RUN_SECONDS:-20}" \
    CHECK610_POLL_SECONDS="${CHECK610_POLL_SECONDS:-1}" \
    CHECK610_LOG_WAIT_SECONDS="${CHECK610_LOG_WAIT_SECONDS:-2}" \
    CHECK610_LOG_POLL_SECONDS="${CHECK610_LOG_POLL_SECONDS:-1}" \
    CHECK610_DOCTOR_TIMEOUT_SECONDS="${CHECK610_DOCTOR_TIMEOUT_SECONDS:-20}" \
    CHECK610_CLI_TIMEOUT_SECONDS="${CHECK610_CLI_TIMEOUT_SECONDS:-20}" \
    CHECK610_KEEP_SCRATCH="${CHECK610_KEEP_SCRATCH:-}" \
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

# This check WRITES exactly twice — one branch on the sandbox repository and one
# review run — and every other command it gives fugaro or gcloud is a read. This
# is the assertion that would catch a diff that made it write anything else.
assert_reads_only() { # dir what
  local fugaro gcloud
  fugaro=$(cat "$1/logs/fugaro.calls")
  gcloud=$(cat "$1/logs/gcloud.calls")
  local word
  for word in "project policy publish" "project repos add" "project repos rm" "update" "setup" "runs rm" "creds set" "creds rm" "image build" "runs show"; do
    expect_absent "$fugaro" "$word" "$2: fugaro '$word' is never run"
  done
  for word in "jobs create" "jobs update" "jobs delete" "jobs deploy" "run deploy" "services deploy" "logging write" "secrets" "projects delete"; do
    expect_absent "$gcloud" "$word" "$2: gcloud '$word' is never run"
  done
}

# One launch and one only: `fugaro review --pr N` is the single billable command
# this check gives, and a second one would be a second review nobody asked for.
# It never runs `fugaro run` either: a review is not a coding run, and the plan's
# option C is about the launcher choosing the review job itself.
assert_one_launch() { # dir what
  local calls n
  calls=$(cat "$1/logs/fugaro.calls")
  n=$(printf '%s\n' "$calls" | grep -c 'review --pr ' || true)
  [ "${n:-0}" -eq 1 ] || fail "$2: expected exactly one 'review --pr' call, found ${n:-0}"
  expect_absent "$calls" "fugaro run " "$2: no coding run is launched"
}

# Every refusal and the dry run: nothing was launched at all, and no pull request
# was opened — the check stops before the two writes it makes.
assert_no_launch() { # dir what
  assert_no_review "$1" "$2"
  expect_absent "$(cat "$1/logs/gh.calls")" "pr create" "$2: no pull request is opened"
}

# The pull request was attempted, so one `gh pr create` is expected — but nothing
# may be launched on its back.
assert_no_review() { # dir what
  local calls
  calls=$(cat "$1/logs/fugaro.calls")
  expect_absent "$calls" "review --pr " "$2: no review is launched"
  expect_absent "$calls" "fugaro run " "$2: no coding run is launched"
}

# The checkout was found clean and has to be left as it was found: the check's own
# branch and file must be gone from the working tree, which is the pull-back's
# `checkout --force` being the last thing it did in it.
assert_checkout_left_clean() { # dir what
  local calls last
  calls=$(cat "$1/logs/git.calls")
  last=$(printf '%s\n' "$calls" | tail -n 1)
  expect_contains "$last" "checkout --quiet --force $base_branch" "$2: the checkout is back on the branch it was found on"
  expect_absent "$calls" "push origin --delete" "$2: no branch of the repository is deleted"
  expect_absent "$calls" "reset --hard" "$2: nothing in the checkout is reset"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check610.sh --sandbox <a sandbox GitHub repository checkout>" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  assert_no_launch "$dir" "--help"
  # The header is what a person reads before running it: it has to say, where they
  # will see it, that the review is billable, that the run is theirs, and that the
  # pull request and the run it leaves behind are not removed.
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  expect_contains "$out" "gcloud logging read" "--help: how the service's lines are read back"
  expect_contains "$out" "pull request" "--help: what it creates and does not remove"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK610_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 610 RESULT" "dry run: the block"
  expect_row dry-run "PASS" "dry run: the row that says nothing was run"
  expect_row sandbox "INFO — planned" "dry run: the rows are planned, not measured"
  expect_row u4 "INFO — planned" "dry run: all four UNVERIFIED rows are planned"
  expect_contains "$out" "CHECK 610 RESULT END (exit 0)" "dry run: the end line"
  expect_absent "$out" "FAIL" "dry run: nothing is measured, so nothing fails"
  # A dry run touches nothing at all: not the cloud, not the repository, not even
  # the checkout's own config.
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "dry run: no read is made"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "run jobs" "dry run: the cloud is not read"
  assert_no_launch "$dir" "dry run"
  assert_scratch_removed "$dir" "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the happy path"
  expect_row sandbox "PASS — $dir/sandbox is a Fugaro repository's checkout of the GITHUB repository $repo with a clean working tree, and it belongs to the $install installation ($gcp_project, region $region)" "the happy path: the sandbox row"
  expect_row config "PASS — $repo's own fugaro.yaml carries no \`profiles:\` key and every one of its 1 workflow(s) resolves checkout 'baked' (chk=baked)" "the happy path: the config row"
  expect_row config "resolves 'vertex' (U3's first reading)" "the happy path: the auth it read"
  expect_row policy "PASS — $install's published policy carries NO profiles block and admits $repo to the project (its budget mode is 'observe'), and its accounts block names 1 account(s) (default), the one(s) admitting this repository reading 'default'" "the happy path: the policy row"
  expect_row job "PASS — the installation's implicit review-only job '$job' is deployed and carries FUGARO_CREDENTIALS_SERVICE_URL" "the happy path: the job row"
  expect_row pull "PASS — a throwaway pull request of $repo was opened by this check: #$pr, branch $branch (base $base_branch, one file, CHK610.md), and the checkout $dir/sandbox is back on $base_branch" "the happy path: the pull request row"
  expect_row launch "PASS — fugaro review --pr $pr launched ONE review run of $repo ($run_id, status 'launched') on '$job'" "the happy path: the launch row"
  expect_row run "PASS — the review run ($run_id) of $repo ended 'succeeded' with outcome 'reviewed' on the pull request this check opened (#$pr, recipe 'review')" "the happy path: the run row"
  expect_row review "PASS — the review recorded its artifact for the run $run_id and the pull request #$pr" "the happy path: the review row"
  expect_row review "the head commit $head_sha and the verdict 'comment'" "the happy path: what the artifact states"
  expect_row u1 "PASS — U1: $repo's workflow is BAKED" "the happy path: U1"
  expect_row u1 "whose container image is the installation's own base image '$base_image'" "the happy path: U1 compares the two images"
  expect_row u2 "PASS — U2: $install's published policy carries no profiles block and its accounts block admits the DEFAULT account" "the happy path: U2"
  expect_row u3 "PASS — U3: the checkout resolves agent.auth 'vertex' and the review's model calls were served by the installation's credentials service as the credential 'default'" "the happy path: U3"
  expect_row u4 "PASS — U4: $install ($gcp_project, region $region) deploys 1 project job(s) and EVERY one of them is an implicit review-only job — $job — with no job of a profile at all" "the happy path: U4"
  expect_row restore "PASS — pull request #$pr of $repo was opened by this check" "the happy path: the restore row"
  expect_row scratch-removed "PASS" "the happy path: the scratch row"
  expect_contains "$out" "CHECK 610 RESULT END (exit 0)" "the happy path: the end line"
  expect_absent "$out" "FAIL" "the happy path: no row fails"
  assert_scratch_removed "$dir" "the happy path"
  assert_reads_only "$dir" "the happy path"
  assert_one_launch "$dir" "the happy path"
  assert_checkout_left_clean "$dir" "the happy path"
  local fugaro gcloud gitcalls ghcalls
  fugaro=$(cat "$dir/logs/fugaro.calls")
  gcloud=$(cat "$dir/logs/gcloud.calls")
  gitcalls=$(cat "$dir/logs/git.calls")
  ghcalls=$(cat "$dir/logs/gh.calls")
  # The reads are made in the checkout, and the launch is the plan's one command.
  expect_contains "$fugaro" "doctor --json (in $dir/sandbox)" "the happy path: the reads are made in the checkout"
  expect_contains "$fugaro" "review --pr $pr --project $install --json" "the happy path: the launch"
  expect_contains "$fugaro" "runs ls --repo $repo --pr $pr --project $install --no-cache --json" "the happy path: the listing it polled"
  expect_contains "$fugaro" "review show $run_slug --project $install --json" "the happy path: the artifact it read back"
  expect_contains "$gcloud" "run jobs list --project=$gcp_project --region=$region --format=json" "the happy path: the job list"
  expect_contains "$gcloud" "jsonPayload.run=\"$run_id\"" "the happy path: the service's lines are read for the run's own id"
  expect_contains "$gcloud" "--limit=200 --order=asc" "the happy path: the lines are read oldest-first"
  expect_contains "$gcloud" "--freshness=2h" "the happy path: the window is the knob when the run did not outlast it"
  # One file on a branch cut from the base branch, pushed, and the checkout back.
  expect_contains "$gitcalls" "fetch --quiet origin $base_branch" "the happy path: the base branch is fetched"
  expect_contains "$gitcalls" "checkout --quiet -B $branch FETCH_HEAD" "the happy path: the throwaway branch is cut"
  expect_contains "$gitcalls" "push --quiet origin HEAD:refs/heads/$branch" "the happy path: the branch is pushed"
  expect_contains "$ghcalls" "pr create --repo $repo --base $base_branch --head $branch --title Check 610: a throwaway pull request to review" "the happy path: the pull request it opened"
  expect_contains "$ghcalls" "pr view $branch --repo $repo --json number -q .number" "the happy path: the number it read back"
}
test_an_unknown_argument_is_refused() {
  local dir=$work/unknown
  prepare "$dir"
  run_check "$dir" -- --review
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --review" "an unknown argument: the reason"
  assert_no_launch "$dir" "an unknown argument"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/no-sandbox
  prepare "$dir"
  run_check "$dir" CHECK610_SANDBOX=
  expect_status "$status" 2 "no --sandbox"
  expect_contains "$err" "Check 610 needs --sandbox" "no --sandbox: the reason"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "no --sandbox: nothing is read"
  assert_no_launch "$dir" "no --sandbox"
}

test_a_path_that_is_not_a_checkout_is_refused() {
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
  # A PATH that holds the fakes for everything but `gh`, and the real one nowhere:
  # this machine may have a real gh, git or fugaro installed, and a case that only
  # deleted a fake would drive it.
  mkdir -p "$dir/empty"
  local tool
  for tool in bash mktemp python3 sed grep tr cut head tail cat rm chmod mkdir date sleep kill ls cp awk; do
    ln -s "$(command -v "$tool")" "$dir/empty/$tool" 2>/dev/null || true
  done
  ln -s "$dir/bin/git" "$dir/empty/git"
  ln -s "$dir/bin/gcloud" "$dir/empty/gcloud"
  ln -s "$dir/bin/fugaro" "$dir/empty/fugaro"
  # run_check makes the call logs; this case calls the script itself, so the log
  # the assertion below reads is made here.
  mkdir -p "$dir/logs"
  : >"$dir/logs/git.calls"
  status=0
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/empty" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_STATE_DIR="$dir/state" \
    CHECK610_SANDBOX="$dir/sandbox" \
    "$dir/empty/bash" "$script" >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  expect_status "$status" 2 "a missing tool"
  expect_contains "$err" "Check 610 needs 'gh' and it is not on PATH" "a missing tool: the reason"
  expect_absent "$(cat "$dir/logs/git.calls")" "git " "a missing tool: nothing is read"
}

test_xtrace_is_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- --xtrace
  expect_status "$status" 1 "a traced shell"
  expect_row xtrace "FAIL" "a traced shell: the row"
  expect_contains "$out" "this shell is tracing" "a traced shell: the reason"
  assert_no_launch "$dir" "a traced shell"
  assert_scratch_removed "$dir" "a traced shell"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_row agent-session "FAIL" "a coding agent's session: the row"
  expect_contains "$out" "is set, so this is a coding agent's session" "a coding agent's session: the marker is named"
  assert_no_launch "$dir" "a coding agent's session"
  assert_scratch_removed "$dir" "a coding agent's session"
}

test_belong_is_refused_by_name() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" CHECK610_SANDBOX="$dir/belong-checkout"
  expect_status "$status" 1 "a sandbox path naming belong"
  expect_contains "$err" "a value this check was given or read names belong" "a sandbox path naming belong: the reason"
  expect_row belong "FAIL" "a sandbox path naming belong: the row"
  expect_row sandbox "FAIL — not reached" "a sandbox path naming belong: nothing is read"
  assert_no_launch "$dir" "a sandbox path naming belong"
}

test_a_value_that_names_belong_is_refused_by_the_repository_guard() {
  local dir=$work/installation-guard
  prepare "$dir"
  run_check "$dir" FAKE_DOCTOR_FILE=doctor-belong.json
  expect_status "$status" 1 "an installation named belong"
  expect_contains "$err" "belongs to belong ($gcp_project, region $region) and $repo" "an installation named belong: the refusal says what it read"
  expect_row sandbox "FAIL" "an installation named belong: the row"
  assert_no_launch "$dir" "an installation named belong"
  assert_scratch_removed "$dir" "an installation named belong"
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

test_an_origin_that_names_no_repository_is_refused() {
  local dir=$work/odd-origin
  prepare "$dir"
  run_check "$dir" "FAKE_ORIGIN=https://github.com/"
  expect_status "$status" 1 "an origin naming no repository"
  expect_contains "$err" "is 'https://github.com/', which names no repository" "an origin naming no repository: the refusal says what it read"
  assert_no_launch "$dir" "an origin naming no repository"
}

test_an_origin_that_is_not_github_is_refused() {
  local dir=$work/not-github
  prepare "$dir"
  run_check "$dir" "FAKE_ORIGIN=https://bitbucket.org/acme-owner/chk610-sandbox.git"
  expect_status "$status" 1 "a Bitbucket checkout"
  expect_contains "$err" "is not a GitHub repository (its host is 'bitbucket.org')" "a Bitbucket checkout: the refusal says which host it read"
  expect_contains "$err" "the Bitbucket half is Check 611" "a Bitbucket checkout: the refusal names the other half"
  expect_row aborted "FAIL" "a Bitbucket checkout: the reason is in the block"
  assert_no_launch "$dir" "a Bitbucket checkout"
}

test_a_credential_in_the_origin_is_never_printed() {
  local dir=$work/credentialled-origin
  prepare "$dir"
  # A URL the strip cannot leave whole: its userinfo carries a `/`, which is where
  # a rule that could not cross one would leave the credential in place. What it
  # still names is `github.com/`, which is the refusal this case measures.
  run_check "$dir" "FAKE_ORIGIN=$credentialed_origin"
  expect_status "$status" 1 "a credentialled origin"
  local word
  for word in $credential_words; do
    expect_absent "$err" "$word" "a credentialled origin: '$word' on stderr"
    expect_absent "$out" "$word" "a credentialled origin: '$word' in the block"
  done
  # The URL is kept, minus its userinfo: the refusal still says what it refused.
  expect_contains "$err" "is 'https://github.com/', which names no repository" "a credentialled origin: the URL is kept without its credential"
  expect_row aborted "FAIL" "a credentialled origin: the reason is in the block"
  assert_no_launch "$dir" "a credentialled origin"
}

test_a_dirty_checkout_is_refused() {
  local dir=$work/dirty
  prepare "$dir"
  run_check "$dir" "FAKE_DIRTY= M CHK610.md"
  expect_status "$status" 1 "a dirty checkout"
  expect_contains "$err" "has uncommitted changes, and this check commits one file to it" "a dirty checkout: the reason"
  expect_contains "$err" "it will not touch them" "a dirty checkout: what it promises"
  assert_no_launch "$dir" "a dirty checkout"
}

test_a_doctor_whose_checks_are_not_all_ok_is_an_answer() {
  local dir=$work/doctor-status-1
  prepare "$dir"
  run_check "$dir" FAKE_DOCTOR_STATUS=1
  expect_status "$status" 0 "a doctor that exited 1"
  expect_row sandbox "PASS" "a doctor that exited 1: the sandbox row is measured"
  assert_one_launch "$dir" "a doctor that exited 1"
}

test_a_doctor_that_never_answers_is_not_measured() {
  local dir=$work/doctor-hangs
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-hangs CHECK610_DOCTOR_TIMEOUT_SECONDS=1
  expect_status "$status" 2 "a doctor that never answered"
  expect_row sandbox "FAIL — fugaro doctor --json did not answer" "a doctor that never answered: the sandbox row"
  expect_row config "FAIL — there is no checkout to read a workflow of" "a doctor that never answered: the config row"
  expect_row u2 "FAIL — the checkout could not be read far enough" "a doctor that never answered: U2"
  assert_no_launch "$dir" "a doctor that never answered"
  assert_scratch_removed "$dir" "a doctor that never answered"
}

test_a_doctor_that_carries_no_project_is_not_measured() {
  local dir=$work/doctor-no-project
  prepare "$dir"
  run_check "$dir" FAKE_DOCTOR_FILE=doctor-noproject.json
  expect_status "$status" 2 "a doctor with no project block"
  expect_row sandbox "FAIL — fugaro doctor --json answered without a project block" "a doctor with no project block: the row"
  assert_no_launch "$dir" "a doctor with no project block"
}

test_a_doctor_that_is_not_a_document_is_not_measured() {
  local dir=$work/doctor-no-document
  prepare "$dir"
  run_check "$dir" FAKE_DOCTOR_FILE=doctor-no-document.json
  expect_status "$status" 2 "a doctor that is not a document"
  expect_row sandbox "FAIL — fugaro doctor --json answered without a project block" "a doctor that is not a document: the row says what it could not read"
  assert_no_launch "$dir" "a doctor that is not a document"
}

test_a_checkout_whose_fugaro_yaml_carries_profiles_is_not_measured() {
  local dir=$work/own-profiles
  prepare "$dir"
  write_checkout_with_profiles "$dir/sandbox"
  run_check "$dir"
  expect_status "$status" 2 "a checkout whose own file carries profiles"
  expect_row config "FAIL — the checkout's own fugaro.yaml carries a \`profiles:\` key" "a checkout whose own file carries profiles: the row"
  expect_row pull "FAIL — the checkout is not the baked, profile-less repository this check is for; the config row says why, so no pull request is opened" "a checkout whose own file carries profiles: nothing is launched on it"
  assert_no_launch "$dir" "a checkout whose own file carries profiles"
}

test_a_checkout_with_no_workflow_is_not_measured() {
  local dir=$work/no-workflow
  prepare "$dir"
  run_check "$dir" FAKE_CONFIG_FILE=config-no-workflow.json
  expect_status "$status" 2 "a checkout with no workflow"
  expect_row config "FAIL — the checkout's resolved config carries no workflow at all" "a checkout with no workflow: the row"
  assert_no_launch "$dir" "a checkout with no workflow"
}

test_a_workflow_that_is_not_baked_is_not_measured() {
  local dir=$work/cloned-workflow
  prepare "$dir"
  run_check "$dir" FAKE_CONFIG_FILE=config-clone.json
  expect_status "$status" 2 "a cloned workflow"
  expect_row config "FAIL — the checkout resolves 1 workflow(s) and chk is not baked (chk=clone)" "a cloned workflow: the row names the workflow and its kind"
  expect_contains "$out" "which is a different review path from the one D2388 and U1 are about" "a cloned workflow: why it is not measured"
  assert_no_launch "$dir" "a cloned workflow"
}

test_a_second_workflow_that_is_not_baked_is_not_measured() {
  local dir=$work/second-workflow
  prepare "$dir"
  run_check "$dir" FAKE_CONFIG_FILE=config-nonbaked-two.json
  expect_status "$status" 2 "a second, non-baked workflow"
  expect_row config "FAIL — the checkout resolves 2 workflow(s) and other is not baked (chk=baked,other=shared)" "a second, non-baked workflow: the row names every workflow"
  assert_no_launch "$dir" "a second, non-baked workflow"
}

test_a_config_that_could_not_be_read_is_not_measured() {
  local dir=$work/config-refused
  prepare "$dir"
  run_check "$dir" FAKE_MODE=config-fails
  expect_status "$status" 2 "a config that could not be read"
  expect_row config "FAIL — fugaro config show --json did not answer in $dir/sandbox (exit 1)" "a config that could not be read: the row says what answered"
  expect_contains "$out" "a config nobody read is not a baked workflow" "a config that could not be read: why it is not measured"
  assert_no_launch "$dir" "a config that could not be read"
}

test_a_policy_that_could_not_be_read_is_not_measured() {
  local dir=$work/policy-refused
  prepare "$dir"
  run_check "$dir" FAKE_MODE=policy-fails
  expect_status "$status" 2 "a policy that could not be read"
  expect_row policy "FAIL — fugaro project policy show --project $install --json did not answer (exit 1)" "a policy that could not be read: the row says what answered"
  expect_row u2 "FAIL — the project's own policy is not one this check can measure a review in" "a policy that could not be read: U2 is not measured"
  assert_no_launch "$dir" "a policy that could not be read"
}

test_a_repository_the_project_does_not_admit_is_not_measured() {
  local dir=$work/not-admitted
  prepare "$dir"
  run_check "$dir" FAKE_POLICY_FILE=policy-not-admitted.json
  expect_status "$status" 2 "a repository the project does not admit"
  expect_row policy "FAIL — the published policy of $install does not admit $repo to the PROJECT" "a repository the project does not admit: the row"
  expect_contains "$out" "add it with 'fugaro project repos add <the repository's canonical name>'" "a repository the project does not admit: the fix is named"
  assert_no_launch "$dir" "a repository the project does not admit"
}

test_a_policy_with_profiles_is_not_measured() {
  local dir=$work/policy-profiles
  prepare "$dir"
  run_check "$dir" FAKE_POLICY_FILE=policy-profiles.json
  expect_status "$status" 2 "a project that publishes profiles"
  expect_row policy "FAIL — $install's published policy carries a profiles block (1: shared)" "a project that publishes profiles: the row names the profile"
  expect_contains "$out" "so this project is not the profile-less one the plan's RJ9 names" "a project that publishes profiles: why it is not measured"
  assert_no_launch "$dir" "a project that publishes profiles"
}

test_a_policy_with_no_budget_mode_is_not_measured() {
  local dir=$work/no-mode
  prepare "$dir"
  run_check "$dir" FAKE_POLICY_FILE=policy-no-mode.json
  expect_status "$status" 2 "a policy with no budget mode"
  expect_row policy "FAIL — $repo is admitted with no profiles block and the policy document carries no budget mode (it reads '-')" "a policy with no budget mode: the row says what it read"
  assert_no_launch "$dir" "a policy with no budget mode"
}

test_no_implicit_review_job_is_not_measured() {
  local dir=$work/no-implicit-job
  prepare "$dir"
  run_check "$dir" FAKE_JOBS_FILE=jobs-no-implicit.json
  expect_status "$status" 2 "an installation with no implicit review-only job"
  expect_row job "FAIL — no job of the installation is named 'fugaro-p-standard-github-default-rev-<hash>…' in 'gcloud run jobs list', so the implicit review-only job is NOT deployed" "no implicit job: the row"
  expect_contains "$out" "Run 'fugaro update' (it deploys the job without a profile)" "no implicit job: the fix is named"
  expect_row pull "FAIL — the installation's review-only job was not read" "no implicit job: nothing is launched on it"
  assert_no_launch "$dir" "an installation with no implicit review-only job"
}

test_two_implicit_review_jobs_is_a_finding() {
  local dir=$work/two-implicit-jobs
  prepare "$dir"
  run_check "$dir" FAKE_JOBS_FILE=jobs-two-implicit.json
  expect_status "$status" 1 "two implicit review-only jobs"
  expect_row job "FAIL — 2 jobs of the installation start with 'fugaro-p-standard-github-default-rev-'" "two implicit jobs: the row counts them"
  expect_contains "$out" "the implicit review-only job's name is one (profile, provider, account) triple plus a hash" "two implicit jobs: why it is a finding"
  assert_no_launch "$dir" "two implicit review-only jobs"
}

test_an_implicit_job_without_the_service_url_is_a_finding() {
  local dir=$work/job-no-url
  prepare "$dir"
  run_check "$dir" FAKE_JOBS_FILE=jobs-no-url.json
  expect_status "$status" 1 "an implicit job with no service URL"
  expect_row job "FAIL — the implicit review-only job '$job' carries no FUGARO_CREDENTIALS_SERVICE_URL, and a review job mounts NOTHING" "an implicit job with no service URL: the row"
  assert_no_launch "$dir" "an implicit job with no service URL"
}

test_an_implicit_job_that_names_the_secret_env_is_a_finding() {
  local dir=$work/job-secrets
  prepare "$dir"
  run_check "$dir" FAKE_JOBS_FILE=jobs-secrets.json
  expect_status "$status" 1 "an implicit job that names a secret environment"
  expect_row job "FAIL — the implicit review-only job '$job' names FUGARO_SECRET_ENVS (present) and mounts 0 secret volume(s)" "an implicit job that names a secret environment: the row"
  expect_contains "$out" "a review variant holds NO secret and NO secret volume" "an implicit job that names a secret environment: why it is a finding"
  assert_no_launch "$dir" "an implicit job that names a secret environment"
}

test_an_implicit_job_that_mounts_a_secret_volume_is_a_finding() {
  local dir=$work/job-secret-volume
  prepare "$dir"
  run_check "$dir" FAKE_JOBS_FILE=jobs-secretvolume.json
  expect_status "$status" 1 "an implicit job that mounts a secret volume"
  expect_row job "FAIL — the implicit review-only job '$job' names FUGARO_SECRET_ENVS (absent) and mounts 1 secret volume(s)" "an implicit job that mounts a secret volume: the row counts the volume"
  assert_no_launch "$dir" "an implicit job that mounts a secret volume"
}

test_a_jobs_list_that_could_not_be_read_is_not_measured() {
  local dir=$work/jobs-refused
  prepare "$dir"
  run_check "$dir" FAKE_MODE=jobs-fails
  expect_status "$status" 2 "a job list that could not be read"
  expect_row job "FAIL — gcloud run jobs list did not answer (exit 1)" "a job list that could not be read: the row says what answered"
  expect_contains "$out" "a read that did not answer is not an installation without one" "a job list that could not be read: why it is not a finding"
  assert_no_launch "$dir" "a job list that could not be read"
}

test_a_jobs_list_that_is_not_a_document_is_not_measured() {
  local dir=$work/jobs-garbage
  prepare "$dir"
  run_check "$dir" FAKE_MODE=jobs-garbage
  expect_status "$status" 2 "a job list that is not a document"
  expect_row job "FAIL — no job of the installation is named 'fugaro-p-standard-github-default-rev-<hash>…'" "a job list that is not a document: nothing is read out of it"
  assert_no_launch "$dir" "a job list that is not a document"
}

test_gh_cannot_read_the_default_branch_is_not_measured() {
  local dir=$work/no-base-branch
  prepare "$dir"
  run_check "$dir" FAKE_MODE=gh-base-fails
  expect_status "$status" 2 "a default branch that could not be read"
  expect_row pull "FAIL — gh could not read $repo's default branch (exit 1)" "a default branch that could not be read: the row"
  expect_row pull "a base branch it guessed at is a pull request against the wrong branch" "a default branch that could not be read: why it stops"
  expect_row pull "gh says: gh: could not resolve to a Repository" "a default branch that could not be read: gh's own words"
  assert_no_launch "$dir" "a default branch that could not be read"
}

test_a_fetch_that_failed_is_not_measured() {
  local dir=$work/fetch-failed
  prepare "$dir"
  run_check "$dir" FAKE_MODE=git-fetch-fails
  expect_status "$status" 2 "a fetch that failed"
  expect_row pull "FAIL — git could not fetch $base_branch from $repo's origin (exit 128)" "a fetch that failed: the row"
  expect_row pull "one cut from a guess is a pull request nobody asked for" "a fetch that failed: why it stops"
  assert_no_launch "$dir" "a fetch that failed"
}

test_a_branch_that_could_not_be_cut_is_not_measured() {
  local dir=$work/branch-failed
  prepare "$dir"
  run_check "$dir" FAKE_MODE=git-branch-fails
  expect_status "$status" 2 "a branch that could not be cut"
  expect_row pull "FAIL — git could not cut the throwaway branch $branch in $dir/sandbox (exit 128)" "a branch that could not be cut: the row"
  assert_no_launch "$dir" "a branch that could not be cut"
}

test_a_commit_that_failed_is_not_measured() {
  local dir=$work/commit-failed
  prepare "$dir"
  run_check "$dir" FAKE_MODE=git-commit-fails
  expect_status "$status" 2 "a commit that failed"
  expect_row pull "FAIL — git could not commit the throwaway file in $dir/sandbox (exit 1)" "a commit that failed: the row"
  expect_row pull "The checkout is back on $base_branch" "a commit that failed: the checkout is put back"
  assert_checkout_left_clean "$dir" "a commit that failed"
  assert_no_launch "$dir" "a commit that failed"
}

test_a_push_that_failed_is_not_measured() {
  local dir=$work/push-failed
  prepare "$dir"
  run_check "$dir" FAKE_MODE=git-push-fails
  expect_status "$status" 2 "a push that failed"
  expect_row pull "FAIL — git could not push the throwaway branch $branch to $repo (exit 1)" "a push that failed: the row"
  expect_row pull "Nothing is reviewed on a branch that is not there. The checkout is back on $base_branch" "a push that failed: the checkout is put back"
  assert_checkout_left_clean "$dir" "a push that failed"
  assert_no_launch "$dir" "a push that failed"
}

test_a_pull_request_that_could_not_be_opened_is_not_measured() {
  local dir=$work/pr-failed
  prepare "$dir"
  run_check "$dir" FAKE_MODE=gh-pr-fails
  expect_status "$status" 2 "a pull request that could not be opened"
  expect_row pull "FAIL — gh could not open the pull request for $branch (exit 1)" "a pull request that could not be opened: the row"
  expect_row pull "The branch IS on $repo — delete it" "a pull request that could not be opened: the branch is named, since it is there"
  assert_checkout_left_clean "$dir" "a pull request that could not be opened"
  assert_no_review "$dir" "a pull request that could not be opened"
}

test_a_pull_request_number_that_could_not_be_read_is_not_measured() {
  local dir=$work/pr-unread
  prepare "$dir"
  run_check "$dir" FAKE_MODE=gh-pr-nonumber
  expect_status "$status" 2 "a pull request number that could not be read"
  expect_row pull "FAIL — gh opened the pull request for $branch but could not read its number back (exit 0, it printed 'not-a-number')" "a pull request number that could not be read: the row"
  expect_row pull "a number this check guessed at is a review of another pull request" "a pull request number that could not be read: why it stops"
  assert_no_review "$dir" "a pull request number that could not be read"
}

test_a_launch_that_was_refused_is_not_measured() {
  local dir=$work/launch-refused
  prepare "$dir"
  run_check "$dir" FAKE_MODE=review-fails
  expect_status "$status" 2 "a launch that was refused"
  expect_row launch "FAIL — fugaro review --pr $pr exited 1 and launched nothing" "a launch that was refused: the row"
  expect_row launch "resolves a review-only variant of a shared project job, and this repository has none" "a launch that was refused: the launcher's own sentence"
  expect_row launch "the refusal the plan's option C removes" "a launch that was refused: why the row carries it"
  assert_one_launch "$dir" "a launch that was refused"
}

test_a_launch_that_never_answers_is_not_measured() {
  local dir=$work/launch-hangs
  prepare "$dir"
  run_check "$dir" FAKE_MODE=review-hangs CHECK610_CLI_TIMEOUT_SECONDS=1
  expect_status "$status" 2 "a launch that never answered"
  expect_row launch "FAIL — fugaro review did not answer inside 1s and was ended" "a launch that never answered: the row"
  expect_row launch "it may or may not have launched a review" "a launch that never answered: why nothing is read"
  assert_one_launch "$dir" "a launch that never answered"
}

test_a_launch_that_names_no_run_is_not_measured() {
  local dir=$work/launch-no-run
  prepare "$dir"
  run_check "$dir" FAKE_LAUNCH_FILE=launch-no-run.json
  expect_status "$status" 2 "a launch that names no run"
  expect_row launch "FAIL — fugaro review --pr $pr answered without naming the run it launched" "a launch that names no run: the row"
  assert_one_launch "$dir" "a launch that names no run"
}

test_a_launch_that_names_no_job_is_not_measured() {
  local dir=$work/launch-no-job
  prepare "$dir"
  run_check "$dir" FAKE_LAUNCH_FILE=launch-no-job.json
  expect_status "$status" 2 "a launch that names no job"
  expect_row launch "FAIL — the launch of the review run ($run_id) names no job in its own document" "a launch that names no job: the row"
  assert_one_launch "$dir" "a launch that names no job"
}

test_a_launch_on_another_job_is_a_finding() {
  local dir=$work/launch-other-job
  prepare "$dir"
  run_check "$dir" FAKE_LAUNCH_FILE=launch-other-job.json
  expect_status "$status" 1 "a review that landed on a coding job"
  expect_row launch "FAIL — fugaro review --pr $pr launched the run $run_id on '$coding_job', which is NOT the installation's implicit review-only job '$job'" "a review on another job: the row names both"
  expect_row launch "the launch selecting a CODING job or a profile's job" "a review on another job: why it is a finding"
  assert_one_launch "$dir" "a review that landed on a coding job"
}

test_a_launch_as_another_account_is_a_finding() {
  local dir=$work/launch-other-account
  prepare "$dir"
  run_check "$dir" FAKE_LAUNCH_FILE=launch-named-account.json
  expect_status "$status" 1 "a review that ran as another account"
  expect_row launch "FAIL — fugaro review --pr $pr launched the run $run_id on the review-only job '$job' as the account 'work'" "a review as another account: the row"
  expect_row launch "the implicit job serves the DEFAULT account only" "a review as another account: why it is a finding"
  assert_one_launch "$dir" "a review that ran as another account"
}

test_a_listing_that_could_not_be_read_is_not_measured() {
  local dir=$work/listing-refused
  prepare "$dir"
  run_check "$dir" FAKE_MODE=listing-fails CHECK610_RUN_SECONDS=2
  expect_status "$status" 2 "a listing that could not be read"
  expect_row run "FAIL — the review run ($run_id) does not appear in 'fugaro runs ls --repo $repo --pr $pr'" "a listing that could not be read: the row"
  expect_row run "so nothing about it is read" "a listing that could not be read: why it is not a finding"
  assert_one_launch "$dir" "a listing that could not be read"
}

test_a_run_the_listing_never_names_is_not_measured() {
  local dir=$work/other-run
  prepare "$dir"
  run_check "$dir" FAKE_ROW_FILE=row-other-run.json CHECK610_RUN_SECONDS=2
  expect_status "$status" 2 "a listing that holds another run's row"
  expect_row run "FAIL — the review run ($run_id) does not appear in 'fugaro runs ls" "a listing that holds another run's row: the row"
  expect_row run "Read 'fugaro runs show $run_id --repo $repo' yourself" "a listing that holds another run's row: what to read instead"
  assert_one_launch "$dir" "a listing that holds another run's row"
}

test_a_run_that_had_not_finished_is_not_measured() {
  local dir=$work/run-inflight
  prepare "$dir"
  run_check "$dir" FAKE_ROW_FILE=row-inflight.json CHECK610_RUN_SECONDS=2
  expect_status "$status" 2 "a run that had not finished"
  expect_row run "FAIL — the review run ($run_id) had not reached a final status after 2s: status=running" "a run that had not finished: the row"
  expect_row run "A run still in flight is not an answer about the review-only job" "a run that had not finished: why it is not measured"
  assert_one_launch "$dir" "a run that had not finished"
}

test_a_run_that_failed_is_a_finding() {
  local dir=$work/run-failed
  prepare "$dir"
  run_check "$dir" FAKE_ROW_FILE=row-failed.json
  expect_status "$status" 1 "a run that failed"
  expect_row run "FAIL — the review run ($run_id) of $repo did not end as a review: status=failed, outcome=draft, pr=$pr" "a run that failed: the row says how it ended"
  expect_row run "halt: the review stopped at its first model call" "a run that failed: the run's own reason"
  expect_row run "A review reaches 'succeeded' with outcome 'reviewed'" "a run that failed: what a review has to reach"
}

test_a_run_about_another_pull_request_is_a_finding() {
  local dir=$work/run-other-pr
  prepare "$dir"
  run_check "$dir" FAKE_ROW_FILE=row-other-pr.json
  expect_status "$status" 1 "a run about another pull request"
  expect_row run "FAIL — the review run ($run_id) of $repo ended 'succeeded' (outcome=reviewed) and its own row names pull request 9 rather than the #$pr this check opened" "a run about another pull request: the row"
}

test_a_review_that_could_not_be_read_is_a_finding() {
  local dir=$work/show-failed
  prepare "$dir"
  run_check "$dir" FAKE_MODE=show-fails
  expect_status "$status" 1 "a review that could not be read back"
  expect_row review "FAIL — fugaro review show $run_slug --json did not answer (exit 1) although the run ended 'succeeded' with outcome 'reviewed'" "a review that could not be read back: the row"
  expect_row review "which is the review NOT being recorded" "a review that could not be read back: why it is a finding"
  assert_one_launch "$dir" "a review that could not be read back"
}

test_a_review_show_that_prints_no_artifact_is_a_finding() {
  local dir=$work/no-artifact
  prepare "$dir"
  run_check "$dir" FAKE_ARTIFACT_FILE=artifact-no-artifact.json
  expect_status "$status" 1 "a review show with no artifact"
  expect_row review "FAIL — fugaro review show $run_slug --json printed no artifact" "a review show with no artifact: the row"
  expect_row review "the review the run recorded is one this installation cannot read back" "a review show with no artifact: why it is a finding"
}

test_an_artifact_about_another_run_is_a_finding() {
  local dir=$work/artifact-other-run
  prepare "$dir"
  run_check "$dir" FAKE_ARTIFACT_FILE=artifact-other-run.json
  expect_status "$status" 1 "an artifact about another run"
  expect_row review "FAIL — the artifact of the review run ($run_id) does not state this review: review_run matches the run 'no'" "an artifact about another run: the row"
}

test_an_artifact_about_another_pull_request_is_a_finding() {
  local dir=$work/artifact-other-pr
  prepare "$dir"
  run_check "$dir" FAKE_ARTIFACT_FILE=artifact-other-pr.json
  expect_status "$status" 1 "an artifact about another pull request"
  expect_row review "FAIL — the artifact of the review run ($run_id) does not state this review: review_run matches the run 'yes', pr matches #$pr 'no'" "an artifact about another pull request: the row"
}

test_an_artifact_with_no_verdict_is_a_finding() {
  local dir=$work/artifact-no-verdict
  prepare "$dir"
  run_check "$dir" FAKE_ARTIFACT_FILE=artifact-no-verdict.json
  expect_status "$status" 1 "an artifact with no verdict"
  expect_row review "does not state this review: review_run matches the run 'yes', pr matches #$pr 'yes', head_sha '$head_sha', verdict '-'" "an artifact with no verdict: the row names what is missing"
}

test_an_image_that_is_not_the_base_image_fails_u1() {
  local dir=$work/u1-other-image
  prepare "$dir"
  # The job holds everything a review job has to hold — the service's address, no
  # secret — and runs an image that is not the installation's: the job row cannot
  # see that, and U1 is the row that can.
  run_check "$dir" FAKE_JOBS_FILE=jobs-other-image.json
  expect_status "$status" 1 "a review job on another image"
  expect_row job "PASS — the installation's implicit review-only job" "a review job on another image: the job row is not the one that reads the image"
  expect_row u1 "FAIL — U1: $repo's workflow is baked and the review-only job '$job' runs the image '$other_image', which is NOT the installation's base image '$base_image'" "a review job on another image: the row names both images"
  expect_row u1 "D2388 answers U1 'yes for now' on the ground that the review recipe only READS the checkout" "a review job on another image: why it is a finding"
  assert_one_launch "$dir" "a review job on another image"
}

test_a_doctor_with_no_base_image_leaves_u1_unmeasured() {
  local dir=$work/u1-no-base
  prepare "$dir"
  run_check "$dir" FAKE_DOCTOR_FILE=doctor-nobase.json
  expect_status "$status" 2 "a doctor with no base image"
  expect_row u1 "FAIL — fugaro doctor --json carried no base image for this installation (project.base_images.base), so the review-only job's image '$base_image' cannot be compared with it" "a doctor with no base image: the row"
  expect_row u1 "an image this check has no other side to compare against is not that row" "a doctor with no base image: why it is not measured"
  assert_one_launch "$dir" "a doctor with no base image"
}

test_a_default_account_that_is_not_admitted_fails_u2() {
  local dir=$work/u2-default-not-admitted
  prepare "$dir"
  # The PROJECT admits the repository and the DEFAULT account does not: the policy
  # row reads the first and says the second is U2's own row, and U2 is where the
  # gap the plan puts on record is measured.
  run_check "$dir" FAKE_POLICY_FILE=policy-default-not-admitted.json
  expect_status "$status" 1 "a default account the policy does not admit"
  expect_row policy "PASS — $install's published policy carries NO profiles block and admits $repo to the project" "a default account the policy does not admit: the policy row"
  expect_row u2 "FAIL — U2: $install's published policy carries no profiles block and its accounts block does NOT admit the default account ('default') to $repo" "a default account the policy does not admit: the row"
  expect_row u2 "credsvc's admittedAccounts" "a default account the policy does not admit: the service that would refuse the call"
  assert_one_launch "$dir" "a default account the policy does not admit"
}

test_an_auth_that_is_not_an_api_key_or_vertex_leaves_u3_unmeasured() {
  local dir=$work/u3-cascade
  prepare "$dir"
  run_check "$dir" FAKE_CONFIG_FILE=config-cascade.json
  expect_status "$status" 2 "a checkout authenticating another way"
  expect_row u3 "FAIL — the checkout resolves agent.auth 'cascade', which is not one of the two U3 is about (an API key or Vertex)" "a checkout authenticating another way: the row"
  expect_row u3 "Name a sandbox repository whose fugaro.yaml resolves agent.auth 'vertex' or 'api-key'" "a checkout authenticating another way: what to do instead"
  # The row is not measured, so the service's lines are never read for it.
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "logging read" "a checkout authenticating another way: no log read is made"
  assert_one_launch "$dir" "a checkout authenticating another way"
}

test_a_checkout_that_resolves_no_auth_leaves_u3_unmeasured() {
  local dir=$work/u3-no-auth
  prepare "$dir"
  run_check "$dir" FAKE_CONFIG_FILE=config-no-auth.json
  expect_status "$status" 2 "a checkout resolving no auth"
  expect_row u3 "FAIL — the checkout resolves no agent.auth at all" "a checkout resolving no auth: the row"
  expect_row u3 "Set agent.auth in $repo's fugaro.yaml" "a checkout resolving no auth: what to do instead"
  assert_one_launch "$dir" "a checkout resolving no auth"
}

test_an_artifact_naming_another_account_is_a_finding() {
  local dir=$work/u3-artifact-account
  prepare "$dir"
  run_check "$dir" FAKE_ARTIFACT_FILE=artifact-other-account.json
  expect_status "$status" 1 "an artifact naming another account"
  expect_row u3 "FAIL — U3: the review's model calls were served as the default account ('default' in the credentials service's own audit for the run $run_id) and the artifact the review wrote names the reviewer account 'work'" "an artifact naming another account: the row names both readings"
  expect_row u3 "the account the review states it ran as and the account the service really served are two readings of one fact, and they disagree" "an artifact naming another account: why it is a finding"
  assert_one_launch "$dir" "an artifact naming another account"
}

test_a_run_the_service_wrote_no_forward_line_for_is_a_finding() {
  local dir=$work/u3-no-forward
  prepare "$dir"
  run_check "$dir" FAKE_LOG_FILE=log-no-forward.json
  expect_status "$status" 1 "a run with no served model line"
  expect_row u3 "FAIL — U3: the checkout resolves agent.auth 'vertex' and the review run ($run_id) has NO served model line (purpose 'forward', status ok)" "a run with no served model line: the row"
  expect_row u3 "a review-only job mounts nothing and the service is its only route to one" "a run with no served model line: why it is a finding"
  assert_one_launch "$dir" "a run with no served model line"
}

test_a_first_model_call_with_another_credential_is_a_finding() {
  local dir=$work/u3-other-credential
  prepare "$dir"
  run_check "$dir" FAKE_LOG_FILE=log-other-credential.json
  expect_status "$status" 1 "a first model call on another credential"
  expect_row u3 "FAIL — U3: the review run's first model call ($run_id) was served with the credential 'work', not the DEFAULT account ('default')" "a first model call on another credential: the row"
  expect_row u3 "the implicit review-only job serves that account and no other (config.DefaultClaudeAccount, D2385)" "a first model call on another credential: why it is a finding"
  assert_one_launch "$dir" "a first model call on another credential"
}

test_the_first_model_call_is_read_by_its_time() {
  local dir=$work/u3-two-forwards
  prepare "$dir"
  # Two served model calls, and the EARLIEST by its own timestamp is the default
  # account's: the document's first forward line is the later switch, so a read that
  # took that one would fail this row on a review that did nothing wrong.
  run_check "$dir" FAKE_LOG_FILE=log-two-forwards.json
  expect_status "$status" 0 "the earliest served call by its own time"
  expect_row u3 "PASS — U3: the checkout resolves agent.auth 'vertex' and the review's model calls were served by the installation's credentials service as the credential 'default'" "the earliest served call by its own time: the row"
  expect_row u3 "the earliest served line of purpose 'forward' the service wrote for the run $run_id, read by NAME and never by value" "the earliest served call by its own time: how it was read"
}

test_a_line_of_another_run_is_not_read() {
  local dir=$work/u3-other-run
  prepare "$dir"
  # The read answers with a line, and the line is another run's: the FILTER is what
  # keeps it out of this row, and a row that counted it would read a credential off
  # a review this check never launched.
  run_check "$dir" FAKE_LOG_FILE=log-other-run.json CHECK610_LOG_WAIT_SECONDS=1
  expect_status "$status" 1 "a served line of another run"
  expect_row u3 "has NO served model line (purpose 'forward', status ok)" "a served line of another run: it is not this run's"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "jsonPayload.run=\"$run_id\"" "a served line of another run: the read is filtered by the run's own id"
  assert_one_launch "$dir" "a served line of another run"
}

test_a_log_read_that_failed_leaves_u3_unmeasured() {
  local dir=$work/u3-log-failed
  prepare "$dir"
  # A read that did not answer is NOT the service having written no line: the row
  # is unmeasured, and the reason has to be the read's own, not a finding.
  run_check "$dir" FAKE_MODE=log-fails CHECK610_LOG_WAIT_SECONDS=1
  expect_status "$status" 2 "a log read that failed"
  expect_row u3 "FAIL — the service's own lines for the review run ($run_id) could not be read: 'gcloud logging read' exited 1" "a log read that failed: the row"
  expect_row u3 "a read that did not answer is NOT the service having written no line" "a log read that failed: why it is not a finding"
  assert_one_launch "$dir" "a log read that failed"
}

test_the_log_read_asks_for_the_oldest_lines() {
  local dir=$work/u3-limit
  prepare "$dir"
  # gcloud answers the NEWEST --limit lines by default, and a review writes more
  # model calls than this check reads: the first one — the row U3 is about — is the
  # OLDEST, so the read has to ask for the oldest and the knob has to reach gcloud.
  run_check "$dir" CHECK610_LOG_LIMIT=3
  expect_status "$status" 0 "the log limit"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "--limit=3 --order=asc" "the log limit: the read is the oldest 3 lines"
  expect_row u3 "PASS — U3" "the log limit: the row still reads the first call"
}

test_the_log_window_is_widened_by_the_time_since_the_launch() {
  local dir=$work/u3-window
  prepare "$dir"
  # CHECK610_LOG_AGE is the floor of the window; the check widens it by the time
  # since the launch, because a review that outlasts the knob would otherwise lose
  # the head of its own lines — the first model call, which is this row.
  run_check "$dir" CHECK610_LOG_AGE=1m
  expect_status "$status" 0 "a window widened by the launch"
  local fresh
  fresh=$(grep -o -- '--freshness=[0-9a-z]*' "$dir/logs/gcloud.calls" | head -n 1)
  case "$fresh" in
  --freshness=1m | "")
    fail "a window widened by the launch: the read asked for '${fresh:-no freshness at all}' rather than a window that reaches back past the launch"
    ;;
  --freshness=[0-9]*[mhd]) ;;
  *) fail "a window widened by the launch: the read asked for '$fresh', which is not a window" ;;
  esac
  expect_row u3 "PASS — U3" "a window widened by the launch: the row is still read"
}

test_an_installation_that_deploys_a_coding_project_job_fails_u4() {
  local dir=$work/u4-coding-job
  prepare "$dir"
  # The job list holds the per-repository job, the implicit review-only job and a
  # coding project job: U4 is about an installation that deploys NONE, and the
  # per-repository job is not a project job at all (it is named for the repository).
  run_check "$dir" FAKE_JOBS_FILE=jobs-coding.json
  expect_status "$status" 1 "an installation with a coding project job"
  expect_row u4 "FAIL — U4: $install deploys 1 project job(s) which are NOT an implicit review-only job — $coding_job out of 2 job(s) named 'fugaro-p-…'" "an installation with a coding project job: the row"
  expect_row u4 "U4 is 'a live terraform apply with NO project jobs'" "an installation with a coding project job: why it is a finding"
  expect_absent "$(printf '%s\n' "$out" | grep -m1 '^FACT u4: ' || true)" "$per_repo_job" "an installation with a coding project job: the per-repository job is not a project job"
  # The review itself still landed on the review-only job: U4's finding is about
  # the apply, not about where this check's review ran.
  expect_row u1 "PASS — U1" "an installation with a coding project job: U1 still holds"
  assert_one_launch "$dir" "an installation with a coding project job"
}

test_a_profiles_review_variant_is_not_an_implicit_job() {
  local dir=$work/u4-profile-review
  prepare "$dir"
  # A profile's own review variant is a project job like any other, and its name
  # carries the word `review`: a check that read the name for a `-review-` segment
  # would count it as a review-only job and pass U4 on an installation that deploys
  # a profile. The classification is the derivation, so it fails.
  run_check "$dir" FAKE_JOBS_FILE=jobs-profile-review.json
  expect_status "$status" 1 "a profile's review variant"
  expect_row u4 "FAIL — U4: $install deploys 1 project job(s) which are NOT an implicit review-only job — $profile_review_job out of 2 job(s) named 'fugaro-p-…'" "a profile's review variant: the row counts it"
  expect_row u4 "a job of a profile: a coding job, or the profile's own review variant" "a profile's review variant: why it is a finding"
  assert_one_launch "$dir" "a profile's review variant"
}

test_the_bitbucket_implicit_job_is_a_review_variant_too() {
  local dir=$work/u4-bitbucket-implicit
  prepare "$dir"
  # The credentials service on and a Bitbucket repository recorded deploys the
  # Bitbucket implicit review-only job BESIDE the github one: both are derivations
  # of the same row, and an installation that deploys both has no job of a profile.
  run_check "$dir" FAKE_JOBS_FILE=jobs-bitbucket-implicit.json
  expect_status "$status" 0 "an installation with the Bitbucket implicit job"
  expect_row u4 "PASS — U4: $install ($gcp_project, region $region) deploys 2 project job(s) and EVERY one of them is an implicit review-only job — $job,$bitbucket_job — with no job of a profile at all" "an installation with the Bitbucket implicit job: both names are counted as review variants"
}

test_a_job_of_another_hash_is_not_the_implicit_job() {
  local dir=$work/job-other-hash
  prepare "$dir"
  # The stem matches and the twelve hex of the hash do not: this is not the name
  # infra.ImplicitReviewJobName derives from the triple, so the job this check's
  # rows are about is not deployed under the name they are held to.
  run_check "$dir" FAKE_JOBS_FILE=jobs-other-hash.json
  expect_status "$status" 2 "a job of another hash on the implicit stem"
  expect_row job "FAIL — the installation's job of the implicit review-only stem is '${job%-*}-abcdefabcdef', and the name infra.ImplicitReviewJobName derives from the triple" "a job of another hash on the implicit stem: the row names both"
  expect_contains "$out" "'$job': a job whose hash is not the one the derivation gives" "a job of another hash on the implicit stem: the derived name is spelled"
  assert_no_launch "$dir" "a job of another hash on the implicit stem"
}

test_kept_scratch_is_reported() {
  local dir=$work/keep-scratch
  prepare "$dir"
  run_check "$dir" CHECK610_KEEP_SCRATCH=1
  expect_status "$status" 0 "kept scratch"
  expect_row scratch-removed "PASS — kept on request (CHECK610_KEEP_SCRATCH is set), at $scratch" "kept scratch: the row names where it is"
  [ -d "$scratch" ] || fail "kept scratch: the scratch directory is not there after all"
}

test_every_row_of_the_block_is_reached() {
  local dir=$work/block-shape
  prepare "$dir"
  # The block's keys and their order are the register's contract, quoted row by row:
  # a key this check forgets to print reads `FAIL — not reached` on a run that failed
  # for some other reason too, which is why a key removed from the check has to be
  # removed here in the same diff.
  run_check "$dir"
  expect_status "$status" 0 "the block's shape"
  expect_absent "$out" "not reached" "the block's shape: no row is missing"
  local key
  for key in fugaro-version xtrace agent-session belong sandbox config policy job pull launch run review u1 u2 u3 u4 restore scratch-removed; do
    expect_contains "$out" "FACT $key: " "the block's shape: the $key row"
  done
}

test_the_sandbox_can_be_named_as_an_argument() {
  local dir=$work/sandbox-argument
  prepare "$dir"
  # The documented interface is the argument, and an empty environmental value has
  # to fall through to it rather than win as a value the check was given.
  run_check "$dir" CHECK610_SANDBOX= -- --sandbox "$dir/sandbox"
  expect_status "$status" 0 "a sandbox named as an argument"
  expect_row sandbox "PASS — $dir/sandbox is a Fugaro repository's checkout of the GITHUB repository $repo" "a sandbox named as an argument: the row"
  assert_scratch_removed "$dir" "a sandbox named as an argument"
}

test_a_checkout_whose_origin_names_belong_is_refused() {
  local dir=$work/origin-guard
  prepare "$dir"
  # The repository name is read from the origin and reaches the same guard the
  # installation's own name does.
  run_check "$dir" FAKE_ORIGIN=https://github.com/belong/chk610-sandbox.git
  expect_status "$status" 1 "a repository named belong"
  expect_contains "$err" "belongs to $install ($gcp_project, region $region) and belong/chk610-sandbox" "a repository named belong: the refusal names what it read"
  expect_row sandbox "FAIL" "a repository named belong: the row"
  # The guard reads the installation before it compares the name it read with it:
  # the refusal names both sides, and the repository's own name is the side that
  # came from the origin.
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "doctor --json (in $dir/sandbox)" "a repository named belong: the installation is read, and named in the refusal"
  assert_no_launch "$dir" "a repository named belong"
}

# ------------------------------------------------------------------- the run
# CHECK_TEST_ONLY names one case; CHECK_TEST_SHARD=i/n runs every case whose index
# is congruent to i modulo n. scripts/live_checks_test.go splits the suite across
# workers that way, so a case added above needs its `t` line below or CI never
# runs it at all.
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
t test_an_unknown_argument_is_refused
t test_a_missing_sandbox_is_refused
t test_a_path_that_is_not_a_checkout_is_refused
t test_a_missing_tool_stops_before_anything_is_called
t test_xtrace_is_refused
t test_a_coding_agents_session_is_refused
t test_belong_is_refused_by_name
t test_a_value_that_names_belong_is_refused_by_the_repository_guard
t test_a_checkout_with_no_origin_is_refused
t test_an_origin_that_names_no_repository_is_refused
t test_an_origin_that_is_not_github_is_refused
t test_a_credential_in_the_origin_is_never_printed
t test_a_dirty_checkout_is_refused
t test_a_doctor_whose_checks_are_not_all_ok_is_an_answer
t test_a_doctor_that_never_answers_is_not_measured
t test_a_doctor_that_carries_no_project_is_not_measured
t test_a_doctor_that_is_not_a_document_is_not_measured
t test_a_checkout_whose_fugaro_yaml_carries_profiles_is_not_measured
t test_a_checkout_with_no_workflow_is_not_measured
t test_a_workflow_that_is_not_baked_is_not_measured
t test_a_second_workflow_that_is_not_baked_is_not_measured
t test_a_config_that_could_not_be_read_is_not_measured
t test_a_policy_that_could_not_be_read_is_not_measured
t test_a_repository_the_project_does_not_admit_is_not_measured
t test_a_policy_with_profiles_is_not_measured
t test_a_policy_with_no_budget_mode_is_not_measured
t test_no_implicit_review_job_is_not_measured
t test_two_implicit_review_jobs_is_a_finding
t test_an_implicit_job_without_the_service_url_is_a_finding
t test_an_implicit_job_that_names_the_secret_env_is_a_finding
t test_an_implicit_job_that_mounts_a_secret_volume_is_a_finding
t test_a_jobs_list_that_could_not_be_read_is_not_measured
t test_a_jobs_list_that_is_not_a_document_is_not_measured
t test_gh_cannot_read_the_default_branch_is_not_measured
t test_a_fetch_that_failed_is_not_measured
t test_a_branch_that_could_not_be_cut_is_not_measured
t test_a_commit_that_failed_is_not_measured
t test_a_push_that_failed_is_not_measured
t test_a_pull_request_that_could_not_be_opened_is_not_measured
t test_a_pull_request_number_that_could_not_be_read_is_not_measured
t test_a_launch_that_was_refused_is_not_measured
t test_a_launch_that_never_answers_is_not_measured
t test_a_launch_that_names_no_run_is_not_measured
t test_a_launch_that_names_no_job_is_not_measured
t test_a_launch_on_another_job_is_a_finding
t test_a_launch_as_another_account_is_a_finding
t test_a_listing_that_could_not_be_read_is_not_measured
t test_a_run_the_listing_never_names_is_not_measured
t test_a_run_that_had_not_finished_is_not_measured
t test_a_run_that_failed_is_a_finding
t test_a_run_about_another_pull_request_is_a_finding
t test_a_review_that_could_not_be_read_is_a_finding
t test_a_review_show_that_prints_no_artifact_is_a_finding
t test_an_artifact_about_another_run_is_a_finding
t test_an_artifact_about_another_pull_request_is_a_finding
t test_an_artifact_with_no_verdict_is_a_finding
t test_an_image_that_is_not_the_base_image_fails_u1
t test_a_doctor_with_no_base_image_leaves_u1_unmeasured
t test_a_default_account_that_is_not_admitted_fails_u2
t test_an_auth_that_is_not_an_api_key_or_vertex_leaves_u3_unmeasured
t test_a_checkout_that_resolves_no_auth_leaves_u3_unmeasured
t test_an_artifact_naming_another_account_is_a_finding
t test_a_run_the_service_wrote_no_forward_line_for_is_a_finding
t test_a_first_model_call_with_another_credential_is_a_finding
t test_the_first_model_call_is_read_by_its_time
t test_a_line_of_another_run_is_not_read
t test_a_log_read_that_failed_leaves_u3_unmeasured
t test_the_log_read_asks_for_the_oldest_lines
t test_the_log_window_is_widened_by_the_time_since_the_launch
t test_a_job_of_another_hash_is_not_the_implicit_job
t test_an_installation_that_deploys_a_coding_project_job_fails_u4
t test_a_profiles_review_variant_is_not_an_implicit_job
t test_the_bitbucket_implicit_job_is_a_review_variant_too
t test_kept_scratch_is_reported
t test_every_row_of_the_block_is_reached
t test_the_sandbox_can_be_named_as_an_argument
t test_a_checkout_whose_origin_names_belong_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check610_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check610_test.sh: ok"
