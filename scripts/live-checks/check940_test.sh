#!/usr/bin/env bash
# Tests for scripts/live-checks/check940.sh, with fake `gcloud` and `fugaro` on
# PATH: NOTHING here reaches a cloud, a project or the network. The fake fugaro
# answers the three reads the check makes — `doctor --json` for the
# installation's project and region, `project policy show --json` for the
# published policy node (which is where the repository-to-image map,
# `repo_images`, is read), and `image survey --json` for the survey document —
# out of the mode it is given, and the fake gcloud answers the installation's
# Cloud Run jobs out of the same mode. Both record every argument they are
# handed, so the tests can assert that the check read the named installation's
# project and region and nothing else, that a repository's job image is held to
# the catalog entry its own `provider/owner/name` key names, that a node still
# holding the legacy `image_map` FAILs (the plan's red step for this task), and
# that a provider the map names no repository at is skipped rather than failed.
#
# Usage: bash scripts/live-checks/check940_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check940.sh
work=$(mktemp -d)
kept=()
cleanup_all() {
  local d
  for d in ${kept[@]+"${kept[@]}"}; do rm -rf "$d" 2>/dev/null || true; done
  if [ -n "${CHECK940_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
expect_clean_scratch() { # what
  if [ -d "$scratch" ]; then fail "$1: the scratch directory survived"; fi
}

# --------------------------------------------------------------------- fakes
# The two repositories, their catalog entries and the registry host are the
# constants below, so a test names the MODE it wants and gets a whole
# installation in that state. The map of the default mode is the map a project
# whose two repositories are mapped really holds, keyed `provider/owner/name`.
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check940.sh's test only.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE_LOG_DIR/fugaro.calls"
case "${1:-}" in
doctor)
  # `fugaro doctor --json`: the installation's project and region. The check
  # reads them from HERE and never from `gcloud config`, so a stray config
  # cannot point it at another project.
  case "${FAKE_FUGARO_MODE:-ok}" in
  belong) printf '{"ok":true,"checks":[],"project":{"name":"fugaro","gcp_project":"belong-prod","region":"us-east5"}}\n' ;;
  *) printf '{"ok":true,"checks":[],"project":{"name":"fugaro","gcp_project":"fugaro-dev","region":"us-east5"}}\n' ;;
  esac
  ;;
project)
  # `fugaro project policy show --json --project <name>`: the published policy
  # node, whose `repo_images` member is the repository-to-image map keyed by
  # provider (D3371) and whose `image_map` member is the legacy one a release
  # before this one published.
  case "$*" in
  *"policy show"*) ;;
  *) exit 1 ;;
  esac
  case "${FAKE_FUGARO_MODE:-ok}" in
  node-unreadable)
    echo "error: the published policy could not be read (no such object)" >&2
    exit 1
    ;;
  legacy-node) printf '{"policy":{"image_map":{"acme/sandbox":"sandbox-web","acme/bb-sandbox":"sandbox-bb"}}}\n' ;;
  both-members) printf '{"policy":{"repo_images":{"github/acme/sandbox":"sandbox-web","bitbucket/acme/bb-sandbox":"sandbox-bb"},"image_map":{"acme/sandbox":"sandbox-web","acme/bb-sandbox":"sandbox-bb"}}}\n' ;;
  empty-node) printf '{"policy":{}}\n' ;;
  github-only) printf '{"policy":{"repo_images":{"github/acme/sandbox":"sandbox-web"}}}\n' ;;
  *) printf '{"policy":{"repo_images":{"github/acme/sandbox":"sandbox-web","bitbucket/acme/bb-sandbox":"sandbox-bb"}}}\n' ;;
  esac
  ;;
image)
  # `fugaro image survey --json`: the survey document, whose version this check
  # holds to 2 (R4).
  case "$*" in
  *"survey --json"*) ;;
  *) exit 1 ;;
  esac
  case "${FAKE_FUGARO_MODE:-ok}" in
  survey-v1) printf '{"version":1,"project":"fugaro","repos":[],"setup":{"catalog":[],"image_map":{"acme/sandbox":"sandbox-web"}}}\n' ;;
  *) printf '{"version":2,"project":"fugaro","repos":[],"setup":{"catalog":[],"repo_images":{"github/acme/sandbox":"sandbox-web","bitbucket/acme/bb-sandbox":"sandbox-bb"},"legacy_image_map":{}}}\n' ;;
  esac
  ;;
*) exit 1 ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env python3
"""A stand-in for `gcloud`, for scripts/live-checks/check940.sh's test only: it
answers `run jobs list` out of a project built from FAKE_GCLOUD_MODE and never
talks to anything.

The jobs below are the shape a real list has: the installation's daily check
jobs (`fugarochk-…`, whose image is not a repository's job image), one job per
repository carrying the repository's clone URL as FUGARO_REMOTE and the
repository's slug as the fugaro_repo label, and one job of a repository this
installation's map does not name (which is never this check's answer).
"""
import json, os, sys

LOG = os.environ["FAKE_LOG_DIR"]
mode = os.environ.get("FAKE_GCLOUD_MODE", "ok")

with open(os.path.join(LOG, "gcloud.calls"), "a") as fh:
    fh.write("gcloud " + " ".join(sys.argv[1:]) + "\n")

CATALOG_IMAGE = "us-east5-docker.pkg.dev/fugaro-dev/fugaro-shared-images/catalog-%s"


def job(name, slug, remote, image, labels=None):
    return {
        "metadata": {"name": name, "labels": dict(labels or {}, fugaro_repo=slug)},
        "spec": {"template": {"spec": {"template": {"spec": {"containers": [
            {"image": image, "env": [{"name": "FUGARO_REMOTE", "value": remote}]}
        ]}}}}},
    }


def jobs():
    out = [
        job("fugarochk-acme-sandbox-0123456789abcdef", "acme-sandbox-0123456789abcdef",
            "https://github.com/acme/sandbox.git",
            CATALOG_IMAGE % "sandbox-bb"),
        job("fugaro-other-repo-aaaaaaaaaaaaaaaa", "other-repo-aaaaaaaaaaaaaaaa",
            "https://github.com/acme/other-repo.git",
            CATALOG_IMAGE % "sandbox-other", {"fugaro_repo": "other-repo-aaaaaaaaaaaaaaaa"}),
    ]
    if mode == "job-other-entry":
        out.append(job("fugaro-acme-sandbox-0123456789abcdef", "acme-sandbox-0123456789abcdef",
                       "https://github.com/acme/sandbox.git", CATALOG_IMAGE % "sandbox-other"))
    else:
        out.append(job("fugaro-acme-sandbox-0123456789abcdef", "acme-sandbox-0123456789abcdef",
                       "https://github.com/acme/sandbox.git", CATALOG_IMAGE % "sandbox-web"))
    out.append(job("fugaro-acme-bb-sandbox-fedcba9876543210", "acme-bb-sandbox-fedcba9876543210",
                   "https://bitbucket.org/acme/bb-sandbox.git", CATALOG_IMAGE % "sandbox-bb"))
    return out


args = sys.argv[1:]
if args[:3] != ["run", "jobs", "list"]:
    sys.stderr.write("gcloud: unsupported call in this test: %s\n" % " ".join(args))
    sys.exit(1)
if mode == "jobs-fail":
    sys.stderr.write("ERROR: (gcloud.run.jobs.list) the request was refused: the session has expired\n")
    sys.exit(1)
if mode == "no-jobs":
    print("[]")
    sys.exit(0)
print(json.dumps(jobs()))
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

# ------------------------------------------------------------------- harness
status=0
out=""
err=""
scratch=""
run_args=()
run_env=()
xtrace_runner=no

run940() { # dir mode — the script's own arguments are in run_args, extra env in run_env
  local dir=$1 mode=$2
  mkdir -p "$dir/state" "$dir/logs"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  # CHECK940_TEST_XTRACE starts the shell the way an owner might by habit: with
  # tracing on, which is the one leak this check's own block cannot catch.
  local -a runner=(bash)
  if [ "$xtrace_runner" = yes ]; then runner=(bash -x); fi
  status=0
  env \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_FUGARO_MODE="$mode" FAKE_GCLOUD_MODE="$mode" \
    ${run_env[@]+"${run_env[@]}"} \
    "${runner[@]}" "$script" ${run_args[@]+"${run_args[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  run_args=()
  run_env=()
  xtrace_runner=no
}

prepare() { # dir
  mkdir -p "$1/home" "$1/tmp" "$1/state"
  write_fake_fugaro "$1"
  write_fake_gcloud "$1"
}

# --------------------------------------------------------------- the tests
# --dry-run is the mode to try first, on a machine that may have no credentials
# at all: it prints the plan and calls NOTHING.
test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_env=(CHECK940_DRY_RUN=1)
  run940 "$dir" ok
  expect_status "$status" 0 "dry run: the verdict"
  expect_contains "$out" "CHECK 940 RESULT" "dry run: the block"
  expect_contains "$err" "repo_images" "dry run: the plan names the member"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the row"
  # The guards that read the cloud are NOT run in this mode — that is what makes
  # it safe on a machine with no credentials — so their rows say the run did not
  # reach them rather than claiming a state nobody read.
  expect_contains "$out" "FACT node: FAIL — not reached" "dry run: a cloud row is not reached"
  if [ -s "$dir/logs/gcloud.calls" ]; then fail "dry run: gcloud was called"; fi
  if [ -s "$dir/logs/fugaro.calls" ]; then fail "dry run: fugaro was called"; fi
  expect_clean_scratch "dry run"
}

# The whole point of this check: a node carrying the map keyed by provider and no
# legacy map, a survey document at version 2, and one job image per provider held
# to the entry that provider's own key names. Every row is measured and the run
# ends 0.
test_happy_path() {
  local dir=$work/ok
  prepare "$dir"
  run940 "$dir" ok
  expect_status "$status" 0 "happy path: the verdict"
  expect_contains "$out" "FACT node: PASS" "happy path: the node"
  expect_contains "$out" "carries the repository-to-image map (repo_images) and no legacy image_map" "happy path: the node's members"
  expect_contains "$out" "FACT survey: PASS" "happy path: the survey"
  expect_contains "$out" "prints version 2" "happy path: the survey's version"
  expect_contains "$out" "FACT job-github: PASS" "happy path: the GitHub job image"
  expect_contains "$out" "FACT job-bitbucket: PASS" "happy path: the Bitbucket job image"
  # The job row names the repository the key was read at, the job and the entry
  # its image is: the three facts that tell a provider-keyed map from a bare-name
  # one.
  expect_contains "$out" "the job of acme/sandbox" "happy path: the repository is named"
  expect_contains "$out" "the catalog entry sandbox-web's image" "happy path: the entry is named"
  expect_contains "$out" "the repository's own github/acme/sandbox key" "happy path: the key is the provider's"
  expect_contains "$out" "FACT scratch-removed: PASS" "happy path: cleanup"
  expect_absent "$out" "FAIL" "happy path: no row failed"
  expect_clean_scratch "happy path"
}

# THE RED STEP OF THIS TASK. A node that still publishes the legacy `image_map`
# and no `repo_images` — an installation this release has not reached — FAILs the
# node row, and the check goes on to measure what it can.
test_the_node_that_still_holds_image_map_fails() {
  local dir=$work/legacy-node
  prepare "$dir"
  run940 "$dir" legacy-node
  expect_status "$status" 1 "legacy node: the verdict"
  expect_contains "$out" "FACT node: FAIL" "legacy node: the row"
  expect_contains "$out" "still holds a legacy image_map" "legacy node: what was read"
  expect_contains "$out" "Run 'fugaro update'" "legacy node: the fix is named"
  expect_absent "$out" "FACT node: PASS" "legacy node: it is not a pass"
  # A FAILed criterion does not stop the run: the survey is still measured.
  expect_contains "$out" "FACT survey: PASS" "legacy node: the survey is still measured"
  expect_clean_scratch "legacy node"
}

# Both members at once is an `update` that stopped between writing the new member
# and removing the old one: nothing is mis-mapped (repo_images wins), and the row
# says so rather than passing silently.
test_a_node_that_holds_both_members_fails() {
  local dir=$work/both
  prepare "$dir"
  run940 "$dir" both-members
  expect_status "$status" 1 "both members: the verdict"
  expect_contains "$out" "FACT node: FAIL" "both members: the row"
  expect_contains "$out" "AND a legacy image_map" "both members: what was read"
  expect_contains "$out" "repo_images wins wherever both are read" "both members: nothing is mis-mapped"
}

# A node publishing a policy with neither member: the map is not there at all.
test_a_node_with_no_map_at_all_fails() {
  local dir=$work/emptynode
  prepare "$dir"
  run940 "$dir" empty-node
  expect_status "$status" 1 "empty node: the verdict"
  expect_contains "$out" "FACT node: FAIL" "empty node: the row"
  expect_contains "$out" "holds no repository-to-image map at all" "empty node: what was read"
}

# The survey document's version is the criterion (R4): a version-1 document is
# the pre-release shape, whatever it carries.
test_the_survey_at_version_1_fails() {
  local dir=$work/survey1
  prepare "$dir"
  run940 "$dir" survey-v1
  expect_status "$status" 1 "survey v1: the verdict"
  expect_contains "$out" "FACT survey: FAIL" "survey v1: the row"
  expect_contains "$out" "prints version 1, not version 2" "survey v1: what was read"
  expect_contains "$out" "FACT node: PASS" "survey v1: the node is still measured"
}

# V3: a project with no Bitbucket repository skips that step with a line and
# never fails it — the map it holds is right, and whether the project has such a
# repository is the owner's to settle.
test_a_provider_the_map_does_not_name_is_skipped() {
  local dir=$work/githubonly
  prepare "$dir"
  run940 "$dir" github-only
  expect_status "$status" 0 "one provider: the verdict"
  expect_contains "$out" "FACT job-github: PASS" "one provider: the GitHub row is measured"
  expect_contains "$out" "FACT job-bitbucket: INFO" "one provider: the skipped row"
  expect_contains "$out" "names no bitbucket repository, so this step is skipped" "one provider: the line"
  expect_absent "$out" "FAIL" "one provider: no row failed"
}

# A job whose image is another entry's: the map says one thing and the job runs
# another, which is exactly the mis-mapping this release is about.
test_a_job_running_another_entry_fails() {
  local dir=$work/otherentry
  prepare "$dir"
  run940 "$dir" job-other-entry
  expect_status "$status" 1 "another entry: the verdict"
  expect_contains "$out" "FACT job-github: FAIL" "another entry: the row"
  expect_contains "$out" "and that is not the catalog entry sandbox-web's image" "another entry: what was read"
  expect_contains "$out" "fugaro image map set github/acme/sandbox" "another entry: the fix names the provider key"
}

# A job list this check cannot read is a row it could NOT measure: never a pass,
# and the verdict is "could not measure" (exit 2) rather than a criterion failing.
test_a_jobs_list_that_cannot_be_read_is_unmeasured() {
  local dir=$work/jobsfail
  prepare "$dir"
  run940 "$dir" jobs-fail
  expect_status "$status" 2 "jobs unreadable: the verdict"
  expect_contains "$out" "FACT job-github: FAIL" "jobs unreadable: the row is not a pass"
  expect_contains "$out" "Cloud Run jobs could not be read" "jobs unreadable: the reason"
  expect_contains "$out" "CHECK 940 RESULT END (exit 2)" "jobs unreadable: the block"
}

# No job of the repository at all is a criterion that did not hold: the map names
# an entry but nothing deploys a job for the repository.
test_a_repository_with_no_job_fails() {
  local dir=$work/nojobs
  prepare "$dir"
  run940 "$dir" no-jobs
  expect_status "$status" 1 "no jobs: the verdict"
  expect_contains "$out" "FACT job-github: FAIL" "no jobs: the row"
  expect_contains "$out" "deploys no Cloud Run job for acme/sandbox" "no jobs: what was read"
}

# A node that cannot be read stops the run before any criterion is: without it
# there is no map to hold a provider key to.
test_a_node_that_cannot_be_read_stops_the_run() {
  local dir=$work/nodeunreadable
  prepare "$dir"
  run940 "$dir" node-unreadable
  expect_status "$status" 1 "unreadable node: the verdict"
  expect_contains "$err" "stopped:" "unreadable node: the stop is said"
  expect_contains "$out" "FACT node: FAIL — not reached" "unreadable node: no row claims a state"
  expect_contains "$out" "FACT aborted: FAIL" "unreadable node: the abort is recorded"
}

# The region is what doctor reported: a listing with no --region, or one pinned
# at another region, reads a region the installation does not deploy to.
test_the_jobs_list_is_read_at_the_installations_region() {
  local dir=$work/region
  prepare "$dir"
  run940 "$dir" ok
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "--project=fugaro-dev" "region: the project doctor named"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "--region=us-east5" "region: the region doctor named"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "doctor --json --project fugaro" "region: the installation read"
}

# --project names the installation, and its default is `fugaro`.
test_the_project_flag_names_the_installation() {
  local dir=$work/projectflag
  prepare "$dir"
  run_args=(--project other)
  run940 "$dir" ok
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "project policy show --json --project other" "project flag: the node read"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "image survey --json --project other" "project flag: the survey read"
}

# belong is never touched, and neither is another project's node.
test_a_guard_stops_on_belong() {
  local dir=$work/belong
  prepare "$dir"
  run940 "$dir" belong
  expect_status "$status" 1 "belong: the verdict"
  expect_contains "$err" "names belong" "belong: the refusal"
  if [ -s "$dir/logs/gcloud.calls" ]; then fail "belong: gcloud was called"; fi
}

# A shell started with tracing on would print every command — the access token
# and all — into the log the block is read from: it is refused.
test_xtrace_is_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  xtrace_runner=yes
  run940 "$dir" ok
  expect_status "$status" 1 "xtrace: the verdict"
  expect_contains "$out" "FACT xtrace: FAIL" "xtrace: the row"
  expect_contains "$err" "tracing" "xtrace: the reason"
  if [ -s "$dir/logs/gcloud.calls" ]; then fail "xtrace: gcloud was called"; fi
}

test_keep_scratch_keeps_the_directory() {
  local dir=$work/keep
  prepare "$dir"
  run_env=(CHECK940_KEEP_SCRATCH=1)
  run940 "$dir" ok
  expect_status "$status" 0 "keep scratch: the verdict"
  expect_contains "$out" "FACT scratch-removed: PASS" "keep scratch: the row"
  expect_contains "$out" "kept on request" "keep scratch: it says it was kept"
  if [ ! -d "$scratch" ]; then fail "keep scratch: the directory was removed anyway"; fi
  rm -rf "$scratch"
}

# The knob CHECK940_PROJECT is read with a default, and the flag wins where both
# are given.
test_the_project_knob_is_read() {
  local dir=$work/knob
  prepare "$dir"
  run_env=(CHECK940_PROJECT=fromknob)
  run940 "$dir" ok
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "doctor --json --project fromknob" "project knob: the installation read"
}

test_nothing_secret_shaped_reaches_the_block() {
  local dir=$work/plain
  prepare "$dir"
  run940 "$dir" ok
  expect_absent "$out" "Authorization" "block: no authorization header"
  expect_absent "$out" "access_token" "block: no token"
  expect_absent "$out" "Bearer" "block: no bearer value"
  expect_contains "$out" "RESULT END (exit" "block: it closed"
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i (each test works in its own directory under
# $work), CHECK_TEST_ONLY=name runs that one test alone. Neither set: all run.
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
t test_happy_path
t test_the_node_that_still_holds_image_map_fails
t test_a_node_that_holds_both_members_fails
t test_a_node_with_no_map_at_all_fails
t test_the_survey_at_version_1_fails
t test_a_provider_the_map_does_not_name_is_skipped
t test_a_job_running_another_entry_fails
t test_a_jobs_list_that_cannot_be_read_is_unmeasured
t test_a_repository_with_no_job_fails
t test_a_node_that_cannot_be_read_stops_the_run
t test_the_jobs_list_is_read_at_the_installations_region
t test_the_project_flag_names_the_installation
t test_a_guard_stops_on_belong
t test_xtrace_is_refused
t test_keep_scratch_keeps_the_directory
t test_the_project_knob_is_read
t test_nothing_secret_shaped_reaches_the_block

if [ "$failures" -ne 0 ]; then
  echo "check940_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check940_test.sh: ok"
