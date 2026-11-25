#!/usr/bin/env bash
# Tests for scripts/live-checks/check181.sh, against fake tools on PATH: nothing
# here reaches the network, no real credential exists, no Fugaro installation is
# read, no run is launched, and the token the test feeds in is a sentinel
# asserted absent from everything the run prints, leaves behind or puts on a
# command line.
#
# What the fakes are: `fugaro` (doctor, secrets allow/set, project profile
# ls/add/set, project policy publish, update, run, runs ls), `gcloud` (the two
# Cloud Run job reads) and `git` (the scratch directory is not a work tree) —
# each a few lines of bash that answer from FAKE181_MODE, keep the profile's own
# state in FAKE181_STATE and write what they were asked to FAKE181_LOG. The fake
# `fugaro` is a small model of the two rules this check reads: `project profile
# set` refuses a repository the token's block does not admit on a profile that
# MOUNTS the token, and `project policy publish` and `update` refuse a profile
# that mounts it at all — so the check's four readings are exercised against a
# CLI that answers the way the real one does, and each mode is one way it could
# answer instead.
#
# Usage: bash scripts/live-checks/check181_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check181.sh
# The PHYSICAL path, so a child's own getcwd() and the path this test uses are
# spelled the same way (macOS: /var is a symlink to /private/var).
work=$(cd "$(mktemp -d)" && pwd -P)
base_path=$PATH
cleanup_all() {
  if [ -n "${CHECK181_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
}
trap cleanup_all EXIT

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}
# rows_of_block TEXT — the block's own rows, one short line each, so a failure
# names what the run recorded instead of pasting the whole block into the log.
rows_of_block() {
  printf '%s\n' "$1" | grep '^FACT ' | grep -v 'not reached' | cut -c1-90 | tr '\n' '|' | head -c 900
}

expect_contains() { # haystack needle what
  case "$1" in
  *"$2"*) ;;
  *) fail "$3: expected to contain '$2'; the rows read: $(rows_of_block "$1")" ;;
  esac
}
expect_absent() {
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1; the rows read: $(rows_of_block "${out:-}")"
}
# expect_last_line OUT WANT WHAT — the block's own last line, exactly. It is the
# line a reader takes as the answer, and the one the exit status has to agree
# with: a block whose last line is a verdict but whose status is another is two
# stories about one run, which is the whole point of the verdict being written
# from the status rather than beside it.
expect_last_line() { # out want what
  local last
  last=$(printf '%s\n' "$1" | grep -v '^[[:space:]]*$' | tail -n 1)
  [ "$last" = "$2" ] || fail "$3: the block's last line reads '$last', want '$2'"
}

# The sentinel is deliberately not a credential shape: nothing here is a real
# token, and the assertions below hold that it never survives a run.
TOKEN_SENTINEL='FAKE-PROJECT-TOKEN-FOR-CHECK-181-TESTS-NOT-A-CREDENTIAL'

# What the fake installation's own doctor reports, and the ONE job the deploy is
# supposed to leave. The job's name carries a hash suffix, so the check has to
# find the job by prefix or by the labels the deployer writes rather than
# compose it — and the fake `fugaro` reads the same name out of the run's state,
# so a run whose execution path names another job is a different job's row.
GCP_PROJECT_NAME='sandbox-p'
JOB_NAME='fugaro-p-bitbucket-shared-bitbucket-2a1f9c'
OTHER_JOB_NAME='fugaro-p-other-profile-bitbucket-0000'

# The repository the check is pointed at as the one the token's block does NOT
# name, and the two it is allow-listed for.
THIRD_REPO_NAME='dimi_bmb/third'

# The markers the check refuses a session for: the test clears all of them for
# every run it makes, so a test run inside a coding agent's own session measures
# the check rather than the session it happens to be in.
unmark=(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT)

# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(installation agent-session belong workspace xtrace config token allow-list store
  profile subset-refusal mount-publish mount-deploy open-publish deploy job
  run-a run-b run-third restore)

# --------------------------------------------------------------------- the fake
write_fakes() { # bin — the directory the three fakes are written into and put on PATH
  local dir=$1
  cat >"$dir/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake `fugaro`, for check181.sh's test only: it answers the calls the check
# makes and records what it was asked. It NEVER prints a token: the store's
# value arrives on stdin, is measured for its length and goes to /dev/null, so
# nothing it does can put one in the log or in the block.
set -u
mode=${FAKE181_MODE:-ok}
log=${FAKE181_LOG:-/dev/null}
state=${FAKE181_STATE:-/tmp}
# The argv as the check ran it, and — when the check aimed the call at a project
# with FUGARO_PROJECT — that too, because `fugaro project profile` and `fugaro
# secrets allow` edit the config the machine selects and have no flag to name
# one. A call with no aim logs its argv alone, so the store's line below is
# pinned exactly.
printf '%s%s\n' "$*" "${FUGARO_PROJECT:+ FUGARO_PROJECT=$FUGARO_PROJECT}" >>"$log"

state_get() { cat "$state/$1" 2>/dev/null || true; }
state_put() { printf '%s' "$2" >"$state/$1"; }
slug_of() { printf '%s' "$1" | tr '/' '_'; }
json_list() { # json_list a b — the CSV as a JSON array of strings
  local out="" item
  for item in $(printf '%s' "$1" | tr ',' ' '); do
    [ -n "$out" ] && out="$out,"
    out="$out\"$item\""
  done
  printf '[%s]' "$out"
}

profile_name=$(state_get profile-name)
repo_a=$(state_get repo-a)
repo_b=$(state_get repo-b)
third=$(state_get third-repo)
job_name=$(state_get job-name)
secrets=$(state_get profile-secrets)
repos=$(state_get profile-repos)

case "${1:-}" in
doctor)
  [ "$mode" = doctor-fails ] && { echo "doctor: no usable credentials on this machine" >&2; exit 2; }
  name=sandbox
  [ "$mode" = doctor-wrong-name ] && name=otherproject
  printf '{"ok":true,"project":{"name":"%s","gcp_project":"%s","region":"us-east5"}}\n' \
    "$name" "$(state_get gcp-project)"
  # exits 1 with its document, as the real doctor does when a check is a warning
  [ "$mode" = doctor-warns ] && exit 1
  exit 0
  ;;
secrets)
  case "${2:-}" in
  allow)
    [ "$mode" = allow-refused ] && { echo "secrets allow: bitbucket-token has no project-wide meaning in this config" >&2; exit 1; }
    echo "allowed bitbucket-token for the named repositories, with the proxy on"
    exit 0
    ;;
  set)
    # The token's value is on stdin and nowhere else. It is measured, never
    # printed, and never written to a file — the two detector modes below are
    # the only ones that break that, and they exist to prove the test's
    # assertions can see a leak at all.
    bytes=$(wc -c | tr -d ' ')
    printf 'store-bytes=%s\n' "$bytes" >>"$log"
    case "$mode" in
    store-echoes)
      # The leak a command could really make: the value in its own last line.
      # It refuses as well, so the line is the one the check reads back into the
      # block — a leak a step never reads is not one this test can catch.
      printf 'stored %s\n' "$(cat "$state/token-echo" 2>/dev/null || echo '')" >&2
      exit 1
      ;;
    store-leaks-file) printf 'the value I was handed: %s\n' "$(cat "$state/token-echo" 2>/dev/null || echo '')" >"$state/leak" ;;
    esac
    [ "$mode" = store-refused ] && { echo "secrets set: bitbucket-token could not be stored in this installation" >&2; exit 1; }
    echo "stored bitbucket-token project-wide"
    exit 0
    ;;
  esac
  echo "fake fugaro: unknown secrets subcommand ${2:-}" >&2
  exit 2
  ;;
project)
  case "${2:-}" in
  profile)
    case "${3:-}" in
    ls)
      if [ "$(state_get profile-defined)" = yes ]; then
        printf '{"path":"%s","profiles":[{"name":"%s","provider":"bitbucket","cpu":2,"memory":"4Gi","timeout_class":"6h","secrets":%s,"allow_repos":%s,"claude_accounts":[],"jobs":""}]}\n' \
          "$(state_get config-path)" "$profile_name" "$(json_list "$secrets")" "$(json_list "$repos")"
      else
        printf '{"path":"%s","profiles":[]}\n' "$(state_get config-path)"
      fi
      exit 0
      ;;
    add)
      [ "$mode" = profile-add-refused ] && { echo "profile add: a config that carries profiles: must define standard as well" >&2; exit 1; }
      state_put profile-defined yes
      state_put profile-secrets ""
      state_put profile-repos ""
      echo "added profile ${4:-}"
      exit 0
      ;;
    set)
      # set NAME KEY VALUE — the door the subset rule owns. On a profile that
      # MOUNTS bitbucket-token, a repository the token's block does not admit is
      # refused (projectpolicy.CheckBitbucketProfileRepos); on one that mounts
      # nothing there is no ceiling and the edit is accepted.
      key=${5:-}
      value=${6:-}
      case "$key" in
      secrets)
        if [ -n "$value" ] && [ "$mode" = profile-door-refuses-mount ]; then
          echo "profiles[0].secrets[0]: \"$value\" is the Bitbucket credential, and a shared project job holds no credential of the provider it serves" >&2
          exit 1
        fi
        state_put profile-secrets "$value"
        echo "set ${4:-} secrets to '${value}'"
        exit 0
        ;;
      allow_repos)
        case ",$secrets," in
        *,bitbucket-token,*)
          case "$value" in
          *"$third"*)
            case "$mode" in
            subset-accepted)
              state_put profile-repos "$value"
              echo "set ${4:-} allow_repos"
              exit 0
              ;;
            subset-silent)
              echo "profiles[0].allow_repos: this list is refused" >&2
              exit 1
              ;;
            *)
              echo "profiles[0].allow_repos: the profile mounts bitbucket-token, whose allow_data_to does not admit $third, and a profile may only narrow what the token admits" >&2
              exit 1
              ;;
            esac
            ;;
          esac
          ;;
        *)
          # On a profile that mounts nothing there is no ceiling, so this edit is
          # one of the two the D2302 shape allows — unless the repository named
          # is the third one and the mode says this door refused it, which is the
          # reading the check exists to catch.
          case "$value" in
          *"$third"*)
            if [ "$mode" = open-refused ]; then
              echo "profiles[0].allow_repos: this list is refused" >&2
              exit 1
            fi
            ;;
          esac
          ;;
        esac
        state_put profile-repos "$value"
        echo "set ${4:-} allow_repos"
        exit 0
        ;;
      esac
      echo "fake fugaro: unknown profile key $key" >&2
      exit 2
      ;;
    esac
    ;;
  policy)
    # publish — the operator's door on the shared-job rule.
    case ",$secrets," in
    *,bitbucket-token,*)
      [ "$mode" = publish-mount-ok ] && { echo "published the project policy"; exit 0; }
      echo "fugaro.yaml: profiles[0].secrets[0]: \"bitbucket-token\" is the Bitbucket credential, and a shared project job holds no credential of the provider it serves: take the entry off the profile, or keep this profile's repositories on per-repository jobs" >&2
      exit 1
      ;;
    esac
    [ "$mode" = open-publish-refused ] && { echo "project policy publish: the node is refused" >&2; exit 1; }
    echo "published the project policy for ${profile_name}"
    exit 0
    ;;
  esac
  echo "fake fugaro: unknown project subcommand ${2:-}" >&2
  exit 2
  ;;
update)
  [ "$mode" = update-fails ] && { echo "update: the apply failed: Error 403: the caller does not have permission" >&2; exit 1; }
  case ",$secrets," in
  *,bitbucket-token,*)
    [ "$mode" = update-mount-ok ] && { echo "update: converged"; exit 0; }
    echo "the deployer refused the job for profile \"$profile_name\": it mounts bitbucket-token, and a shared project job holds no credential of the provider it serves" >&2
    exit 1
    ;;
  esac
  echo "update: converged"
  exit 0
  ;;
run)
  # Only the FIRST launch fails in this mode: a check whose first run never
  # launched still has two other runs to read, and a fake that failed all three
  # would leave the third run's criterion unmeasured for a reason that is not
  # this reading.
  [ "$mode" = run-fails ] && [ -z "$(state_get launches)" ] \
    && { echo "run: the execution ended before finalizing" >&2; exit 1; }
  repo=""
  prev=""
  for a in "$@"; do
    [ "$prev" = "--repo" ] && repo=$a
    prev=$a
  done
  n=$(state_get launches)
  n=$((n + 1))
  state_put launches "$n"
  id=$(printf 'run-%04d' "$n")
  state_put "last-run-$(slug_of "$repo")" "$id"
  state_put "polls-$(slug_of "$repo")" 0
  printf '{"run_id":"%s","status":"launched","repo":"%s"}\n' "$id" "$repo"
  exit 0
  ;;
runs)
  # The listing the check polls. Two things about it are the regressions this
  # fake exists to catch: the FIRST poll after a launch answers an in-flight row
  # for the run just launched (status running, terminal=false, nothing pushed),
  # which is what `fugaro runs ls` really says while a run is going — and the
  # row for the launched run is never first in the list, an older unrelated run
  # is. So a check that read the listing once would read an in-flight row, and
  # one that read rows[0] or asked for --limit 1 would read another run's row or
  # no row at all, instead of picking this run's row out by run_id.
  repo=""
  prev=""
  for a in "$@"; do
    [ "$prev" = "--repo" ] && repo=$a
    prev=$a
  done
  slug=$(slug_of "$repo")
  ours=$(state_get "last-run-$slug")
  n=$(state_get "polls-$slug")
  n=$((n + 1))
  state_put "polls-$slug" "$n"
  unrelated='{"run_id":"run-9000","status":"running","terminal":false,"settled":false,"pushed":false,"pr":0}'
  if [ -z "$ours" ]; then
    printf '{"project":"sandbox","runs":[%s]}\n' "$unrelated"
    exit 0
  fi
  exec_path="projects/$(state_get gcp-project)/locations/us-east5/jobs/$job_name/executions/$ours-7bd2"
  if [ "$repo" = "$third" ]; then
    if [ "$mode" = third-norow ]; then
      printf '{"project":"sandbox","runs":[%s]}\n' "$unrelated"
      exit 0
    fi
    if [ "$n" -le 1 ]; then
      printf '{"project":"sandbox","runs":[%s,{"run_id":"%s","status":"running","terminal":false,"settled":false,"pushed":false,"pr":0,"execution":"%s"}]}\n' \
        "$unrelated" "$ours" "$exec_path"
      exit 0
    fi
    pushed=false
    pr=0
    halt='"halt":{"reason":"not_admitted","scope":"run","detail":"the repository '"$third"' is not admitted by bitbucket-token allow_data_to: fugaro secrets allow bitbucket-token '"$third"'"}'
    case "$mode" in
    third-pushes)
      pushed=true
      pr=9
      halt=""
      ;;
    third-silent)
      halt='"halt":{"reason":"not_admitted","scope":"run","detail":"the git operation was refused before a credential was issued"}' ;;
    esac
    if [ -n "$halt" ]; then
      printf '{"project":"sandbox","runs":[%s,{"run_id":"%s","status":"failed","terminal":true,"settled":true,"pushed":%s,"pr":%d,"execution":"%s",%s}]}\n' \
        "$unrelated" "$ours" "$pushed" "$pr" "$exec_path" "$halt"
    else
      printf '{"project":"sandbox","runs":[%s,{"run_id":"%s","status":"succeeded","terminal":true,"settled":true,"pushed":%s,"pr":%d,"execution":"%s"}]}\n' \
        "$unrelated" "$ours" "$pushed" "$pr" "$exec_path"
    fi
    exit 0
  fi
  if [ "$n" -le 1 ]; then
    printf '{"project":"sandbox","runs":[%s,{"run_id":"%s","status":"running","terminal":false,"settled":false,"pushed":false,"pr":0,"execution":"%s"}]}\n' \
      "$unrelated" "$ours" "$exec_path"
    exit 0
  fi
  pushed=true
  pr=7
  [ "$mode" = no-push ] && { pushed=false; pr=0; }
  case "$mode" in
  run-other-job) exec_path="projects/$(state_get gcp-project)/locations/us-east5/jobs/$(state_get other-job-name)/executions/$ours-0000" ;;
  esac
  printf '{"project":"sandbox","runs":[%s,{"run_id":"%s","status":"succeeded","terminal":true,"settled":true,"pushed":%s,"pr":%d,"execution":"%s","base_branch":"fugaro/check181-run"}]}\n' \
    "$unrelated" "$ours" "$pushed" "$pr" "$exec_path"
  exit 0
  ;;
esac
echo "fake fugaro: unknown subcommand ${1:-}" >&2
exit 2
FUGARO_EOF
  chmod +x "$dir/fugaro"

  cat >"$dir/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake `gcloud`, for check181.sh's test only: the two Cloud Run job reads. It
# answers the way gcloud does — `run jobs list` an ARRAY of job documents,
# `run jobs describe <name>` one — with the container spec at
# spec.template.spec.template.spec.containers, which is where the real command
# puts it, so a reader looking anywhere else reads no environment at all. A read
# of another project or region is refused the way gcloud answers nothing for it.
set -u
state=${FAKE181_STATE:-/tmp}
mode=${FAKE181_MODE:-ok}
project_want=$(cat "$state/gcp-project" 2>/dev/null || echo "")
job_name=$(cat "$state/job-name" 2>/dev/null || echo "")
profile=$(cat "$state/profile-name" 2>/dev/null || echo "")
project=""
region=""
shift 2 # `run jobs`
verb=${1:-}
shift || true
name=""
if [ "$verb" = describe ]; then
  name=${1:-}
  shift || true
fi
while [ $# -gt 0 ]; do
  case "$1" in
  --project=*) project=${1#--project=} ;;
  --region=*) region=${1#--region=} ;;
  --project) project=${2:-}; shift ;;
  --region) region=${2:-}; shift ;;
  esac
  shift
done
if [ -n "$project_want" ] && [ "${project:-}" != "$project_want" ]; then
  echo "fake gcloud: this read names --project '${project:-none}', and the installation is in '$project_want'" >&2
  exit 1
fi
[ "$region" = us-east5 ] || { echo "fake gcloud: this read names --region '${region:-none}', and the installation is in us-east5" >&2; exit 1; }

envs='"env":[{"name":"FUGARO_CREDENTIALS_SERVICE_URL","value":"https://creds-sandbox.invalid"}'
[ "$mode" = job-no-service ] && envs='"env":['
if [ "$mode" = job-has-token ]; then
  envs="$envs"',{"name":"FUGARO_BITBUCKET_TOKEN","valueSource":{"secretKeyRef":{"name":"fugaro-project-sandbox-bitbucket-token","key":"latest"}}}'
fi
envs="$envs]"
job_doc() { # job_doc NAME
  printf '{"metadata":{"name":"%s","labels":{"fugaro_profile":"%s","fugaro_provider":"bitbucket"}},"spec":{"template":{"spec":{"template":{"spec":{"containers":[{%s}]}}}}}}\n' \
    "$1" "$profile" "$envs"
}
case "$verb" in
list)
  [ "$mode" = jobs-unreadable ] && { echo "fake gcloud: PERMISSION_DENIED: the caller does not have run.jobs.list" >&2; exit 1; }
  [ "$mode" = no-job ] && { printf '[]\n'; exit 0; }
  printf '['
  job_doc "$job_name" | tr -d '\n'
  printf ']\n'
  exit 0
  ;;
describe)
  [ "$mode" = job-describe-unreadable ] && { echo "fake gcloud: PERMISSION_DENIED: the caller does not have run.jobs.get" >&2; exit 1; }
  job_doc "$name"
  exit 0
  ;;
esac
echo "fake gcloud: unknown run jobs subcommand $verb" >&2
exit 2
GCLOUD_EOF
  chmod +x "$dir/gcloud"

  cat >"$dir/git" <<'GIT_EOF'
#!/usr/bin/env bash
# A fake `git`, for check181.sh's test only: the scratch directory has to be
# shown NOT to be a work tree, and this answers the one question the check asks
# it. Nothing else in the check runs git.
case "$*" in
*rev-parse*) exit 128 ;;
esac
exit 0
GIT_EOF
  chmod +x "$dir/git"
}

# ---------------------------------------------------------------- running it
write_token() { # write_token FILE [value]
  printf '%s\n' "${2:-$TOKEN_SENTINEL}" >"$1"
  chmod 600 "$1"
}

# seed_state DIR — the fake tools' own state: the installation's GCP project,
# the ONE job the deploy leaves, the profile and its three repositories, and the
# counter every launch bumps. Every run of this test seeds it, so a fake that
# answers from a state file nothing wrote is a fake answering nothing.
seed_state() { # dir
  local dir=$1
  mkdir -p "$dir/state"
  printf '%s\n' "$GCP_PROJECT_NAME" >"$dir/state/gcp-project"
  printf '%s\n' "$JOB_NAME" >"$dir/state/job-name"
  printf '%s\n' "$OTHER_JOB_NAME" >"$dir/state/other-job-name"
  printf '%s\n' "${RUN181_PROFILE:-bitbucket-shared}" >"$dir/state/profile-name"
  printf '%s\n' "${RUN181_NAME_A:-dimi_bmb/one}" >"$dir/state/repo-a"
  printf '%s\n' "${RUN181_NAME_B:-dimi_bmb/two}" >"$dir/state/repo-b"
  printf '%s\n' "${RUN181_THIRD:-dimi_bmb/third}" >"$dir/state/third-repo"
  # The project config file the fake's `project profile ls --json` names, which
  # is the one check181.sh insists on before it writes anything: it has to be the
  # installation's own, and RUN181_CONFIG_PATH replaces it so a test can point
  # the reading at another project's.
  printf '%s\n' "${RUN181_CONFIG_PATH:-/sandbox/.config/fugaro/projects/sandbox.yaml}" >"$dir/state/config-path"
  printf '%s\n' "${RUN181_PROFILE_DEFINED:-no}" >"$dir/state/profile-defined"
  # A profile an earlier shape left mounting the credential: the check's own
  # reading is what takes the entry off, which is a case of its own.
  printf '%s\n' "${RUN181_PROFILE_SECRETS:-}" >"$dir/state/profile-secrets"
  : >"$dir/state/profile-repos"
  : >"$dir/state/launches"
  # The two detector modes echo back the value they were handed on stdin. The
  # fake reads it from here because it never keeps what it read: a fake that
  # stored the value to leak it later would be a fake that holds a credential.
  printf '%s' "$TOKEN_SENTINEL" >"$dir/state/token-echo"
}

# run181 DIR — runs the check once against the fakes. RUN181_ARGS appends
# arguments, RUN181_MODE picks the fake fugaro's behaviour, RUN181_NAME_A /
# RUN181_NAME_B / RUN181_THIRD replace the repository names the check is pointed
# at, RUN181_WORKSPACE replaces the workspace, and RUN181_ENV names environment
# variables (NAME=value) to set, so a test can add a bad override. Sets $status,
# $out and $err.
run181() {
  local dir=$work/$1
  mkdir -p "$dir/tmp" "$dir/bin"
  seed_state "$dir"
  write_token "$dir/token"
  write_fakes "$dir/bin"
  status=0
  out=""
  "${unmark[@]}" PATH="$dir/bin:$PATH" TMPDIR="$dir/tmp" \
    FAKE181_MODE="${RUN181_MODE:-ok}" FAKE181_STATE="$dir/state" FAKE181_LOG="$dir/fugaro.log" \
    CHECK181_CLI_SECONDS=20 CHECK181_UPDATE_SECONDS=30 \
    CHECK181_RUN_SECONDS=30 CHECK181_RUN_POLL_SECONDS=1 CHECK181_RUN_POLL_MAX=4 \
    ${RUN181_ENV[@]+"${RUN181_ENV[@]}"} \
    bash "$script" \
    --installation sandbox --token-file "$dir/token" \
    --repo "${RUN181_NAME_A:-dimi_bmb/one}" \
    --repo "${RUN181_NAME_B:-dimi_bmb/two}" \
    --third-repo "${RUN181_THIRD:-dimi_bmb/third}" \
    ${RUN181_WORKSPACE:+--workspace "$RUN181_WORKSPACE"} \
    ${RUN181_ARGS[@]+"${RUN181_ARGS[@]}"} \
    >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  RUN181_ARGS=()
  RUN181_ENV=()
  RUN181_MODE=ok
  RUN181_NAME_A=""
  RUN181_NAME_B=""
  RUN181_THIRD=""
  RUN181_WORKSPACE=""
  RUN181_PROFILE_DEFINED=""
  RUN181_PROFILE_SECRETS=""
  RUN181_CONFIG_PATH=""
}

# assert_no_secret DIR WHAT — nothing the run printed, no file it left behind
# and no command line it ran carries the token. The one file this TEST writes
# with the sentinel in it on purpose is its own fixture, and it is the only one
# it may be in.
assert_no_secret() {
  local dir=$1 what=$2 hits
  expect_absent "$(cat "$dir/out" "$dir/err" 2>/dev/null || true)" "$TOKEN_SENTINEL" "$what: the token in the output"
  expect_absent "$(cat "$dir/fugaro.log" 2>/dev/null || true)" "$TOKEN_SENTINEL" "$what: the token on a command line (the fake fugaro logs its own argv)"
  hits=$(grep -rl -- "$TOKEN_SENTINEL" "$dir" 2>/dev/null \
    | grep -v -F -x -e "$dir/token" -e "$dir/state/token-echo" || true)
  [ -z "$hits" ] || fail "$what: the token is in $hits"
}

expect_block_keys() { # out what
  local key
  expect_contains "$1" "CHECK 181 RESULT" "$2: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$1" "FACT $key:" "$2: the $key row"
  done
}

scratch_of() { # dir — the scratch directory the run announced
  sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$1/err" | head -n 1
}

# expect_store_argv DIR WHAT — the store's own call, pinned to the value write
# the register documents. The fake logs its argv, so this reads what the check
# actually ran rather than a string it printed: the value goes in on stdin, so
# the command line names the credential and never carries it.
expect_store_argv() {
  local dir=$1 what=$2 lines bad
  lines=$(grep -c '^secrets set bitbucket-token' "$dir/fugaro.log" 2>/dev/null || true)
  if [ "${lines:-0}" -lt 1 ]; then
    fail "$what: no 'secrets set bitbucket-token' call was made at all"
    return 0
  fi
  bad=$(grep '^secrets set bitbucket-token' "$dir/fugaro.log" \
    | grep -v -x -F 'secrets set bitbucket-token --project-wide --project sandbox' | tr '\n' ';' || true)
  [ -z "$bad" ] || fail "$what: a store call reads '$bad', want exactly 'secrets set bitbucket-token --project-wide --project sandbox': the value goes in on stdin, so the command line carries the name and never the value"
}

# expect_store_saw_the_token DIR WHAT — the store really was handed the token.
# The fake measures its stdin and logs the byte count and nothing else, so this
# is the reading of "the token reached the one command that is given it": a
# check whose store read an empty stdin would leave the whole no-secret
# assertion above true for free.
expect_store_saw_the_token() {
  local dir=$1 what=$2 want got
  want=$(( ${#TOKEN_SENTINEL} + 1 ))
  got=$(sed -n 's/^store-bytes=//p' "$dir/fugaro.log" | tail -n 1)
  [ "$got" = "$want" ] \
    || fail "$what: the store was handed ${got:-nothing} byte(s) on stdin, want $want (the token file's one line): the value has to reach the one command that stores it, or 'no secret anywhere' is a claim about nothing"
}

# expect_runs_polled DIR WHAT LAUNCHES — how the check read each run's row.
# `fugaro run` returns at launch (the fake's own answer says status "launched"),
# so the listing has to be polled until that run reached a final status: one
# read per run would be an in-flight row (terminal=false, nothing pushed), which
# is what the fake answers first on purpose. And the read has to be the window's
# own listing with the row picked out by run_id: --limit would leave the row out
# of the listing altogether, and the fake keeps an unrelated, newer run first in
# it so a positional read is another run's row.
expect_runs_polled() {
  local dir=$1 what=$2 launches=$3 calls bad
  calls=$(grep -c '^runs ls' "$dir/fugaro.log" 2>/dev/null || true)
  if [ "${calls:-0}" -lt $((launches * 2)) ]; then
    fail "$what: the check made ${calls:-0} 'runs ls' call(s) for ${launches} launch(es), fewer than the two per run a check that waits for the run to finish has to make: fugaro run returns at launch, and the first poll answers an in-flight row"
  fi
  bad=$(grep '^runs ls' "$dir/fugaro.log" | grep -e '-limit' -e '-after' | tr '\n' ';' || true)
  [ -z "$bad" ] || fail "$what: a 'runs ls' call reads '$bad': the row of the run being waited for has to be in the listing, so the check reads the window's own and picks the row out by run_id"
}

# --------------------------------------------------------------- the tests
# The all-PASS run: EVERY criterion is measured, which is what makes the block's
# last line PASS and the exit status 0. One criterion fewer and the run is
# INCOMPLETE (test_unmeasured_criteria_are_not_a_pass below).
test_happy_path() {
  run181 happy
  expect_status "$status" 0 "happy path"
  expect_block_keys "$out" "happy path"
  expect_last_line "$out" "CHECK 181 RESULT: PASS (exit 0)" "happy path: the block's last line is the verdict"
  expect_contains "$out" "FACT installation: PASS — " "happy path: the installation"
  expect_contains "$out" "sandbox-p" "happy path: the GCP project it read"
  expect_contains "$out" "FACT agent-session: PASS — " "happy path: the agent-session guard"
  expect_contains "$out" "FACT belong: PASS — " "happy path: the belong guard"
  expect_contains "$out" "FACT workspace: PASS — " "happy path: the workspace guard"
  expect_contains "$out" "FACT xtrace: PASS — " "happy path: the xtrace guard"
  expect_contains "$out" "FACT token: PASS — " "happy path: the token file"
  expect_contains "$out" "FACT allow-list: PASS — " "happy path: the allow-list and the proxy switch"
  expect_contains "$out" "FACT store: PASS — " "happy path: the store"
  expect_contains "$out" "FACT profile: PASS — " "happy path: the profile, defined by the check"
  expect_contains "$out" "FACT subset-refusal: PASS — " "happy path: the subset rule refusing the third repository"
  expect_contains "$out" "FACT mount-publish: PASS — " "happy path: the publish refusing the mount"
  expect_contains "$out" "FACT mount-deploy: PASS — " "happy path: the deployer refusing the mount"
  expect_contains "$out" "FACT open-publish: PASS — " "happy path: the tokenless profile accepting the third repository"
  expect_contains "$out" "FACT deploy: PASS — " "happy path: the converge"
  expect_contains "$out" "FACT job: PASS — " "happy path: the one job, service present and token absent"
  expect_contains "$out" "$JOB_NAME" "happy path: the job it read"
  expect_contains "$out" "FACT run-a: PASS — " "happy path: the first run"
  expect_contains "$out" "FACT run-b: PASS — " "happy path: the second run"
  expect_contains "$out" "FACT run-third: PASS — " "happy path: the third repository refused, in words that name it"
  expect_contains "$out" "FACT restore: PASS — " "happy path: the cleanup"
  expect_store_argv "$work/happy" "happy path"
  expect_store_saw_the_token "$work/happy" "happy path"
  expect_runs_polled "$work/happy" "happy path" 3
  assert_no_secret "$work/happy" "happy path"
  # The scratch directory is gone, and the run said so.
  local scratch
  scratch=$(scratch_of "$work/happy")
  [ -n "$scratch" ] || fail "happy path: the run announced no scratch directory"
  [ ! -e "$scratch" ] || fail "happy path: the scratch directory $scratch is still there"
}

# A profile the config already defines is read rather than added, and one that
# mounts the credential from an earlier shape has the entry taken off it: the
# check reads the profile it deploys the shared job for, and a shared job's
# profile holds no credential of the provider it serves.
test_an_existing_profile_is_read_and_its_mount_taken_off() {
  RUN181_PROFILE_DEFINED=yes
  RUN181_PROFILE_SECRETS=bitbucket-token
  run181 existing
  expect_status "$status" 0 "an existing profile that mounts the credential"
  expect_contains "$out" "FACT profile: PASS — " "an existing profile: the profile was read"
  expect_contains "$out" "mounts nothing" "an existing profile: the entry was taken off it"
  expect_absent "$(cat "$work/existing/fugaro.log")" "profile add" "an existing profile: a defined profile is never added"
  expect_contains "$out" "FACT subset-refusal: PASS — " "an existing profile: the subset rule was still asked"
  expect_last_line "$out" "CHECK 181 RESULT: PASS (exit 0)" "an existing profile: the verdict"
}

# A criterion the run could not settle is NOT a pass: the block's last line says
# INCOMPLETE and the status is 3. This is the one reading the register's section
# warns about, and the one that must never be reported as a pass.
test_unmeasured_criteria_are_not_a_pass() {
  # The profile door refuses the credential entry, so the subset rule is never
  # asked: its row reads UNVERIFIED and the run is INCOMPLETE.
  RUN181_MODE=profile-door-refuses-mount
  run181 unmeasured
  expect_status "$status" 3 "unmeasured criterion"
  expect_contains "$out" "FACT subset-refusal: UNVERIFIED — " "unmeasured criterion: the row is not a pass"
  expect_last_line "$out" "CHECK 181 RESULT: INCOMPLETE (exit 3)" "unmeasured criterion: the verdict is INCOMPLETE"
  assert_no_secret "$work/unmeasured" "unmeasured criterion"
}

# A criterion that was measured and failed is a FAIL, and FAIL is exit 1.
test_a_failed_criterion_is_a_fail() {
  RUN181_MODE=subset-accepted
  run181 failed
  expect_status "$status" 1 "a failed criterion"
  expect_contains "$out" "FACT subset-refusal: FAIL — " "a failed criterion: the row is a FAIL"
  expect_contains "$out" "$THIRD_REPO_NAME" "a failed criterion: the row names the repository the rule did not stop"
  expect_last_line "$out" "CHECK 181 RESULT: FAIL (exit 1)" "a failed criterion: the verdict is FAIL"
}

# --dry-run prints the plan and touches NOTHING: no installation is read, no
# policy is published, no secret is stored, no run is launched.
test_dry_run_touches_nothing() {
  RUN181_ARGS=(--dry-run)
  run181 dryrun
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "This is the PLAN" "dry run: the plan"
  expect_contains "$out" "dimi_bmb" "dry run: the workspace it defaults to"
  expect_contains "$out" "bitbucket-shared" "dry run: the profile it defaults to"
  expect_absent "$out" "CHECK 181 RESULT" "dry run: no result block"
  [ ! -s "$work/dryrun/fugaro.log" ] || fail "dry run: a fugaro command was run: $(cat "$work/dryrun/fugaro.log" | tr '\n' ' ')"
  [ -z "$(scratch_of "$work/dryrun")" ] || fail "dry run: it made a scratch directory"
  assert_no_secret "$work/dryrun" "dry run"
}

# The plan and the usage are the two outputs an owner can have with no cloud
# tools at all, and refusing them for a missing `fugaro` would deny the one
# answer that needs no installation.
test_the_plan_and_the_usage_need_no_cloud_tools() {
  local status2=0 out2=""
  out2=$("${unmark[@]}" PATH="$base_path" HOME="${HOME:-}" TMPDIR="$work/notools" \
    bash "$script" --dry-run --installation sandbox --token-file "$work/token" \
    --repo dimi_bmb/one --repo dimi_bmb/two --third-repo dimi_bmb/third 2>&1) || status2=$?
  expect_status "$status2" 0 "the plan without cloud tools"
  expect_contains "$out2" "This is the PLAN" "the plan without cloud tools"
  status2=0
  out2=$("${unmark[@]}" PATH="$base_path" HOME="${HOME:-}" bash "$script" --help 2>&1) || status2=$?
  expect_status "$status2" 0 "the usage without cloud tools"
  expect_contains "$out2" "usage: check181.sh" "the usage without cloud tools"
}

# `belong` is the installation and the workspace this check must never touch. It
# is refused by name in every position it can be named, and nothing is sent
# anywhere — the guard runs before the first command.
test_belong_is_refused_everywhere() {
  local what
  for what in installation workspace repo-a repo-b third-repo; do
    local status2=0 out2=""
    case "$what" in
    installation) set -- --installation belong --token-file "$work/token" --repo dimi_bmb/one --repo dimi_bmb/two --third-repo dimi_bmb/third ;;
    workspace) set -- --installation sandbox --token-file "$work/token" --repo belong/one --repo dimi_bmb/two --third-repo dimi_bmb/third ;;
    repo-a) set -- --installation sandbox --token-file "$work/token" --repo dimi_bmb/one --repo belong/two --third-repo dimi_bmb/third ;;
    repo-b) set -- --installation sandbox --token-file "$work/token" --repo dimi_bmb/one --repo dimi_bmb/two --third-repo belong/third ;;
    third-repo) set -- --installation sandbox --token-file "$work/token" --repo belong/one --repo dimi_bmb/two --third-repo dimi_bmb/third ;;
    esac
    mkdir -p "$work/belong-$what/bin"
    write_fakes "$work/belong-$what/bin"
    out2=$("${unmark[@]}" PATH="$work/belong-$what/bin:$PATH" TMPDIR="$work/belong-$what" \
      FAKE181_STATE="$work/belong-$what/state" FAKE181_LOG="$work/belong-$what/fugaro.log" \
      bash "$script" "$@" 2>&1) || status2=$?
    expect_status "$status2" 2 "belong as the $what"
    expect_contains "$out2" "names 'belong'" "belong as the $what: the refusal"
    expect_absent "$out2" "CHECK 181 RESULT: PASS" "belong as the $what: it must never be a pass"
    [ ! -s "$work/belong-$what/fugaro.log" ] || fail "belong as the $what: a fugaro command ran before the refusal"
  done
}

# A repository outside the sandbox workspace is refused rather than measured: it
# would be a run of a repository this check was never pointed at, and the
# reading would be about another workspace's job.
test_a_repository_outside_the_workspace_is_refused() {
  local status2=0 out2=""
  out2=$("${unmark[@]}" PATH="$base_path" TMPDIR="$work" \
    bash "$script" --dry-run --installation sandbox --token-file "$work/token" \
    --repo other/one --repo dimi_bmb/two --third-repo dimi_bmb/third 2>&1) || status2=$?
  expect_status "$status2" 2 "a repository outside the workspace"
  expect_contains "$out2" "is not in the workspace 'dimi_bmb'" "a repository outside the workspace: the refusal"
  # The owner may override the workspace, and then the same repository is fine.
  status2=0
  out2=$("${unmark[@]}" PATH="$base_path" TMPDIR="$work" \
    bash "$script" --dry-run --installation sandbox --token-file "$work/token" --workspace other \
    --repo other/one --repo dimi_bmb/two --third-repo dimi_bmb/third 2>&1) || status2=$?
  expect_status "$status2" 2 "an override that leaves one repository out"
}

# The allow-list and the profile live in the project config the MACHINE selects:
# `fugaro project profile` and `fugaro secrets allow` have no flag to name a
# project, so the check aims each of them at the installation with FUGARO_PROJECT
# and reads the file back before the first write. Both halves are pinned here — a
# call with no aim edits whatever config the machine happens to select, and a
# `--project` passed to a command that has no such flag is refused outright.
test_the_config_the_check_edits_is_the_installations() {
  run181 aimed
  expect_status "$status" 0 "the aim"
  expect_contains "$out" "FACT config: PASS — " "the aim: the config row"
  expect_contains "$out" "/sandbox/.config/fugaro/projects/sandbox.yaml" "the aim: the file it read back"
  local log="$work/aimed/fugaro.log" bad
  bad=$(grep -E '^(project profile (ls|add|set)|secrets allow)' "$log" | grep -v 'FUGARO_PROJECT=sandbox' | tr '\n' ';' || true)
  [ -z "$bad" ] || fail "the aim: a call that edits the selected config ran with no FUGARO_PROJECT=sandbox: $bad"
  bad=$(grep -E '^(project profile (ls|add|set)|secrets allow)' "$log" | grep -e ' --project' -e ' --config' | tr '\n' ';' || true)
  [ -z "$bad" ] || fail "the aim: a call named the project with a flag to a command that has none, which the CLI refuses: $bad"
  expect_contains "$(cat "$log")" \
    "project profile set bitbucket-shared allow_repos bitbucket/dimi_bmb/one,bitbucket/dimi_bmb/two FUGARO_PROJECT=sandbox" \
    "the aim: the profile's list is provider-qualified, the shape the policy holds an entry to"
  assert_no_secret "$work/aimed" "the aim"
}

# The aim is PROVED rather than trusted: a checkout whose fugaro.yaml names
# another project outranks FUGARO_PROJECT (localcfg.Select), so the check reads
# the config's own path back and stops before anything is written into it. An
# allow-list granted, or a profile added, in another project's config is a change
# to an installation this check was never pointed at.
test_a_config_that_is_not_the_installations_is_refused() {
  RUN181_CONFIG_PATH=/sandbox/.config/fugaro/projects/other.yaml
  run181 other-config
  expect_status "$status" 2 "another project's config"
  expect_contains "$out" "FACT config: FAIL — not reached" "another project's config: the row was never written"
  expect_contains "$out" "other.yaml" "another project's config: the file it named"
  expect_contains "$out" "FUGARO_PROJECT=sandbox" "another project's config: the aim it named"
  expect_contains "$out" "Nothing was written" "another project's config: the refusal says so"
  local log="$work/other-config/fugaro.log"
  expect_absent "$(cat "$log")" "secrets allow" "another project's config: the allow-list was not written"
  expect_absent "$(cat "$log")" "profile set" "another project's config: the profile was not edited"
  expect_absent "$(cat "$log")" "profile add" "another project's config: no profile was added"
  expect_last_line "$out" "CHECK 181 RESULT: INCOMPLETE (exit 2)" "another project's config: the verdict"
  assert_no_secret "$work/other-config" "another project's config"
}

# A coding agent's session is refused outright: every command this check runs is
# one a person types at their own terminal, and the marker is what tells them
# apart.
test_an_agent_session_is_refused() {
  local marker
  for marker in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT; do
    RUN181_ENV=("$marker=1")
    run181 "agent-$marker"
    expect_status "$status" 2 "an agent session ($marker)"
    expect_contains "$out" "is a coding agent's session" "an agent session ($marker): the refusal"
    expect_contains "$out" "FACT agent-session: FAIL — not reached" "an agent session ($marker): the row was never reached"
    [ ! -s "$work/agent-$marker/fugaro.log" ] || fail "an agent session ($marker): a fugaro command ran"
  done
}

# A shell started with tracing on is refused: `bash -x` prints every command as
# it runs, and one of them is handed the token file.
test_xtrace_is_refused() {
  local dir=$work/xtrace status2=0
  mkdir -p "$dir/tmp" "$dir/bin"
  seed_state "$dir"
  write_token "$dir/token"
  write_fakes "$dir/bin"
  out=$("${unmark[@]}" PATH="$dir/bin:$PATH" TMPDIR="$dir/tmp" FAKE181_STATE="$dir/state" \
    FAKE181_LOG="$dir/fugaro.log" bash -x "$script" \
    --installation sandbox --token-file "$dir/token" \
    --repo dimi_bmb/one --repo dimi_bmb/two --third-repo dimi_bmb/third 2>&1) || status2=$?
  expect_status "$status2" 2 "xtrace"
  expect_contains "$out" "FACT xtrace: FAIL — " "xtrace: the row"
  expect_contains "$out" "this shell is tracing" "xtrace: the refusal"
  [ ! -s "$dir/fugaro.log" ] || fail "xtrace: a fugaro command ran under a trace"
  expect_absent "$out" "$TOKEN_SENTINEL" "xtrace: the token in the trace"
}

# The token file's own shape, and the three ways it is not one this check will
# store: missing, readable by anybody, and empty.
test_token_file_complaints() {
  local dir=$work/tokenfile
  mkdir -p "$dir/tmp" "$dir/bin"
  seed_state "$dir"
  write_fakes "$dir/bin"
  local status2=0
  # missing
  out=$("${unmark[@]}" PATH="$dir/bin:$PATH" TMPDIR="$dir/tmp" FAKE181_STATE="$dir/state" \
    FAKE181_LOG="$dir/fugaro.log" bash "$script" \
    --installation sandbox --token-file "$dir/absent" \
    --repo dimi_bmb/one --repo dimi_bmb/two --third-repo dimi_bmb/third 2>&1) || status2=$?
  expect_status "$status2" 2 "a missing token file"
  expect_contains "$out" "could not be read" "a missing token file: the refusal"
  # too permissive
  status2=0
  write_token "$dir/loose"
  chmod 644 "$dir/loose"
  out=$("${unmark[@]}" PATH="$dir/bin:$PATH" TMPDIR="$dir/tmp" FAKE181_STATE="$dir/state" \
    FAKE181_LOG="$dir/fugaro.log" bash "$script" \
    --installation sandbox --token-file "$dir/loose" \
    --repo dimi_bmb/one --repo dimi_bmb/two --third-repo dimi_bmb/third 2>&1) || status2=$?
  expect_status "$status2" 2 "a token file anybody can read"
  expect_contains "$out" "mode 644" "a token file anybody can read: the refusal"
  expect_absent "$out" "$TOKEN_SENTINEL" "a token file anybody can read: the token in the refusal"
  # empty
  status2=0
  : >"$dir/empty"
  chmod 600 "$dir/empty"
  out=$("${unmark[@]}" PATH="$dir/bin:$PATH" TMPDIR="$dir/tmp" FAKE181_STATE="$dir/state" \
    FAKE181_LOG="$dir/fugaro.log" bash "$script" \
    --installation sandbox --token-file "$dir/empty" \
    --repo dimi_bmb/one --repo dimi_bmb/two --third-repo dimi_bmb/third 2>&1) || status2=$?
  expect_status "$status2" 2 "an empty token file"
  expect_contains "$out" "is empty (0 bytes)" "an empty token file: the refusal"
}

# The three refusals the check's subject turns on, each read the other way: the
# one that accepted the edit, the one whose sentence named nothing, and the one
# that never reached the door.
test_the_refusals_read_the_other_way() {
  RUN181_MODE=subset-silent
  run181 silent
  expect_status "$status" 1 "a refusal that names nothing"
  expect_contains "$out" "FACT subset-refusal: FAIL — " "a refusal that names nothing: the row"
  expect_contains "$out" "does not name the repository" "a refusal that names nothing: the reading"

  RUN181_MODE=publish-mount-ok
  run181 publishok
  expect_status "$status" 1 "a publish that accepted the mount"
  expect_contains "$out" "FACT mount-publish: FAIL — " "a publish that accepted the mount: the row"

  RUN181_MODE=update-mount-ok
  run181 updateok
  expect_status "$status" 1 "an update that accepted the mount"
  expect_contains "$out" "FACT mount-deploy: FAIL — " "an update that accepted the mount: the row"

  RUN181_MODE=open-refused
  run181 openrefused
  expect_status "$status" 1 "the tokenless profile refusing the third repository"
  expect_contains "$out" "FACT open-publish: FAIL — " "the tokenless profile refusing the third repository: the row"
}

# The job the deploy left, read three ways: the deploy that failed, the listing
# that answered nothing, and the job that carries a token after all.
test_the_job_readings() {
  RUN181_MODE=update-fails
  run181 deployfailed
  expect_status "$status" 1 "a deploy that failed"
  expect_contains "$out" "FACT deploy: FAIL — " "a deploy that failed: the row"
  expect_contains "$out" "FACT job: FAIL — " "a deploy that failed: the job row says why"

  RUN181_MODE=no-job
  run181 nojob
  expect_status "$status" 1 "a deploy that left no job"
  expect_contains "$out" "FACT job: FAIL — " "a deploy that left no job: the row"

  RUN181_MODE=job-has-token
  run181 jobtoken
  expect_status "$status" 1 "a job that carries a token"
  expect_contains "$out" "FACT job: FAIL — " "a job that carries a token: the row"
  expect_contains "$out" "holds a Bitbucket token" "a job that carries a token: the reading"

  RUN181_MODE=job-no-service
  run181 jobnoservice
  expect_status "$status" 1 "a job with no service address"
  expect_contains "$out" "FACT job: FAIL — " "a job with no service address: the row"

  # A listing the account may not read is not a FAIL: nothing was measured, so
  # the criterion is unsettled and the run is INCOMPLETE.
  RUN181_MODE=jobs-unreadable
  run181 jobsunreadable
  expect_status "$status" 3 "a listing the account may not read"
  expect_contains "$out" "FACT job: UNVERIFIED — " "a listing the account may not read: the row"
  expect_last_line "$out" "CHECK 181 RESULT: INCOMPLETE (exit 3)" "a listing the account may not read: the verdict"
}

# The runs. A run that did not push, a run on another job, a third repository
# that pushed anyway, and a third repository whose refusal names nothing.
test_the_run_readings() {
  RUN181_MODE=no-push
  run181 nopush
  expect_status "$status" 1 "a run that did not push"
  expect_contains "$out" "FACT run-a: FAIL — " "a run that did not push: the row"

  RUN181_MODE=run-other-job
  run181 otherjob
  expect_status "$status" 1 "a run on another job"
  expect_contains "$out" "FACT run-a: FAIL — " "a run on another job: the row"
  expect_contains "$out" "$OTHER_JOB_NAME" "a run on another job: the job it read instead"

  RUN181_MODE=third-pushes
  run181 thirdpushed
  expect_status "$status" 1 "the third repository pushing anyway"
  expect_contains "$out" "FACT run-third: FAIL — " "the third repository pushing anyway: the row"
  expect_contains "$out" "CLONED AND PUSHED" "the third repository pushing anyway: the reading"

  RUN181_MODE=third-silent
  run181 thirdsilent
  expect_status "$status" 1 "a third-repository refusal that names nothing"
  expect_contains "$out" "FACT run-third: FAIL — " "a third-repository refusal that names nothing: the row"
  expect_contains "$out" "do NOT name the repository" "a third-repository refusal that names nothing: the reading"

  # No terminal row at all: nothing was measured, so the criterion is unsettled.
  RUN181_MODE=third-norow
  run181 thirdnorow
  expect_status "$status" 3 "a third run with no row to read"
  expect_contains "$out" "FACT run-third: UNVERIFIED — " "a third run with no row to read: the row"
}

# A launch that never happened is a FAIL, not an UNVERIFIED: `fugaro run` said
# so itself.
test_a_run_that_never_launched_is_a_fail() {
  RUN181_MODE=run-fails
  run181 runfails
  expect_status "$status" 1 "a run that never launched"
  expect_contains "$out" "FACT run-a: FAIL — " "a run that never launched: the row"
  expect_contains "$out" "was never launched" "a run that never launched: the reading"
}

# The store, the allow-list and the profile add: each refused at its own door,
# which stops the run before anything is measured.
test_the_earlier_doors_that_stop_the_run() {
  RUN181_MODE=allow-refused
  run181 allowrefused
  expect_status "$status" 2 "an allow-list the config refuses"
  expect_contains "$out" "FACT allow-list: FAIL — not reached" "an allow-list the config refuses: the row"
  expect_contains "$out" "was REFUSED" "an allow-list the config refuses: the reading"
  assert_no_secret "$work/allowrefused" "an allow-list the config refuses"

  RUN181_MODE=store-refused
  run181 storerefused
  expect_status "$status" 2 "a store that was refused"
  expect_contains "$out" "FACT store: FAIL — not reached" "a store that was refused: the row"
  assert_no_secret "$work/storerefused" "a store that was refused"

  RUN181_MODE=profile-add-refused
  run181 addrefused
  expect_status "$status" 2 "a profile that could not be defined"
  expect_contains "$out" "FACT profile: FAIL — not reached" "a profile that could not be defined: the row"

  RUN181_MODE=doctor-fails
  run181 doctorfails
  expect_status "$status" 2 "a doctor that failed"
  expect_contains "$out" "FACT installation: FAIL — not reached" "a doctor that failed: the row"

  # A doctor that names another installation is a stop, not a measurement of
  # somebody else's project.
  RUN181_MODE=doctor-wrong-name
  run181 doctorwrong
  expect_status "$status" 2 "a doctor that named another installation"
  expect_contains "$out" "is the installation 'otherproject'" "a doctor that named another installation: the refusal"

  # doctor exits 1 whenever a check is a warning, and still prints its document:
  # that is an answer, not a failure.
  RUN181_MODE=doctor-warns
  run181 doctorwarns
  expect_status "$status" 0 "a doctor that warned"
  expect_contains "$out" "FACT installation: PASS — " "a doctor that warned: the installation was still read"
}

# The cleanup has to work with HOME unset: reading it unguarded would abort the
# removal under `set -u` and leave the scratch directory behind.
test_cleanup_survives_an_unset_home() {
  local dir=$work/nohome
  mkdir -p "$dir/tmp" "$dir/bin"
  seed_state "$dir"
  write_token "$dir/token"
  write_fakes "$dir/bin"
  local status2=0 scratch
  out=$("${unmark[@]}" -u HOME PATH="$dir/bin:$PATH" TMPDIR="$dir/tmp" FAKE181_STATE="$dir/state" \
    FAKE181_LOG="$dir/fugaro.log" CHECK181_RUN_SECONDS=30 CHECK181_RUN_POLL_SECONDS=1 \
    CHECK181_RUN_POLL_MAX=4 bash "$script" \
    --installation sandbox --token-file "$dir/token" \
    --repo dimi_bmb/one --repo dimi_bmb/two --third-repo dimi_bmb/third 2>&1) || status2=$?
  expect_status "$status2" 0 "an unset HOME"
  expect_contains "$out" "FACT restore: PASS — " "an unset HOME: the cleanup"
  scratch=$(printf '%s\n' "$out" | sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' | head -n 1)
  [ -n "$scratch" ] || fail "an unset HOME: the run announced no scratch directory"
  [ ! -e "$scratch" ] || fail "an unset HOME: the scratch directory $scratch is still there"
}

# The three ways the check is pointed at itself, refused before anything runs.
test_the_argument_shapes_that_are_refused() {
  local status2=0 out2=""
  out2=$("${unmark[@]}" PATH="$base_path" TMPDIR="$work" bash "$script" --dry-run --installation sandbox \
    --token-file "$work/token" --repo dimi_bmb/one --repo dimi_bmb/one --third-repo dimi_bmb/third 2>&1) || status2=$?
  expect_status "$status2" 2 "the same repository twice"
  expect_contains "$out2" "the same repository" "the same repository twice: the refusal"

  status2=0
  out2=$("${unmark[@]}" PATH="$base_path" TMPDIR="$work" bash "$script" --dry-run --installation sandbox \
    --token-file "$work/token" --repo dimi_bmb/one --repo dimi_bmb/two --third-repo dimi_bmb/two 2>&1) || status2=$?
  expect_status "$status2" 2 "the third repository on the allow-list"
  expect_contains "$out2" "has to be one the token's block does NOT name" "the third repository on the allow-list: the refusal"

  status2=0
  out2=$("${unmark[@]}" PATH="$base_path" TMPDIR="$work" bash "$script" --dry-run --installation sandbox \
    --token-file "$work/token" --repo dimi_bmb/one --repo dimi_bmb/two --repo dimi_bmb/three --third-repo dimi_bmb/four 2>&1) || status2=$?
  expect_status "$status2" 2 "three --repo pairs"
  expect_contains "$out2" "given three times" "three --repo pairs: the refusal"

  status2=0
  out2=$("${unmark[@]}" PATH="$base_path" TMPDIR="$work" bash "$script" --dry-run --installation sandbox \
    --repo dimi_bmb/one --repo dimi_bmb/two --third-repo dimi_bmb/third 2>&1) || status2=$?
  expect_status "$status2" 2 "no token file"
  expect_contains "$out2" "--token-file is required" "no token file: the refusal"

  status2=0
  out2=$("${unmark[@]}" PATH="$base_path" TMPDIR="$work" bash "$script" --dry-run --installation sandbox \
    --token-file "$work/token" --repo dimi_bmb/one --repo dimi_bmb/two 2>&1) || status2=$?
  expect_status "$status2" 2 "no third repository"
  expect_contains "$out2" "--third-repo is required" "no third repository: the refusal"

  status2=0
  out2=$("${unmark[@]}" PATH="$base_path" TMPDIR="$work" bash "$script" --dry-run --installation sandbox \
    --token-file "$work/token" --repo dimi_bmb/one --repo dimi_bmb/two --third-repo dimi_bmb/third --nope 2>&1) || status2=$?
  expect_status "$status2" 2 "an unknown argument"
  expect_contains "$out2" "unknown argument" "an unknown argument: the refusal"

  status2=0
  out2=$("${unmark[@]}" PATH="$base_path" TMPDIR="$work" bash "$script" --dry-run --installation sandbox \
    --token-file "$work/token" --repo dimi_bmb/one --repo "not a name" --third-repo dimi_bmb/third 2>&1) || status2=$?
  expect_status "$status2" 2 "a --repo that is not owner/name"
  expect_contains "$out2" "is not owner/name" "a --repo that is not owner/name: the refusal"
}

# A block that ran to the end with a criterion nothing wrote is INCOMPLETE, and
# the rows the run never reached read as loudly as the ones it did.
test_the_block_always_carries_every_criterion() {
  RUN181_MODE=allow-refused
  run181 everyrow
  local key
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$out" "FACT $key:" "every criterion: the $key row"
  done
  expect_contains "$out" "FACT restore: PASS — " "every criterion: the restore row is written even for a run that stopped early"
  expect_last_line "$out" "CHECK 181 RESULT: INCOMPLETE (exit 2)" "every criterion: a run that stopped early is INCOMPLETE"
}

# The secret assertions are the reason this check exists, so they have to be able
# to fail: two modes leak the value on purpose, one into the output and one into
# a file the run leaves behind, and both assertions must catch them.
test_the_secret_assertions_catch_an_echoing_script() {
  local caught=0
  RUN181_MODE=store-echoes
  run181 leaks
  case "$(cat "$work/leaks/out" "$work/leaks/err" 2>/dev/null || true)" in
  *"$TOKEN_SENTINEL"*) caught=$((caught + 1)) ;;
  esac
  RUN181_MODE=store-leaks-file
  run181 leaksfile
  local hits
  hits=$(grep -rl -- "$TOKEN_SENTINEL" "$work/leaksfile" 2>/dev/null \
    | grep -v -F -x -e "$work/leaksfile/token" -e "$work/leaksfile/state/token-echo" || true)
  [ -n "$hits" ] && caught=$((caught + 1))
  # Two, and not fewer: the leak in what the run PRINTED and the leak in a file
  # it LEFT BEHIND are two assertions, and an assert_no_secret that lost either
  # half would report one here.
  [ "${caught:-0}" -eq 2 ] \
    || fail "detector: an echoing script raised $caught of the two secret assertions this can provoke (the token in the output, the token in a file left behind), so one of them is vacuous"
  # The run that leaks into a FILE is a pass by the check's own reading: nothing
  # it prints carries the value, and the file is one it never opens again. That
  # is exactly why the assertion above greps for one — the check cannot see a
  # leak it does not read, and the test is what stands in for the owner's eye.
  expect_status "$status" 0 "the detector run that leaks into a file"
}

# Sharding, so the package's Go test can run the tests of this file side by
# side: CHECK_TEST_SHARD="i/n" runs every n-th test starting at i (each test
# works in its own directory under $work), CHECK_TEST_ONLY=name runs that one
# test alone. Neither set: all run.
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

t test_happy_path
t test_an_existing_profile_is_read_and_its_mount_taken_off
t test_unmeasured_criteria_are_not_a_pass
t test_a_failed_criterion_is_a_fail
t test_dry_run_touches_nothing
t test_the_plan_and_the_usage_need_no_cloud_tools
t test_belong_is_refused_everywhere
t test_a_repository_outside_the_workspace_is_refused
t test_the_config_the_check_edits_is_the_installations
t test_a_config_that_is_not_the_installations_is_refused
t test_an_agent_session_is_refused
t test_xtrace_is_refused
t test_token_file_complaints
t test_the_refusals_read_the_other_way
t test_the_job_readings
t test_the_run_readings
t test_a_run_that_never_launched_is_a_fail
t test_the_earlier_doors_that_stop_the_run
t test_cleanup_survives_an_unset_home
t test_the_argument_shapes_that_are_refused
t test_the_block_always_carries_every_criterion
t test_the_secret_assertions_catch_an_echoing_script

if [ "$failures" -ne 0 ]; then
  echo "check181_test: $failures failure(s)" >&2
  exit 1
fi
echo "check181_test: all tests passed" >&2
