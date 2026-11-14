#!/usr/bin/env bash
# Tests for scripts/live-checks/check27.sh against a fake gcloud and a fake
# fugaro on PATH: nothing here reaches the network, there is no cloud project,
# no billing account is linked and no credential is read, made or printed. NO
# LIVE STEP OF CHECK 27 IS RUN BY ANY OF THIS — an agent never runs a check, and
# what runs here is a script of fakes.
#
# The two fakes model the check's own premises and nothing else. The fake gcloud
# holds a project that exists once something creates it, the billing link, the
# Firestore location, the two buckets, the jobs, the secret containers and the
# images a converge leaves behind — so every read-back the check makes is a
# reading of state the case put there, and every delete it makes is visible
# afterwards. The fake fugaro is the CLI: it REFUSES the create path when it has
# no terminal (which is how a case proves the script gave the wizard a pty),
# asks for the project ID and reads the line the pty wrote, prints the plan a
# `--plan-only` run prints — one row per stage, the project's own row carrying
# the billing account the CLI puts there — converges, and answers `update` and
# `doctor`.
# Both log every call before anything else, so "no call was made at all" is
# provable rather than argued.
#
# Usage: bash scripts/live-checks/check27_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check27.sh
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK27_TEST_KEEP:-}" ]; then
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 900)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1; block: $(printf '%s\n' "${out:-}" | tail -n 12 | tr '\n' '|')"
}

# The throwaway project, the sandbox repository and the made-up billing account
# of the right shape a case runs against. No real account: the check compares
# the link it reads back with what --billing-account was given, and never prints
# it.
PROJECT=fugaro-live-chk27-a
REPO=owner/sandbox
ACCOUNT=0A1B2C-3D4E5F-607182

# Every fact key the run's block carries whatever happened (the dry run's own
# narrower list is asserted separately).
BLOCK_KEYS=(project billing-account belong identity config-root
  project-absent version plan-only negative wizard
  created billing firestore installation images doctor update
  secrets human restore)

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin" "$1/state"
  write_fake_gcloud "$1"
  write_fake_fugaro "$1"
  chmod 700 "$1/bin/gcloud" "$1/bin/fugaro"
}

write_fake_gcloud() { # dir
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake gcloud for check27.sh's test: the project, its billing link, its
# Firestore database, its buckets, its jobs, its secret containers and its
# images, all held in files under $FAKE27_DIR/state so a case can put them there
# and read the state afterwards. Every call is recorded on its first line,
# before anything else, so a run that made no gcloud call at all is provable.
set -euo pipefail
dir=${FAKE27_DIR:?FAKE27_DIR is not set}
state=$dir/state
printf 'gcloud %s\n' "$*" >>"$state/calls.gcloud"

unknown() {
  printf 'fake gcloud: unexpected call: %s\n' "$*" >&2
  exit 64
}

named_by() { # file id: the id the file records, empty when it records another
  [ -f "$1" ] || return 0
  local id
  id=$(cat "$1")
  [ "$id" = "$2" ] && printf '%s' "$id"
  return 0
}

cmd="${1:-} ${2:-} ${3:-}"
case "$cmd" in
"projects describe "*)
  if [ -n "$(named_by "$state/project" "$3")" ]; then
    printf '%s\tACTIVE\n' "$3"
    exit 0
  fi
  if [ -n "$(named_by "$state/project.deleted" "$3")" ]; then
    printf 'DELETE_REQUESTED\n'
    exit 0
  fi
  printf 'ERROR: (gcloud.projects.describe) [NOT_FOUND] Requested entity was not found.\n' >&2
  exit 1
  ;;
"projects delete "*)
  if [ -n "${FAKE27_PROJECT_DELETE_REFUSED:-}" ]; then
    printf 'ERROR: (gcloud.projects.delete) PERMISSION_DENIED: the caller does not have permission\n' >&2
    exit 1
  fi
  rm -f "$state/project"
  printf '%s\n' "$3" >"$state/project.deleted"
  ;;
"billing projects "*)
  [ -f "$state/billing" ] || { printf 'ERROR: (gcloud.billing.projects.describe) NOT_FOUND\n' >&2; exit 1; }
  cat "$state/billing"
  ;;
"firestore databases "*)
  [ -f "$state/firestore" ] || { printf 'ERROR: (gcloud.firestore.databases.describe) NOT_FOUND\n' >&2; exit 1; }
  cat "$state/firestore"
  ;;
"storage buckets "*)
  cat "$state/buckets" 2>/dev/null || true
  ;;
"storage rm "*)
  if [ -n "${FAKE27_BUCKET_REFUSES_DELETE:-}" ]; then
    printf 'ERROR: (gcloud.storage.rm) HTTPError 403: the caller does not have permission\n' >&2
    exit 1
  fi
  target=""
  for a in "$@"; do
    case "$a" in gs://*) target=${a#gs://} ;; esac
  done
  [ -n "$target" ] || unknown "$@"
  grep -vxF "$target" "$state/buckets" >"$state/buckets.new" 2>/dev/null || true
  mv "$state/buckets.new" "$state/buckets"
  ;;
"run jobs list")
  cat "$state/jobs" 2>/dev/null || true
  ;;
"run jobs delete")
  if [ -n "${FAKE27_JOB_REFUSES_DELETE:-}" ]; then
    printf 'ERROR: (gcloud.run.jobs.delete) PERMISSION_DENIED: the caller does not have permission\n' >&2
    exit 1
  fi
  grep -vxF "${4:?}" "$state/jobs" >"$state/jobs.new" 2>/dev/null || true
  mv "$state/jobs.new" "$state/jobs"
  ;;
"secrets list "*)
  cat "$state/secrets" 2>/dev/null || true
  ;;
"artifacts docker images")
  cat "$state/images" 2>/dev/null || true
  ;;
*)
  unknown "$@"
  ;;
esac
GCLOUD_EOF
}

write_fake_fugaro() { # dir
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check27.sh's test: the CLI as this check drives it.
#
# `setup` is the whole point of the check, so this fake models what makes it a
# person's run: with NO terminal it REFUSES (the project is created only where a
# person can type), and with one it prints the wizard's banner and suggestion,
# asks "Type the GCP project's ID: " and reads the line the pty wrote — then
# writes the project, its buckets, its job, its secrets, its images, its billing
# link and its Firestore location into $FAKE27_DIR/state, so every read-back the
# check makes is a reading of what this fake really leaves behind.
#
# `version`, `update` and `doctor` answer as the check needs: a release number, a
# stage list that reads `No changes` on the second converge, and doctor's own
# `project:` line.
set -euo pipefail
dir=${FAKE27_DIR:?FAKE27_DIR is not set}
state=$dir/state
tty=no
[ -t 0 ] && tty=yes
printf 'fugaro %s tty=%s xdg=%s\n' "$*" "$tty" "${XDG_CONFIG_HOME:-unset}" >>"$state/calls.fugaro"

flag() { # flag: the value of `--flag <value>` in the arguments after it, or nothing
  local want=$1 prev="" a
  shift
  for a in "$@"; do
    if [ "$prev" = "$want" ]; then printf '%s' "$a"; return 0; fi
    prev=$a
  done
  return 1
}
has() { # flag
  local want=$1 a
  shift
  for a in "$@"; do [ "$a" = "$want" ] && return 0; done
  return 1
}
# stage_list LABEL [ACCOUNT], the six stages as the run prints them: one ROW
# each, in initflow's own shape ('  <status>  <name>  <detail>'), because a
# `--plan-only` run prints rows and not bare names. The project stage is the row
# that names the billing account — a real plan draws it as `link billing account
# <ID> (its own confirmation; may incur charges)` (init_project.go's plan detail)
# — so the fake prints that text where a case can see it, and a case can fail
# when the account ID reaches what a run prints rather than never noticing.
stage_list() {
  local label=$1 acct=${2:-}
  if [ -n "$acct" ]; then
    printf '  %s  %-16s will create the project, add Firebase (free), link billing account %s (its own confirmation; may incur charges)\n' "$label" installation "$acct"
  else
    printf '  %s  %-16s\n' "$label" installation
  fi
  printf '  %s  %-16s\n' "$label" firebase
  printf '  %s  %-16s\n' "$label" images
  printf '  %s  %-16s\n' "$label" installation-2
  printf '  %s  %-16s\n' "$label" secrets
  printf '  %s  %-16s\n' "$label" plugin
}
installation_of() { # id
  printf '%s\n' "$1" >"$state/project"
  if [ -z "${FAKE27_NO_BUCKET:-}" ]; then
    printf 'fugaro-runs-%s\n' "$1" >>"$state/buckets"
  fi
  printf 'fugaro-state-%s\n' "$1" >>"$state/buckets"
  if [ -z "${FAKE27_NO_JOB:-}" ]; then
    printf 'fugarohist\n' >"$state/jobs"
  fi
  printf 'github-app-key\nclaude-oauth-token\nfugaro-billing-worker\n' >"$state/secrets"
  if [ -z "${FAKE27_NO_IMAGE:-}" ]; then
    printf 'history\tlatest\n' >"$state/images"
  fi
  if [ -n "${FAKE27_BILLING_DISABLED:-}" ]; then
    printf 'billingAccounts/%s\tFalse\n' "${FAKE27_ACCOUNT:-0A1B2C-3D4E5F-607182}" >"$state/billing"
  else
    printf 'billingAccounts/%s\tTrue\n' "${FAKE27_ACCOUNT:-0A1B2C-3D4E5F-607182}" >"$state/billing"
  fi
  printf '%s\n' "${FAKE27_FIRESTORE:-us-east5}" >"$state/firestore"
}

verb=${1:-}
case "$verb" in
version)
  printf '%s\n' "${FAKE27_VERSION:-0.24.0}"
  ;;
setup)
  if has --plan-only "$@"; then
    if [ -n "${FAKE27_PLAN_NO_STAGES:-}" ]; then
      printf '  will do  %-16s\n' installation
    else
      stage_list 'will do' "$(flag --link-billing "$@" || true)"
    fi
    if [ -n "${FAKE27_PLAN_BLOCKS:-}" ]; then
      sleep 600
    fi
    if [ -n "${FAKE27_PLAN_CREATES:-}" ]; then
      installation_of "$(flag --gcp-project "$@" || true)"
    fi
    exit 0
  fi
  if [ "$tty" != yes ]; then
    printf 'ERROR: creating %s needs your own terminal, and this run has none\n' "${3:-the project}" >&2
    if [ -n "${FAKE27_NEG_CREATES:-}" ]; then
      installation_of "${3:-}"
    fi
    exit "${FAKE27_NEG_EXIT:-1}"
  fi
  answer=""
  printf 'fugaro setup: this is the first run here; each answer has a suggestion, press Enter to accept it.\n'
  if [ -z "${FAKE27_ASK_NOTHING:-}" ]; then
    printf "the GCP project's ID [fugaro-fugaro]\n"
    printf "Type the GCP project's ID: "
    if [ -n "${FAKE27_BLOCK:-}" ]; then
      sleep 600
    fi
    IFS= read -r answer || answer=""
    printf '%s\n' "$answer" >"$state/id-typed"
    printf 'CONFIRM: creating %s, and adding Firebase to it\n' "$answer"
  fi
  # The wizard's own billing banner, with the account in it: a real create run
  # with `--link-billing` prints that confirmation (step 12), after its question
  # and before it creates anything. It is the account ID's own way into a
  # transcript, so a create that then fails is what a case reads it through.
  acct=$(flag --link-billing "$@" || true)
  if [ -n "$acct" ]; then
    printf 'CONFIRM: linking billing account %s (its own confirmation; may incur charges)\n' "$acct"
  fi
  if [ -n "${FAKE27_CREATE_FAILS:-}" ]; then
    if [ -n "${FAKE27_CREATE_FAILS_HALF:-}" ]; then
      installation_of "${FAKE27_FAIL_ID:-not-a-project}"
    fi
    printf 'ERROR: (fugaro) creating the project: HTTPError 403: the caller does not have permission\n' >&2
    exit 1
  fi
  printf '%s\n' "$acct" >"$state/linkbilling"
  installation_of "$answer"
  stage_list 'done'
  exit 0
  ;;
update)
  if [ -n "${FAKE27_UPDATE_FAILS:-}" ]; then
    printf 'ERROR: (fugaro) the installation root failed to apply\n' >&2
    exit 1
  fi
  case "$*" in
  *"--project fugaro"*) ;;
  *)
    printf 'fake fugaro: update without --project fugaro: %s\n' "$*" >&2
    exit 64
    ;;
  esac
  printf '%s\n' "$*" >>"$state/updates"
  n=$(wc -l <"$state/updates" | tr -d ' ')
  if [ "$n" -ge 2 ] && [ -n "${FAKE27_UPDATE2_WORK:-}" ]; then
    stage_list 'will do'
    exit 0
  fi
  stage_list 'No changes'
  exit 0
  ;;
doctor)
  printf 'project: fugaro (GCP %s, us-east5)\n' "${FAKE27_DOCTOR_PROJECT:-${FAKE27_PROJECT:-fugaro-live-chk27-a}}"
  if [ -n "${FAKE27_DOCTOR_FAILS:-}" ]; then
    printf 'default-service-accounts: FAIL — fix: gcloud projects remove-iam-policy-binding ...\n'
    exit 1
  fi
  printf 'secrets: ok\n'
  exit 0
  ;;
*)
  printf 'fake fugaro: unexpected call: %s\n' "$*" >&2
  exit 64
  ;;
esac
FUGARO_EOF
}

# --------------------------------------------------------------------- cases
dir=""
status=0
out=""
RUN27_ARGS=()
RUN27_ENV=()

setup_case() { # name
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp"
  write_fakes "$dir"
  : >"$dir/state/calls.gcloud"
  : >"$dir/state/calls.fugaro"
  : >"$dir/state/buckets"
  : >"$dir/state/jobs"
  : >"$dir/state/secrets"
  : >"$dir/state/images"
  : >"$dir/state/updates"
  status=0
  out=""
  RUN27_ARGS=()
  RUN27_ENV=()
}

# The agent markers are UNSET: this suite is itself run inside an agent's
# session in CI, and Check 27's first guard is exactly that marker — a case that
# wants one re-adds it through RUN27_ENV, and the xtrace case runs the script
# under `bash -x` through RUN27_XTRACE.
run27() { # [args...]
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT -u CLAUDE_CODE_SSE_PORT -u XDG_CONFIG_HOME \
    TMPDIR="$dir/tmp" FAKE27_DIR="$dir" FAKE27_PROJECT="$PROJECT" \
    CHECK27_DIR="$dir/root" \
    CHECK27_PLAN_SECONDS="${PLAN_SECONDS:-30}" \
    CHECK27_CREATE_SECONDS="${CREATE_SECONDS:-30}" \
    CHECK27_CONVERGE_SECONDS="${CONVERGE_SECONDS:-30}" \
    CHECK27_QUICK_SECONDS="${QUICK_SECONDS:-30}" \
    PATH="$dir/bin:$PATH" \
    ${RUN27_ENV[@]+"${RUN27_ENV[@]}"} \
    bash ${RUN27_XTRACE:+-x} "$script" \
    ${RUN27_ARGS[@]+"${RUN27_ARGS[@]}"} "$@" 2>"$dir/err") || status=$?
  RUN27_ARGS=()
  RUN27_ENV=()
  RUN27_XTRACE=""
  PLAN_SECONDS=30
  CREATE_SECONDS=30
  CONVERGE_SECONDS=30
  QUICK_SECONDS=30
  return 0
}

run27_default() { run27 --project "$PROJECT" --billing-account "$ACCOUNT" --repo "$REPO" "$@"; }

expect_block_keys() { # what
  local key
  expect_contains "$out" "CHECK 27 RESULT" "$1: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$out" "FACT $key:" "$1: the $key row"
  done
  expect_contains "$out" "CHECK 27 RESULT END" "$1: the block's end line"
}

expect_no_calls() { # what
  if [ -s "$dir/state/calls.gcloud" ]; then
    fail "$1: a gcloud call was made: $(tr '\n' ';' <"$dir/state/calls.gcloud")"
  fi
  if [ -s "$dir/state/calls.fugaro" ]; then
    fail "$1: a fugaro call was made: $(tr '\n' ';' <"$dir/state/calls.fugaro")"
  fi
}

fact_line() { # key
  printf '%s\n' "$out" | grep -m1 "^FACT $1:" || true
}

calls_gcloud() { cat "$dir/state/calls.gcloud" 2>/dev/null || true; }
calls_fugaro() { cat "$dir/state/calls.fugaro" 2>/dev/null || true; }

expect_no_fails() { # what
  local line
  while IFS= read -r line; do
    case "$line" in
    *"FAIL —"*) fail "$1: a row FAILed: $line" ;;
    esac
  done <<<"$out"
}

# ==================================================================== the tests

test_a_full_run_passes() {
  setup_case full
  run27_default
  expect_status "$status" 0 "a full run"
  expect_block_keys "a full run"
  expect_no_fails "a full run"
  expect_contains "$(fact_line project-absent)" "PASS" "the project was absent before the run"
  expect_contains "$(fact_line plan-only)" "PASS" "the plan-only run"
  expect_contains "$(fact_line negative)" "PASS" "the negative run"
  expect_contains "$(fact_line wizard)" "PASS" "the create run"
  expect_contains "$(fact_line wizard)" "fugaro-fugaro" "the id the wizard proposed, recorded"
  expect_contains "$(fact_line created)" "ACTIVE" "the project read back"
  expect_contains "$(fact_line billing)" "PASS" "the billing link"
  expect_contains "$(fact_line firestore)" "us-east5" "the Firestore location"
  expect_contains "$(fact_line installation)" "fugarohist" "the history job"
  expect_contains "$(fact_line images)" "history" "the history image"
  expect_contains "$(fact_line doctor)" "PASS" "doctor"
  expect_contains "$(fact_line update)" "No changes" "the second update"
  expect_contains "$(fact_line secrets)" "INFO" "the secrets row is informational"
  expect_contains "$(fact_line human)" "INFO" "the human row is informational"
  expect_contains "$(fact_line restore)" "PASS" "the Restore"
  expect_contains "$(fact_line restore)" "DELETE_REQUESTED" "the project's own state after the delete"
}

test_the_create_run_is_given_a_real_terminal() {
  setup_case tty
  run27_default
  expect_status "$status" 0 "a full run"
  local create neg up
  create=$(grep 'setup --name' "$dir/state/calls.fugaro" | head -1)
  expect_contains "$create" "tty=yes" "the create run is at a real terminal"
  neg=$(grep -- '--yes' "$dir/state/calls.fugaro" | head -1)
  expect_contains "$neg" "tty=no" "the negative run has no terminal at all"
  up=$(grep 'update --project' "$dir/state/calls.fugaro" | head -1)
  expect_contains "$up" "tty=no" "the update runs have no terminal"
  # The answer the pty wrote is the id the check's own --project names: that is
  # what makes the create run THIS check's project and not the wizard's default.
  expect_contains "$(cat "$dir/state/id-typed")" "$PROJECT" "the id typed at the question"
  expect_contains "$(cat "$dir/state/linkbilling")" "$ACCOUNT" "the flag path linked the account"
}

# The billing account's ID reaches what a run prints through the WIZARD, and
# through nothing else here: a real `--plan-only` draws the project stage's row
# as `link billing account <ID> (its own confirmation; may incur charges)`
# (init_project.go's plan detail), the transcript keeps it and the runner echoes
# it to the owner live. What this check owns is its own lines and its RESULT
# block, and neither carries it. Both halves are asserted, so the case fails
# whichever way the script starts lying — by leaking it, or by claiming a
# redaction it does not have.
test_the_account_id_is_the_wizards_own_line_and_not_the_scripts() {
  setup_case redact
  run27_default
  expect_status "$status" 0 "a full run"
  expect_contains "$(cat "$dir/err")" "link billing account $ACCOUNT (its own confirmation; may incur charges)" "the plan row the wizard prints, echoed to the owner as it is"
  expect_absent "$out" "$ACCOUNT" "the script's own stdout: its printed command lines and its block"
  expect_contains "$(fact_line billing-account)" "PASS" "the shape guard"
}

test_belong_is_refused() {
  local bad
  for bad in belong fugaro-belong fugaro-dev edge-devel-dimi my-belong-2026 FUGARO-BELONG-2; do
    setup_case "belong-$bad"
    run27_default --project "$bad"
    expect_status "$status" 2 "--project $bad"
    expect_contains "$(fact_line belong)" "FAIL" "--project $bad names a protected installation"
    expect_contains "$out" "CHECK 27 RESULT" "the block is still printed for $bad"
  done
  setup_case belong-repo
  run27 --project "$PROJECT" --billing-account "$ACCOUNT" --repo owner/belong-sandbox
  expect_status "$status" 2 "--repo owner/belong-sandbox"
  expect_contains "$(fact_line belong)" "FAIL" "--repo names a protected installation"
}

test_belong_is_refused_before_any_call() {
  setup_case belong-quiet
  run27_default --project belong
  expect_status "$status" 2 "--project belong"
  expect_no_calls "the belong guard runs before anything else"
  expect_contains "$(fact_line restore)" "nothing was created and nothing was read" "the Restore made no call either"
}

test_the_name_must_be_fugaro() {
  setup_case name
  run27 --project "$PROJECT" --billing-account "$ACCOUNT" --repo "$REPO" --name other
  expect_status "$status" 2 "--name other"
  expect_contains "$out" "must be fugaro" "--name other is refused"
  expect_no_calls "--name other is refused before anything runs"
}

test_a_project_that_already_exists_stops() {
  setup_case exists
  printf '%s\n' "$PROJECT" >"$dir/state/project"
  run27_default
  expect_status "$status" 2 "a project that already exists"
  expect_contains "$(fact_line project-absent)" "FAIL" "the project-absent guard"
  expect_contains "$(fact_line project-absent)" "already exists" "the guard's detail"
  expect_absent "$(calls_fugaro)" "setup --name" "nothing was created"
  expect_contains "$(fact_line restore)" "FAIL" "the Restore finds it there"
  expect_contains "$(fact_line restore)" "Delete it yourself" "the row names how to finish by hand"
}

test_a_dev_build_is_refused() {
  setup_case dev
  RUN27_ENV=(FAKE27_VERSION=dev)
  run27_default
  expect_status "$status" 2 "a dev build"
  expect_contains "$(fact_line version)" "FAIL" "the version guard"
  expect_contains "$(fact_line version)" "ghcr.io" "why a dev build cannot run the check"
  case "$(calls_gcloud)" in
  *'projects describe'*) fail "a dev build was refused only after a gcloud call: $(calls_gcloud)" ;;
  esac
}

test_a_dry_run_prints_the_plan_and_makes_no_call() {
  setup_case dry
  run27_default --dry-run
  expect_status "$status" 0 "a dry run"
  expect_no_calls "a dry run makes no call at all"
  expect_contains "$out" "FACT dry-run: PASS" "the dry-run row"
  expect_contains "$(fact_line restore)" "nothing was created and nothing was read" "the dry run's Restore row"
  local key
  for key in project billing-account belong identity config-root dry-run; do
    expect_contains "$out" "FACT $key:" "the dry run's own $key row"
  done
  expect_absent "$out" "FACT wizard:" "the dry run's block carries no step rows"
  expect_absent "$out" "FAIL —" "a dry run voices no verdict about the steps it did not run"
  expect_contains "$(cat "$dir/err")" "NOTHING below was run" "the plan is printed"
  expect_contains "$(cat "$dir/err")" "gcloud projects delete $PROJECT --quiet" "the Restore's own plan"
}

test_a_dry_run_still_refuses_belong() {
  setup_case dry-belong
  run27_default --project belong --dry-run
  expect_status "$status" 2 "a dry run against belong"
  expect_contains "$(fact_line belong)" "FAIL" "the belong guard runs in a dry run too"
  expect_contains "$out" "FACT aborted: FAIL" "the dry run stopped at the guard"
  expect_no_calls "a refused dry run makes no call"
}

test_a_dry_run_that_is_refused_records_no_dry_run_row() {
  setup_case dry-agent
  RUN27_ENV=(CLAUDECODE=1)
  run27_default --dry-run
  expect_status "$status" 2 "a dry run in an agent's session"
  expect_contains "$(fact_line agent-session)" "FAIL" "the agent guard"
  expect_absent "$out" "FACT dry-run: PASS" "a refused dry run is not a dry run that happened"
}

test_a_plan_only_run_that_creates_the_project_is_a_fail() {
  setup_case plan-creates
  RUN27_ENV=(FAKE27_PLAN_CREATES=1)
  run27_default
  expect_status "$status" 2 "a plan-only run that created the project"
  expect_contains "$(fact_line plan-only)" "FAIL" "the plan-only row"
  expect_contains "$(fact_line plan-only)" "creates nothing" "why it is a failure"
}

test_a_plan_only_run_that_prints_no_stages_is_a_fail() {
  setup_case plan-nostages
  RUN27_ENV=(FAKE27_PLAN_NO_STAGES=1)
  run27_default
  expect_status "$status" 2 "a plan-only run with no stage list"
  expect_contains "$(fact_line plan-only)" "FAIL" "the plan-only row"
  expect_contains "$(fact_line plan-only)" "stage" "the row names the stage list"
}

test_a_plan_only_that_blocks_is_killed_and_fails() {
  setup_case plan-blocks
  RUN27_ENV=(FAKE27_PLAN_BLOCKS=1)
  PLAN_SECONDS=2
  run27_default
  # A --plan-only run takes every input from its flags and asks nothing, so a
  # wait in it is a finding: the run is killed and the row says so.
  expect_status "$status" 2 "a plan-only run that blocked"
  expect_contains "$(fact_line plan-only)" "FAIL" "the plan-only row"
  expect_contains "$(fact_line plan-only)" "blocked waiting for an answer" "the row says what a wait means"
}

test_the_negative_that_is_not_refused_is_a_fail() {
  setup_case neg-zero
  RUN27_ENV=(FAKE27_NEG_EXIT=0)
  run27_default
  expect_status "$status" 2 "a negative run that exited 0"
  expect_contains "$(fact_line negative)" "FAIL" "the negative row"
  expect_contains "$(fact_line negative)" "must be REFUSED" "the row names what step 2 expects"
}

test_the_negative_that_created_the_id_is_a_fail() {
  setup_case neg-creates
  RUN27_ENV=(FAKE27_NEG_CREATES=1 FAKE27_NEG_EXIT=1)
  run27_default
  expect_status "$status" 2 "a negative run that created the id"
  expect_contains "$(fact_line negative)" "FAIL" "the negative row"
  expect_contains "$(fact_line negative)" "EXISTS" "the row says the id exists"
}

test_a_create_that_never_asks_the_project_id_is_a_fail() {
  setup_case create-noquestion
  RUN27_ENV=(FAKE27_ASK_NOTHING=1)
  run27_default
  expect_status "$status" 2 "a create run that never asked"
  expect_contains "$(fact_line wizard)" "FAIL" "the wizard row"
  expect_contains "$(fact_line wizard)" "without ever asking" "the row names the question that was never asked"
}

test_a_create_that_blocks_is_killed_and_fails() {
  setup_case create-blocks
  RUN27_ENV=(FAKE27_BLOCK=1)
  CREATE_SECONDS=2
  run27_default
  expect_status "$status" 2 "a create run that blocked"
  expect_contains "$(fact_line wizard)" "FAIL" "the wizard row"
  expect_contains "$(fact_line wizard)" "was killed" "the row says it was killed"
  expect_contains "$(fact_line restore)" "PASS" "the Restore still runs"
}

test_a_create_that_fails_stops_and_nothing_was_created() {
  setup_case create-fails
  RUN27_ENV=(FAKE27_CREATE_FAILS=1)
  run27_default
  expect_status "$status" 2 "a create run that failed"
  expect_contains "$(fact_line wizard)" "FAIL" "the wizard row"
  # The row quotes the transcript, and the transcript holds the wizard's own
  # billing banner, which names the account: whatever line the row quotes, the
  # block stays safe to paste back.
  expect_absent "$out" "$ACCOUNT" "the block"
  expect_contains "$(fact_line restore)" "PASS" "the Restore with nothing to delete"
  expect_contains "$(fact_line restore)" "nothing of this check's was created" "nothing was created to delete"
  expect_absent "$(calls_gcloud)" "projects delete" "the Restore never deletes a project that is not there"
}

test_a_create_that_half_created_is_reported_by_the_restore() {
  setup_case create-half
  RUN27_ENV=(FAKE27_CREATE_FAILS=1 FAKE27_CREATE_FAILS_HALF=1 "FAKE27_FAIL_ID=$PROJECT")
  run27_default
  expect_status "$status" 2 "a create run that failed after making the project"
  expect_contains "$(fact_line restore)" "FAIL" "the Restore row"
  expect_contains "$(fact_line restore)" "gcloud projects delete $PROJECT --quiet" "the row names the command to finish by hand"
}

test_a_billing_link_to_another_account_is_a_fail() {
  setup_case billing-other
  RUN27_ENV=(FAKE27_ACCOUNT=FFFFFF-FFFFFF-FFFFFF)
  run27_default
  expect_status "$status" 2 "a link to another account"
  expect_contains "$(fact_line billing)" "FAIL" "the billing row"
  expect_contains "$(fact_line billing)" "did not link" "why it is a failure"
  expect_contains "$out" "FACT aborted" "the run stopped at the billing read"
}

# The billing row is the one row that QUOTES the account's own name, straight
# out of the read-back gcloud answered with: the block is what you paste back, so
# it goes in as the section's placeholder and the terminal's own lines with it.
# Without redact() this row is the leak, and this case is what catches it.
test_a_billing_readback_is_redacted_in_the_block() {
  setup_case billing-disabled
  RUN27_ENV=(FAKE27_BILLING_DISABLED=1)
  run27_default
  expect_status "$status" 2 "a project whose billing is not enabled"
  expect_contains "$(fact_line billing)" "FAIL" "the billing row"
  expect_contains "$(fact_line billing)" "<ACCOUNT_ID>" "the read-back is quoted as the placeholder"
  expect_absent "$out" "$ACCOUNT" "the block"
  expect_absent "$(cat "$dir/err")" "billingAccounts/$ACCOUNT" "the lines this script writes to your terminal"
}

test_a_firestore_location_that_is_not_the_region_is_a_fail() {
  setup_case firestore-other
  RUN27_ENV=(FAKE27_FIRESTORE=europe-west4)
  run27_default
  expect_status "$status" 2 "a Firestore location that is not the region"
  expect_contains "$(fact_line firestore)" "FAIL" "the firestore row"
  expect_contains "$(fact_line firestore)" "permanent" "the row says the location cannot be changed"
}

test_a_missing_bucket_stops_the_installation_row() {
  setup_case no-bucket
  RUN27_ENV=(FAKE27_NO_BUCKET=1)
  run27_default
  expect_status "$status" 2 "a project with no runs bucket"
  expect_contains "$(fact_line installation)" "FAIL" "the installation row"
  expect_contains "$(fact_line installation)" "fugaro-runs-$PROJECT" "the row names the bucket"
}

test_a_missing_job_stops_the_installation_row() {
  setup_case no-job
  RUN27_ENV=(FAKE27_NO_JOB=1)
  run27_default
  expect_status "$status" 2 "a project with no history job"
  expect_contains "$(fact_line installation)" "FAIL" "the installation row"
  expect_contains "$(fact_line installation)" "fugarohist" "the row names the job"
}

test_a_missing_image_is_a_fail() {
  setup_case no-image
  RUN27_ENV=(FAKE27_NO_IMAGE=1)
  run27_default
  expect_status "$status" 2 "a registry with no history image"
  expect_contains "$(fact_line images)" "FAIL" "the images row"
  expect_contains "$(fact_line images)" "history" "the row names the image"
}

test_doctor_that_does_not_name_the_project_is_a_fail() {
  setup_case doctor-other
  RUN27_ENV=(FAKE27_DOCTOR_PROJECT=another-project-9f2a)
  run27_default
  expect_status "$status" 2 "doctor resolving another project"
  expect_contains "$(fact_line doctor)" "FAIL" "the doctor row"
  expect_contains "$(fact_line doctor)" "(GCP $PROJECT," "the row names the project doctor should have resolved"
  expect_contains "$out" "sixth stop" "the run names the section's sixth stop as the read that failed"
}

test_doctor_that_exits_nonzero_is_a_fail() {
  setup_case doctor-fails
  RUN27_ENV=(FAKE27_DOCTOR_FAILS=1)
  run27_default
  expect_status "$status" 2 "doctor exiting 1"
  expect_contains "$(fact_line doctor)" "FAIL" "the doctor row"
  expect_contains "$(fact_line doctor)" "fix" "the row says what doctor printed"
}

test_an_update_that_still_has_work_is_a_fail() {
  setup_case update-work
  RUN27_ENV=(FAKE27_UPDATE2_WORK=1)
  run27_default
  expect_status "$status" 2 "a second update with work left"
  expect_contains "$(fact_line update)" "FAIL" "the update row"
  expect_contains "$(fact_line update)" "No changes" "the row names step 4's assertion"
}

test_an_update_that_fails_is_a_fail() {
  setup_case update-fails
  RUN27_ENV=(FAKE27_UPDATE_FAILS=1)
  run27_default
  expect_status "$status" 2 "an update that failed"
  expect_contains "$(fact_line update)" "FAIL" "the update row"
  expect_contains "$(fact_line update)" "first fugaro update exited 1" "the row names which run failed"
}

test_the_restore_deletes_the_jobs_the_buckets_and_the_project() {
  setup_case restore
  run27_default
  expect_status "$status" 0 "a full run"
  expect_contains "$(calls_gcloud)" "run jobs delete fugarohist" "the job is deleted"
  expect_contains "$(calls_gcloud)" "storage rm --recursive gs://fugaro-runs-$PROJECT" "the runs bucket is deleted"
  expect_contains "$(calls_gcloud)" "storage rm --recursive gs://fugaro-state-$PROJECT" "the state bucket is deleted"
  expect_contains "$(calls_gcloud)" "projects delete $PROJECT --quiet" "the project's own delete"
  expect_contains "$(calls_gcloud)" "secrets list --project=$PROJECT" "the secrets are listed"
  # The order that matters: the secrets are read BEFORE the project's delete, so
  # the names are evidence and not a reading of something already gone.
  local sec del
  sec=$(grep -n "secrets list" "$dir/state/calls.gcloud" | head -1 | cut -d: -f1)
  del=$(grep -n "projects delete" "$dir/state/calls.gcloud" | head -1 | cut -d: -f1)
  if [ -z "$sec" ] || [ -z "$del" ] || [ "$sec" -gt "$del" ]; then
    fail "the secrets were not listed before the project's delete ($sec vs $del)"
  fi
  expect_contains "$(fact_line restore)" "github-app-key" "the secret names are recorded"
  expect_contains "$(fact_line restore)" "0 bucket" "no bucket of the check's is left"
}

test_a_job_the_restore_cannot_delete_is_a_fail() {
  setup_case restore-job
  RUN27_ENV=(FAKE27_JOB_REFUSES_DELETE=1)
  run27_default
  expect_status "$status" 1 "a run whose Restore could not delete a job"
  expect_contains "$(fact_line restore)" "FAIL" "the restore row"
  expect_contains "$(fact_line restore)" "would not go" "the row says what is left"
  expect_contains "$(fact_line wizard)" "PASS" "the wizard itself held"
}

test_a_bucket_the_restore_cannot_delete_is_a_fail() {
  setup_case restore-bucket
  RUN27_ENV=(FAKE27_BUCKET_REFUSES_DELETE=1)
  run27_default
  expect_status "$status" 1 "a run whose Restore could not delete a bucket"
  expect_contains "$(fact_line restore)" "FAIL" "the restore row"
  expect_contains "$(fact_line restore)" "fugaro-runs-$PROJECT" "the row names the bucket"
}

test_a_project_whose_delete_was_refused_is_a_fail() {
  setup_case restore-project
  RUN27_ENV=(FAKE27_PROJECT_DELETE_REFUSED=1)
  run27_default
  expect_status "$status" 1 "a run whose project delete was refused"
  expect_contains "$(fact_line restore)" "FAIL" "the restore row"
  expect_contains "$(fact_line restore)" "finish by hand" "the row tells you to delete it by hand"
}

test_an_agent_session_is_refused() {
  setup_case agent
  RUN27_ENV=(CLAUDECODE=1)
  run27_default
  expect_status "$status" 2 "a coding agent's session"
  expect_contains "$(fact_line agent-session)" "FAIL" "the agent guard"
  expect_contains "$(fact_line agent-session)" "CLAUDECODE" "the row names the marker it found"
  expect_no_calls "an agent's session runs nothing"
}

test_a_shell_that_is_tracing_is_refused() {
  setup_case xtrace
  RUN27_XTRACE=1
  run27_default
  expect_status "$status" 2 "a shell started with -x"
  expect_contains "$(fact_line xtrace)" "FAIL" "the xtrace guard"
  expect_no_calls "a tracing shell runs nothing"
}

test_the_config_root_is_left_in_place_and_named() {
  setup_case root
  run27_default
  expect_status "$status" 0 "a full run"
  [ -f "$dir/root/vars.env" ] || fail "the check did not record its config root in vars.env"
  expect_contains "$(cat "$dir/root/vars.env")" "C=$dir/root/config" "the recorded root"
  [ -d "$dir/root/config" ] || fail "the config root is not there after the run"
  expect_contains "$(fact_line config-root)" "PASS" "the config-root row"
  expect_contains "$(cat "$dir/err")" "rm -rf $dir/root" "the line that removes it is printed for the section's own steps"
  # The scratch directory does NOT stay: it holds the transcripts and goes with
  # the run.
  local left
  left=$(find "$dir/tmp" -maxdepth 1 -name 'chk27.*' -print 2>/dev/null | head -1)
  [ -z "$left" ] || fail "the scratch directory is still there: $left"
}

test_a_config_root_the_shell_already_points_elsewhere_is_refused() {
  setup_case root-other
  RUN27_ENV=(XDG_CONFIG_HOME=/tmp/somewhere-else)
  run27_default
  expect_status "$status" 2 "a shell already pointed at another config root"
  expect_contains "$out" "XDG_CONFIG_HOME" "the row names the variable"
  expect_no_calls "nothing runs when the config root is not this check's"
}

test_a_vars_env_naming_another_root_is_refused() {
  setup_case root-recorded
  # The section's own third stop: a hand-edited or tampered vars.env must never
  # point the check at the machine's own config root.
  mkdir -p "$dir/root"
  printf 'C=/tmp/the-machine\n' >"$dir/root/vars.env"
  run27_default
  expect_status "$status" 2 "a vars.env recording another root"
  expect_contains "$out" "C=/tmp/the-machine" "the row names what the file records"
  expect_contains "$out" "delete $dir/root" "the row says to start again"
  expect_no_calls "nothing runs when the recorded root is not this check's"
}

test_the_negative_uses_a_config_root_of_its_own() {
  setup_case neg-root
  run27_default
  expect_status "$status" 0 "a full run"
  local neg create
  neg=$(grep -- '--yes' "$dir/state/calls.fugaro" | head -1)
  create=$(grep 'setup --name' "$dir/state/calls.fugaro" | head -1)
  expect_contains "$create" "xdg=$dir/root/config" "the create run resolves the check's own root"
  expect_absent "$neg" "xdg=$dir/root/config" "the negative run never reads the check's root"
  expect_contains "$neg" "xdg=$dir/tmp/" "the negative run's root is the scratch's own"
}

test_usage_and_arguments() {
  setup_case usage
  run27
  expect_status "$status" 2 "no arguments"
  expect_contains "$(cat "$dir/err")" "usage:" "the usage line"
  setup_case usage2
  run27 --project "$PROJECT"
  expect_status "$status" 2 "--project alone"
  expect_contains "$(cat "$dir/err")" "--billing-account is required" "the missing flag is named"
  setup_case usage3
  run27 --project "$PROJECT" --billing-account "$ACCOUNT"
  expect_status "$status" 2 "--repo missing"
  expect_contains "$(cat "$dir/err")" "--repo is required" "the missing flag is named"
  setup_case usage4
  run27 --project "$PROJECT" --billing-account "$ACCOUNT" --repo "$REPO" --nope
  expect_status "$status" 2 "an unknown flag"
  expect_contains "$(cat "$dir/err")" "unknown argument" "the unknown flag is named"
  setup_case usage5
  run27_default --help
  expect_status "$status" 0 "--help"
  expect_absent "$out" "CHECK 27 RESULT" "--help prints no block"
  expect_contains "$out" "usage:" "the usage line"
  expect_no_calls "--help calls nothing"
}

test_a_region_that_is_not_one_is_refused() {
  setup_case region
  run27 --project "$PROJECT" --billing-account "$ACCOUNT" --repo "$REPO" --region nowhere
  expect_status "$status" 2 "a region that is not one"
  expect_contains "$(cat "$dir/err")" "does not look like a region" "the refusal names the shape"
  expect_no_calls "nothing runs with a region that is not one"
}

test_an_account_of_the_wrong_shape_is_refused() {
  setup_case account
  run27 --project "$PROJECT" --billing-account 'not-an-account' --repo "$REPO"
  expect_status "$status" 2 "an account ID of the wrong shape"
  expect_contains "$out" "account ID such as" "the refusal names the shape it wants"
  expect_contains "$(fact_line billing-account)" "FAIL" "the account row"
  expect_absent "$out" "not-an-account" "the value is not repeated in the block"
  expect_no_calls "nothing runs with an account that is not one"
}

# ================================================================= registration
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

t test_a_full_run_passes
t test_the_create_run_is_given_a_real_terminal
t test_the_account_id_is_the_wizards_own_line_and_not_the_scripts
t test_belong_is_refused
t test_belong_is_refused_before_any_call
t test_the_name_must_be_fugaro
t test_a_project_that_already_exists_stops
t test_a_dev_build_is_refused
t test_a_dry_run_prints_the_plan_and_makes_no_call
t test_a_dry_run_still_refuses_belong
t test_a_dry_run_that_is_refused_records_no_dry_run_row
t test_a_plan_only_run_that_creates_the_project_is_a_fail
t test_a_plan_only_run_that_prints_no_stages_is_a_fail
t test_a_plan_only_that_blocks_is_killed_and_fails
t test_the_negative_that_is_not_refused_is_a_fail
t test_the_negative_that_created_the_id_is_a_fail
t test_a_create_that_never_asks_the_project_id_is_a_fail
t test_a_create_that_blocks_is_killed_and_fails
t test_a_create_that_fails_stops_and_nothing_was_created
t test_a_create_that_half_created_is_reported_by_the_restore
t test_a_billing_link_to_another_account_is_a_fail
t test_a_billing_readback_is_redacted_in_the_block
t test_a_firestore_location_that_is_not_the_region_is_a_fail
t test_a_missing_bucket_stops_the_installation_row
t test_a_missing_job_stops_the_installation_row
t test_a_missing_image_is_a_fail
t test_doctor_that_does_not_name_the_project_is_a_fail
t test_doctor_that_exits_nonzero_is_a_fail
t test_an_update_that_still_has_work_is_a_fail
t test_an_update_that_fails_is_a_fail
t test_the_restore_deletes_the_jobs_the_buckets_and_the_project
t test_a_job_the_restore_cannot_delete_is_a_fail
t test_a_bucket_the_restore_cannot_delete_is_a_fail
t test_a_project_whose_delete_was_refused_is_a_fail
t test_an_agent_session_is_refused
t test_a_shell_that_is_tracing_is_refused
t test_the_config_root_is_left_in_place_and_named
t test_a_config_root_the_shell_already_points_elsewhere_is_refused
t test_a_vars_env_naming_another_root_is_refused
t test_the_negative_uses_a_config_root_of_its_own
t test_usage_and_arguments
t test_a_region_that_is_not_one_is_refused
t test_an_account_of_the_wrong_shape_is_refused

if [ "$failures" -ne 0 ]; then
  echo "$failures test(s) failed" >&2
  exit 1
fi
echo "check27_test.sh: all tests passed"
