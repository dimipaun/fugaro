#!/usr/bin/env bash
# Tests for scripts/live-checks/check-390.sh, against a fake `fugaro` and a fake
# `gcloud` on PATH: nothing here reaches the network, there is no cloud project,
# no image is built, no job is redeployed and no agent runs any live step of the
# check.
#
# The fakes model the calls the check makes and nothing else. The fake `fugaro`
# keeps a catalog in the case's own directory (so `image ls` really answers
# differently before the add, after it and after the teardown), records what its
# own stdin turned out to be, and plays the modes a case sets for `doctor`,
# `image add`, `image rm` and `apply --status`. The fake `gcloud` answers
# `run jobs list` and `artifacts docker images describe` out of the same
# directory, so a case can say what the jobs run and what digest the image
# resolves to.
#
# Usage: bash scripts/live-checks/check-390_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-390.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK390_TEST_KEEP:-}" ]; then
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
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT (claim|add|job|digest|status|teardown|aborted):' | sed 's/^/ ... last run: /')"
}

# Every fact key the run's block carries, whatever it measured.
BLOCK_KEYS=(xtrace agent-session checkout installation belong entry add record job registry digest status teardown claim)

# The installation every case runs against: its project is not `belong`.
PROJECT=sbx
GCP=sbx-proj
REGION=europe-west1
REPO=acme/app
ENTRY=chk390-catalog
DOCKERFILE=acme/app:Dockerfile
# Two digests, so a case can make the record and the registry disagree.
DIGEST=sha256:1111111111111111111111111111111111111111111111111111111111111111
OTHER=sha256:2222222222222222222222222222222222222222222222222222222222222222

# --------------------------------------------------------------------- the fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for check-390.sh's test only. What `doctor` answers,
# which mode `image add` and `image rm` play and what `apply --status` says is
# owed come from files in the case's own directory; the catalog `image ls`
# renders is a file there too, so the three reads the check makes (before the
# add, after it, after the teardown) really differ.
set -euo pipefail
dir=${FAKE390_DIR:?FAKE390_DIR is not set}
catalog=$dir/catalog
: >"$catalog.tmp"
mode_of() { if [ -f "$dir/$1.mode" ]; then cat "$dir/$1.mode"; else printf 'ok\n'; fi; }

# render_catalog: the JSON `image ls` answers, one row per line of the catalog
# file, each `name|digest`.
render_catalog() {
  local first=yes line name digest
  printf '{"project":"%s","entries":[' "${FAKE390_PROJECT:-sbx}"
  while IFS='|' read -r name digest; do
    [ -n "$name" ] || continue
    if [ "$first" = yes ]; then first=no; else printf ','; fi
    printf '{"name":"%s","source":"a Dockerfile in acme/app","digest":"%s"}' "$name" "$digest"
  done <"$catalog"
  printf ']}\n'
}

# stdin_kind: what this call's stdin turned out to be, for the case that reads
# whether the check closed it. A read on /dev/null returns EOF at once; a read
# that blocks past two seconds says the stdin is an open terminal or pipe.
stdin_kind() {
  if IFS= read -r -t 2 _line; then printf 'line'; else
    rc=$?
    if [ "$rc" -gt 128 ]; then printf 'open'; else printf 'closed'; fi
  fi
}

case "${1:-}" in
doctor)
  printf 'fugaro %s\n' "$*" >>"$dir/calls"
  case "$(mode_of doctor)" in
  fail)
    printf 'Error: the installation could not be read\n' >&2
    exit 1
    ;;
  belong)
    printf '{"ok":true,"project":{"name":"belong","gcp_project":"belong-proj","region":"%s"}}\n' "${FAKE390_REGION:-europe-west1}"
    ;;
  *)
    printf '{"ok":true,"project":{"name":"%s","gcp_project":"%s","region":"%s"}}\n' \
      "${FAKE390_PROJECT:-sbx}" "${FAKE390_GCP:-sbx-proj}" "${FAKE390_REGION:-europe-west1}"
    ;;
  esac
  ;;
image)
  case "${2:-}" in
  ls)
    printf 'fugaro %s\n' "$*" >>"$dir/calls"
    case "$(mode_of ls)" in
    fail)
      printf 'Error: the catalog could not be read\n' >&2
      exit 1
      ;;
    *) render_catalog ;;
    esac
    ;;
  add)
    printf 'fugaro %s (stdin=%s)\n' "$*" "$(stdin_kind)" >>"$dir/calls"
    case "$(mode_of add)" in
    fail)
      printf 'Error: the build failed: the Dockerfile was not found\n' >&2
      exit 1
      ;;
    silent) exit 0 ;;
    *)
      printf '%s|%s\n' "${3:-}" "${FAKE390_DIGEST:-$FAKE390_DEFAULT_DIGEST}" >>"$catalog"
      printf 'Image %s added and now available to your jobs.\n' "${3:-}"
      ;;
    esac
    ;;
  rm)
    printf 'fugaro %s\n' "$*" >>"$dir/calls"
    case "$(mode_of rm)" in
    fail)
      printf 'Error: the entry could not be removed\n' >&2
      exit 1
      ;;
    *)
      grep -v "^${3:-}|" "$catalog" >"$catalog.tmp" 2>/dev/null || true
      mv "$catalog.tmp" "$catalog"
      printf 'Image %s removed.\n' "${3:-}"
      ;;
    esac
    ;;
  *) printf 'fake fugaro: unexpected call: %s\n' "$*" >&2; exit 64 ;;
  esac
  ;;
apply)
  printf 'fugaro %s\n' "$*" >>"$dir/calls"
  case "$(mode_of status)" in
  owed)
    printf '{"version":1,"project":"%s","gcp_project":"%s","recorded":true,"current":true,"pending":[{"step":"policy","kind":"resource","key":"image"}]}\n' \
      "${FAKE390_PROJECT:-sbx}" "${FAKE390_GCP:-sbx-proj}"
    ;;
  *)
    printf '{"version":1,"project":"%s","gcp_project":"%s","recorded":true,"current":true,"pending":[]}\n' \
      "${FAKE390_PROJECT:-sbx}" "${FAKE390_GCP:-sbx-proj}"
    ;;
  esac
  ;;
*)
  printf 'fake fugaro: unexpected call: %s\n' "$*" >&2
  exit 64
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"

  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for check-390.sh's test only: `run jobs list` renders
# the jobs of the case's own directory (one per line, `name|image|label|remote`)
# and `artifacts docker images describe` answers the digest a case sets.
set -euo pipefail
dir=${FAKE390_DIR:?FAKE390_DIR is not set}
mode_of() { if [ -f "$dir/$1.mode" ]; then cat "$dir/$1.mode"; else printf 'ok\n'; fi; }
case "${1:-}" in
run)
  printf 'gcloud %s\n' "$*" >>"$dir/calls"
  case "$(mode_of jobs)" in
  fail)
    printf 'ERROR: (gcloud.run.jobs.list) the caller does not have permission\n' >&2
    exit 1
    ;;
  none) printf '[]\n' ;;
  *)
    printf '['
    first=yes
    while IFS='|' read -r name image label remote entry; do
      [ -n "$name" ] || continue
      if [ "$first" = yes ]; then first=no; else printf ','; fi
      # The real shape of `gcloud run jobs list` (v1): an env entry's value is
      # the string itself, not a nested object. A fake that wrapped it would
      # hide a reader that reaches through `value.value` and raises on a real
      # job, which empties the whole reading. FUGARO_IMAGE_ENTRY is the deploy's
      # own stamp of the entry the job's image came from, empty on a job the
      # catalog chose no image for.
      printf '{"metadata":{"name":"%s","labels":{"fugaro_repo":"%s"}},"spec":{"template":{"spec":{"template":{"spec":{"containers":[{"image":"%s","env":[{"name":"FUGARO_REMOTE","value":"%s"},{"name":"FUGARO_IMAGE_ENTRY","value":"%s"}]}]}}}}}}' \
        "$name" "$label" "$image" "$remote" "$entry"
    done <"$dir/jobs"
    printf ']\n'
    ;;
  esac
  ;;
artifacts)
  printf 'gcloud %s\n' "$*" >>"$dir/calls"
  case "$(mode_of registry)" in
  fail)
    printf 'ERROR: (gcloud.artifacts.docker.images.describe) NOT_FOUND\n' >&2
    exit 1
    ;;
  nosha) printf '{"image_summary":{"fully_qualified_digest":"example.com/x/y:latest"}}\n' ;;
  *)
    reg=${FAKE390_REGISTRY_DIGEST:-${FAKE390_DEFAULT_DIGEST:-}}
    printf '{"image_summary":{"digest":"%s","fully_qualified_digest":"example.com/x@%s"}}\n' "$reg" "$reg"
    ;;
  esac
  ;;
*)
  printf 'fake gcloud: unexpected call: %s\n' "$*" >&2
  exit 64
  ;;
esac
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

# ------------------------------------------------------------------- harness
setup_case() { # name
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp" "$dir/home" "$dir/repo"
  write_fakes "$dir"
  : >"$dir/state/calls"
  # The project's catalog holds one entry, the one the repository's job runs:
  # what a healthy installation looks like. The entry this check adds is a second
  # one, which nothing selects — an entry a job selects is one `fugaro image rm`
  # refuses to remove, so the check's own entry can never be the one a job runs.
  printf 'base|%s\n' "$DIGEST" >"$dir/state/catalog"
  # The jobs of the repository the checkout belongs to: one, carrying the
  # deploy's own stamp of the catalog entry its image was deployed from
  # (FUGARO_IMAGE_ENTRY), which is what the job row holds to the catalog.
  printf 'fugaro-acme-app-run|%s|acme-app-0123456789abcdef|https://github.com/acme/app.git|base\n' \
    "${REGION}-docker.pkg.dev/${GCP}/fugaro-shared/catalog-base:latest" >"$dir/state/jobs"
  git -C "$dir/repo" init -q
  git -C "$dir/repo" remote add origin "https://github.com/${REPO}.git"
  status=0
  out=""
  err=""
  scratch=""
  RUN390_ARGS=()
  RUN390_ENV=()
  RUN390_BASH=(bash)
}

run390() {
  status=0
  # env -i: the case's environment is the whole environment, so no marker of the
  # suite that started this file can decide the check's own agent-session guard.
  out=$(cd "$dir/repo" && env -i PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE390_DIR="$dir/state" FAKE390_PROJECT="$PROJECT" FAKE390_GCP="$GCP" FAKE390_REGION="$REGION" \
    FAKE390_DEFAULT_DIGEST="$DIGEST" \
    ${RUN390_ENV[@]+"${RUN390_ENV[@]}"} \
    "${RUN390_BASH[@]}" "$script" ${RUN390_ARGS[@]+"${RUN390_ARGS[@]}"} "$@" 2>"$dir/err") || status=$?
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  RUN390_ARGS=()
  RUN390_ENV=()
  RUN390_BASH=(bash)
}
run390_default() { run390 --dockerfile "$DOCKERFILE" "$@"; }

fact_line() { # key
  printf '%s\n' "$out" | grep -m1 "^FACT $1:" || true
}

expect_block_keys() { # what
  local key
  expect_contains "$out" "CHECK 390 RESULT" "$1: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$out" "FACT $key:" "$1: the $key row"
  done
  expect_contains "$out" "CHECK 390 RESULT END (exit " "$1: the block's end"
}

calls() { cat "$dir/state/calls"; }

expect_no_calls() { # what
  if [ -s "$dir/state/calls" ]; then
    fail "$1: a call was made: $(tr '\n' ';' <"$dir/state/calls")"
  fi
}

expect_call() { # what needle
  expect_contains "$(calls)" "$2" "$1"
}
expect_no_call() { # what needle
  expect_absent "$(calls)" "$2" "$1"
}

assert_scratch_removed() { # what
  if [ -z "$scratch" ]; then
    fail "$1: the check named no scratch directory"
    return 0
  fi
  [ -d "$scratch" ] && fail "$1: the scratch directory $scratch was NOT removed"
  return 0
}

# mode NAME VALUE writes a knob the fakes read.
mode() { # mode name value
  printf '%s\n' "$2" >"$dir/state/$1.mode"
}

# -------------------------------------------------------------------- the cases
test_help() {
  setup_case help
  run390 --help
  expect_status "$status" 0 "a help run"
  expect_contains "$out" "usage: check-390.sh" "a help run prints the usage"
  expect_absent "$out" "FACT " "a help run prints no block"
  expect_no_calls "a help run"
}

test_a_dry_run_prints_the_plan_and_makes_no_call() {
  setup_case dry
  run390_default --dry-run
  expect_status "$status" 0 "a dry run"
  expect_block_keys "a dry run"
  expect_contains "$(fact_line claim)" "planned" "a dry run says the verdict is a real run's"
  expect_contains "$(fact_line add)" "BUILDS an image" "a dry run says the add builds"
  expect_no_calls "a dry run"
  assert_scratch_removed "a dry run"
}

test_a_dry_run_still_refuses_belong() {
  setup_case dry-belong
  run390_default --dry-run --project belong
  expect_status "$status" 2 "a dry run pointed at belong"
  expect_contains "$(fact_line belong)" "FAIL" "the belong row"
  expect_contains "$(fact_line belong)" "belong" "the belong row names what it refused"
  expect_no_calls "a dry run pointed at belong"
}

test_a_missing_dockerfile_is_refused() {
  setup_case nodockerfile
  run390 --entry "$ENTRY"
  expect_status "$status" 2 "a run with no --dockerfile"
  expect_contains "$err" "--dockerfile" "the refusal names the flag"
  expect_no_calls "a run with no --dockerfile"
}

test_the_happy_path_passes() {
  setup_case happy
  run390_default
  expect_status "$status" 0 "the happy path"
  expect_block_keys "the happy path"
  expect_contains "$(fact_line add)" "PASS" "the add row"
  expect_contains "$(fact_line record)" "$DIGEST" "the record row names the digest"
  expect_contains "$(fact_line job)" "carries the catalog's own stamp" "the job row"
  expect_contains "$(fact_line job)" "entry=base" "the job row names the entry each job's image came from"
  expect_contains "$(fact_line registry)" "$DIGEST" "the registry row names the digest"
  expect_contains "$(fact_line digest)" "PASS" "the digest row"
  expect_contains "$(fact_line digest)" "base" "the digest row names the entry the job's image came from"
  expect_contains "$(fact_line status)" "owes nothing" "the status row"
  expect_contains "$(fact_line teardown)" "PASS" "the teardown row"
  expect_contains "$(fact_line claim)" "PASS" "the claim row"
  expect_call "the add ran" "image add ${ENTRY} --dockerfile ${DOCKERFILE}"
  expect_call "the teardown ran" "image rm ${ENTRY}"
  expect_call "the jobs were listed" "run jobs list"
  expect_call "the registry was read" "artifacts docker images describe"
  assert_scratch_removed "the happy path"
}

test_the_add_runs_with_its_stdin_closed() {
  setup_case stdin
  run390_default
  expect_status "$status" 0 "the happy path"
  expect_call "the add's own stdin" "image add ${ENTRY} --dockerfile ${DOCKERFILE} (stdin=closed)"
}

test_a_failing_add_is_a_fail() {
  setup_case addfail
  mode add fail
  run390_default
  expect_status "$status" 1 "an add that failed"
  expect_contains "$(fact_line add)" "FAIL" "the add row"
  expect_contains "$(fact_line add)" "not found" "the add row quotes the run's own line"
  expect_contains "$(fact_line claim)" "FAIL" "the claim row"
  # The teardown still runs: whatever the command may have written before it
  # failed is taken back out.
  expect_call "the teardown ran anyway" "image rm ${ENTRY}"
}

test_an_add_that_prints_nothing_is_a_fail() {
  setup_case addsilent
  mode add silent
  run390_default
  expect_status "$status" 1 "an add that printed nothing"
  expect_contains "$(fact_line add)" "printed none of its own success line" "the add row"
  expect_contains "$(fact_line claim)" "FAIL" "the claim row"
}

test_an_entry_the_add_left_out_of_the_catalog_is_a_fail() {
  setup_case missingrecord
  # A fake whose add answers its success line and writes nothing.
  cat >"$dir/bin/fugaro" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
dir=${FAKE390_DIR:?}
printf 'fugaro %s\n' "$*" >>"$dir/calls"
case "${1:-}" in
image)
  case "${2:-}" in
  add) printf 'Image %s added and now available to your jobs.\n' "$3" ;;
  rm) printf 'Image %s removed.\n' "$3" ;;
  ls) printf '{"project":"sbx","entries":[]}\n' ;;
  esac
  ;;
doctor) printf '{"ok":true,"project":{"name":"sbx","gcp_project":"sbx-proj","region":"europe-west1"}}\n' ;;
apply) printf '{"version":1,"pending":[]}\n' ;;
esac
EOF
  chmod +x "$dir/bin/fugaro"
  run390_default
  expect_status "$status" 1 "an entry the add never wrote"
  expect_contains "$(fact_line add)" "PASS" "the add row"
  expect_contains "$(fact_line record)" "FAIL" "the record row"
  expect_contains "$(fact_line claim)" "FAIL" "the claim row"
  expect_contains "$(fact_line claim)" "record" "the claim row names the row that failed"
}

test_a_digest_that_does_not_match_is_a_fail() {
  setup_case digestmismatch
  RUN390_ENV=(FAKE390_REGISTRY_DIGEST="$OTHER")
  run390_default
  expect_status "$status" 1 "a registry digest that is not the record's"
  expect_contains "$(fact_line digest)" "FAIL" "the digest row"
  expect_contains "$(fact_line digest)" "$OTHER" "the digest row names what the image resolves to"
  expect_contains "$(fact_line claim)" "FAIL" "the claim row"
}

test_a_job_stamped_with_an_entry_the_catalog_does_not_hold_is_a_fail() {
  setup_case strayjob
  # A job whose deploy stamped it with an entry the project's catalog no longer
  # holds: the redeploy left it on an image no catalog describes.
  printf 'fugaro-acme-app-run|%s|acme-app-0123456789abcdef|https://github.com/acme/app.git|gone\n' \
    "${REGION}-docker.pkg.dev/${GCP}/fugaro-shared/catalog-gone:latest" >"$dir/state/jobs"
  run390_default
  expect_status "$status" 1 "a job stamped with an entry the catalog does not hold"
  expect_contains "$(fact_line job)" "FAIL" "the job row"
  expect_contains "$(fact_line job)" "gone" "the job row names the entry the catalog does not hold"
  expect_contains "$(fact_line claim)" "FAIL" "the claim row"
}

test_no_job_of_the_repository_is_unmeasured() {
  setup_case nojobs
  mode jobs none
  run390_default
  expect_status "$status" 2 "no job of the repository"
  expect_contains "$(fact_line job)" "no job of ${REPO}" "the job row"
  expect_contains "$(fact_line claim)" "NOT MEASURED" "the claim row"
}

test_a_job_the_catalog_gives_no_image_is_unmeasured() {
  setup_case plainjob
  # A job the catalog chose no image for: it runs the image this project builds
  # for the repository itself, and carries an empty stamp. That is a legitimate
  # installation (O4 b), so the job row holds — and the two rows that read the
  # deployed image's digest have nothing to read, which is NOT MEASURED and not
  # a FAIL about a job that is doing nothing wrong.
  printf 'fugaro-acme-app-run|%s|acme-app-0123456789abcdef|https://github.com/acme/app.git|\n' \
    "${REGION}-docker.pkg.dev/${GCP}/fugaro/acme-app:latest" >"$dir/state/jobs"
  run390_default
  expect_status "$status" 2 "a job the catalog gives no image"
  expect_contains "$(fact_line job)" "PASS" "the job row"
  expect_contains "$(fact_line registry)" "no job of ${REPO} carries a catalog entry's stamp" "the registry row"
  expect_contains "$(fact_line digest)" "no job of ${REPO} carries a catalog entry's stamp" "the digest row"
  expect_contains "$(fact_line claim)" "NOT MEASURED" "the claim row"
}

test_a_jobs_listing_that_fails_is_unmeasured() {
  setup_case jobsfail
  mode jobs fail
  run390_default
  expect_status "$status" 2 "a jobs listing that failed"
  expect_contains "$(fact_line job)" "gcloud run jobs list exited" "the job row"
  expect_contains "$(fact_line claim)" "NOT MEASURED" "the claim row"
}

test_a_registry_that_cannot_be_read_is_unmeasured() {
  setup_case registryfail
  mode registry fail
  run390_default
  expect_status "$status" 2 "a registry that could not be read"
  expect_contains "$(fact_line registry)" "artifacts docker images describe" "the registry row"
  expect_contains "$(fact_line claim)" "NOT MEASURED" "the claim row"
}

test_a_registry_document_with_no_sha_is_unmeasured() {
  setup_case registrynosha
  mode registry nosha
  run390_default
  expect_status "$status" 2 "a registry document with no sha256"
  expect_contains "$(fact_line registry)" "no sha256" "the registry row"
  expect_contains "$(fact_line claim)" "NOT MEASURED" "the claim row"
}

test_a_step_left_owed_is_a_fail() {
  setup_case owed
  mode status owed
  run390_default
  expect_status "$status" 1 "a step left owed"
  expect_contains "$(fact_line status)" "FAIL" "the status row"
  expect_contains "$(fact_line status)" "policy" "the status row names the step"
  expect_contains "$(fact_line claim)" "FAIL" "the claim row"
}

test_a_leftover_entry_is_removed_first() {
  setup_case leftover
  printf '%s|%s\nbase|%s\n' "$ENTRY" "$DIGEST" "$DIGEST" >"$dir/state/catalog"
  run390_default
  expect_status "$status" 0 "a run over a leftover entry"
  expect_contains "$(fact_line entry)" "leftover" "the entry row says it was a leftover"
  expect_call "the leftover was removed" "image rm ${ENTRY}"
  expect_contains "$(fact_line claim)" "PASS" "the claim row"
}

test_a_teardown_that_fails_is_a_fail() {
  setup_case teardownfail
  mode rm fail
  run390_default
  expect_status "$status" 1 "a teardown that failed"
  expect_contains "$(fact_line teardown)" "FAIL" "the teardown row"
  expect_contains "$(fact_line teardown)" "STILL in the project's catalog" "the teardown row names what is left"
  expect_contains "$(fact_line claim)" "teardown did NOT" "the claim row"
}

test_a_catalog_that_cannot_be_read_is_unmeasured() {
  setup_case nocatalog
  mode ls fail
  run390_default
  expect_status "$status" 2 "a catalog that could not be read"
  expect_contains "$(fact_line entry)" "FAIL" "the entry row"
  expect_contains "$(fact_line claim)" "NOT MEASURED" "the claim row"
  # An entry this check cannot read is one it must not add: nothing is written.
  expect_no_call "the add never ran" "image add"
}

test_a_coding_agents_session_is_refused() {
  setup_case agent
  RUN390_ENV=(CLAUDECODE=1)
  run390_default
  expect_status "$status" 2 "a coding agent's session"
  expect_contains "$(fact_line agent-session)" "CLAUDECODE" "the agent-session row names the marker"
  expect_no_calls "a coding agent's session"
}

test_an_editors_own_marker_is_dropped_not_refused() {
  setup_case ide
  RUN390_ENV=(CLAUDE_CODE_SSE_PORT=1234)
  run390_default
  expect_status "$status" 0 "a shell in an editor's terminal"
  expect_contains "$(fact_line agent-session)" "PASS" "the agent-session row"
}

test_a_shell_that_arrived_tracing_is_refused() {
  setup_case xtrace
  RUN390_BASH=(bash -x)
  run390_default
  expect_status "$status" 2 "a shell that arrived tracing"
  expect_contains "$(fact_line xtrace)" "FAIL" "the xtrace row"
  expect_no_calls "a shell that arrived tracing"
}

test_belong_is_refused_in_what_you_typed() {
  setup_case belong-typed
  run390_default --project belong
  expect_status "$status" 2 "belong typed on the command line"
  expect_contains "$(fact_line belong)" "FAIL" "the belong row"
  expect_no_calls "belong typed on the command line"
}

test_belong_is_refused_in_what_doctor_answers() {
  setup_case belong-doctor
  mode doctor belong
  run390_default
  expect_status "$status" 2 "belong answered by doctor"
  expect_contains "$(fact_line belong)" "FAIL" "the belong row"
  expect_call "the installation was read" "doctor --json"
  expect_no_call "the add never ran" "image add"
}

test_a_project_you_typed_that_is_not_the_configs_is_refused() {
  setup_case twoprojects
  run390_default --project somethingelse
  expect_status "$status" 2 "two different installations"
  expect_contains "$err" "two different installations" "the refusal"
  expect_no_call "the add never ran" "image add"
}

# Sharding, so the package's Go test can run the tests of this file side by side:
# CHECK_TEST_SHARD="i/n" runs every n-th test starting at i, and CHECK_TEST_ONLY=name
# runs that one test alone. Neither set: all run.
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
t test_a_dry_run_prints_the_plan_and_makes_no_call
t test_a_dry_run_still_refuses_belong
t test_a_missing_dockerfile_is_refused
t test_the_happy_path_passes
t test_the_add_runs_with_its_stdin_closed
t test_a_failing_add_is_a_fail
t test_an_add_that_prints_nothing_is_a_fail
t test_an_entry_the_add_left_out_of_the_catalog_is_a_fail
t test_a_digest_that_does_not_match_is_a_fail
t test_a_job_stamped_with_an_entry_the_catalog_does_not_hold_is_a_fail
t test_no_job_of_the_repository_is_unmeasured
t test_a_job_the_catalog_gives_no_image_is_unmeasured
t test_a_jobs_listing_that_fails_is_unmeasured
t test_a_registry_that_cannot_be_read_is_unmeasured
t test_a_registry_document_with_no_sha_is_unmeasured
t test_a_step_left_owed_is_a_fail
t test_a_leftover_entry_is_removed_first
t test_a_teardown_that_fails_is_a_fail
t test_a_catalog_that_cannot_be_read_is_unmeasured
t test_a_coding_agents_session_is_refused
t test_an_editors_own_marker_is_dropped_not_refused
t test_a_shell_that_arrived_tracing_is_refused
t test_belong_is_refused_in_what_you_typed
t test_belong_is_refused_in_what_doctor_answers
t test_a_project_you_typed_that_is_not_the_configs_is_refused

if [ "$failures" != 0 ]; then
  echo "$failures failure(s)" >&2
  exit 1
fi
echo "check-390_test.sh: all cases passed"
