#!/usr/bin/env bash
# Tests for scripts/live-checks/check200.sh, against fake gcloud, fugaro and go
# on PATH and a REAL local git repository: nothing here reaches the network,
# there is no cloud project, no run is launched and no credential is read, made
# or printed. The repository is a local one with a bare remote beside it, so
# `git checkout -b`, `git commit`, `git push` and `git push --delete` really
# happen — the Restore is a reading of a real repository, which is what makes
# "the throwaway branch is gone locally and on origin" a fact rather than a
# claim about a fake.
#
# The fakes model the three facts the check is about and nothing else: a run
# record whose halt and checkpoint the case sets, a build record whose edition
# the case sets, and an untagged binary built by the fake `go` that answers
# `report` with the core stub and runs the digest.
#
# Usage: bash scripts/live-checks/check200_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check200.sh
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK200_TEST_KEEP:-}" ]; then
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
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT (aborted|restore):' | sed 's/^/ ... last run: /')"
}

# The project, the repository and the run a case works with. None of them names
# `belong`, which is the installation this check must never touch.
PROJECT=prj-sandbox-a
REPO_SLUG=sandbox-a/alpha
SLUG=alpha
RUN_ID=20261114-101010-abcd
RUN_REF=$SLUG/$RUN_ID

# Every fact key the run's block carries, whatever it measured.
BLOCK_KEYS=(fugaro-version release xtrace agent-session belong bound sandbox
  source config run-launched terminal result-json halt checkpoint
  edition-carrier digest-untagged restore)

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake gcloud for check200.sh's test: the account the check reads at startup,
# and the two documents it reads out of the runs bucket. Every call is recorded
# on the first line, before anything else, so a dry run that made no call at all
# is provable rather than argued.
set -euo pipefail
dir=${FAKE200_DIR:?FAKE200_DIR is not set}
printf 'gcloud %s\n' "$*" >>"$dir/calls"
if [ "$*" = "config get-value account" ]; then
  printf '%s\n' "${FAKE200_ACCOUNT:-owner@sandbox-a.example}"
  exit 0
fi
uri=""
for a in "$@"; do
  case "$a" in gs://*) uri=$a ;; esac
done
case "$uri" in
"") printf 'fake gcloud: unexpected call: %s\n' "$*" >&2; exit 64 ;;
esac
key=${uri#gs://*/}
case "$key" in
runs/*/result.json)
  printf '{\n'
  printf '  "version": 1,\n'
  printf '  "run_id": "%s",\n' "${FAKE200_RECORD_RUN_ID:-${FAKE200_RUN_ID:-20261114-101010-abcd}}"
  printf '  "repo": "%s",\n' "${FAKE200_REPO_SLUG:-sandbox-a/alpha}"
  printf '  "workflow": "%s",\n' "${FAKE200_WORKFLOW:-go}"
  printf '  "status": "%s",\n' "${FAKE200_STATUS:-halted}"
  printf '  "outcome": "draft",\n'
  printf '  "failure_class": "%s",\n' "${FAKE200_FAILURE_CLASS:-halted_governance}"
  printf '  "cost_usd": 0.42,\n'
  printf '  "halt": {"reason": "%s", "scope": "run", "at": "2026-11-14T10:00:00Z", "detail": "%s"},\n' \
    "${FAKE200_HALT_REASON:-stage_budget}" "${FAKE200_HALT_DETAIL:-time}"
  if [ "${FAKE200_CHECKPOINT:-present}" = present ]; then
    printf '  "checkpoint": {"trigger": "%s", "why_over": "%s", "estimate_usd": 0.42},\n' \
      "${FAKE200_CP_TRIGGER:-stage_budget}" "${FAKE200_CP_WHY:-a stage passed its own time bound}"
  fi
  printf '  "stages": []\n}\n'
  ;;
builds/*/image.json)
  if [ "${FAKE200_NO_IMAGE:-no}" = yes ]; then
    printf 'fake gcloud: no object at %s\n' "$key" >&2
    exit 1
  fi
  printf '{\n'
  printf '  "version": 1,\n'
  printf '  "repo": "%s",\n' "${FAKE200_REPO_SLUG:-sandbox-a/alpha}"
  printf '  "workflow": "%s",\n' "${FAKE200_WORKFLOW:-go}"
  printf '  "build_id": "build-1",\n'
  printf '  "edition": "%s",\n' "${FAKE200_EDITION:-ee}"
  printf '  "fugaro_version": "%s"\n' "${FAKE200_VERSION:-0.13.0}"
  printf '}\n'
  ;;
*)
  printf 'fake gcloud: no object at %s\n' "$key" >&2
  exit 1
  ;;
esac
GCLOUD_EOF

  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check200.sh's test: the release gate, the doctor, the
# settings write (which the product refuses, and which the check therefore never
# makes), the launch and the runs list.
set -euo pipefail
dir=${FAKE200_DIR:?FAKE200_DIR is not set}
printf 'fugaro %s\n' "$*" >>"$dir/fugaro-calls"
case "$1" in
version)
  # `fugaro version` prints the version and a newline, nothing else
  # (internal/cli/root.go's versionTemplate).
  printf '%s\n' "${FAKE200_VERSION:-0.13.0}"
  ;;
doctor)
  printf '{"project": {"gcp_project": "%s"}}\n' "${FAKE200_PROJECT:-prj-sandbox-a}"
  ;;
settings)
  # The real CLI answers NO write of this key at any layer spelling that could
  # reach a run's own ref, and the check therefore writes the fugaro.yaml line
  # itself. The fake answers the product's own refusal (verified against the
  # CLI: `fugaro settings set governance.stage_timeout.implement 1m --layer
  # repo` exits 1) rather than performing an edit no real command would make,
  # so a case that catches the check calling it is catching a call the product
  # does not answer.
  printf 'governance.stage_timeout.implement: no setting has that name; the nearest are layer.governance.stage_timeout.implement, governance.stage_timeout, layer.governance.stage_tokens.implement\n' >&2
  exit 1
  ;;
runs)
  case "$2" in
  digest)
    # `fugaro runs digest --help` is the release gate's feature probe.
    case "$*" in
    *--help*)
      [ "${FAKE200_DIGEST_HELP:-yes}" = yes ] || exit 1
      printf 'Usage: fugaro runs digest\n'
      ;;
    esac
    ;;
  ls)
    printf '{"runs": [{"run_id": "%s", "status": "%s", "terminal": %s, "stage": "implement", "run": "%s"}]}\n' \
      "${FAKE200_RUN_ID:-20261114-101010-abcd}" "${FAKE200_RUN_STATUS:-halted}" \
      "${FAKE200_TERMINAL:-true}" "${FAKE200_RUN_REF:-alpha/20261114-101010-abcd}"
    ;;
  *)
    printf 'fake fugaro: unexpected call: %s\n' "$*" >&2
    exit 64
    ;;
  esac
  ;;
run)
  printf '{"run_id": "%s", "repo": "%s", "run": "%s", "branch": "fugaro/%s", "ref": "%s", "status": "launched"}\n' \
    "${FAKE200_RUN_ID:-20261114-101010-abcd}" "${FAKE200_REPO_SLUG:-sandbox-a/alpha}" \
    "${FAKE200_RUN_REF:-alpha/20261114-101010-abcd}" "${FAKE200_RUN_ID:-20261114-101010-abcd}" \
    "${FAKE200_LAUNCH_REF:-chk200-00000000}"
  ;;
*)
  printf 'fake fugaro: unexpected call: %s\n' "$*" >&2
  exit 64
  ;;
esac
FUGARO_EOF

  cat >"$1/bin/go" <<'GO_EOF'
#!/usr/bin/env bash
# A fake go for check200.sh's test: `go build -o <path> ./cmd/fugaro` writes the
# untagged binary, which is the only thing the check builds. The binary it
# writes is a script whose `report` answers the core stub unless the case says
# the build is not untagged after all.
set -euo pipefail
dir=${FAKE200_DIR:?FAKE200_DIR is not set}
printf 'go %s\n' "$*" >>"$dir/go-calls"
out=""
prev=""
for a in "$@"; do
  if [ "$prev" = "-o" ]; then out=$a; fi
  prev=$a
done
case "$out" in
"") printf 'fake go: no -o in: %s\n' "$*" >&2; exit 64 ;;
esac
cat >"$out" <<'CORE_EOF'
#!/usr/bin/env bash
set -euo pipefail
case "$1" in
report)
  if [ "${FAKE200_CORE_STUB:-yes}" = yes ]; then
    printf 'report is part of Fugaro Subscription Edition; this build does not include it\n' >&2
    exit 1
  fi
  printf 'a report, from a build that is not the Subscription Edition\n'
  ;;
runs)
  if [ "${FAKE200_DIGEST_EXIT:-0}" != 0 ]; then
    printf 'fake fugaro-core: runs digest failed\n' >&2
    exit "${FAKE200_DIGEST_EXIT}"
  fi
  printf '%s\n' "${FAKE200_DIGEST_OUT:-# Run digest — 2026-11-14}"
  ;;
*)
  printf 'fake fugaro-core: unexpected call: %s\n' "$*" >&2
  exit 64
  ;;
esac
CORE_EOF
chmod +x "$out"
GO_EOF
  chmod +x "$1/bin/gcloud" "$1/bin/fugaro" "$1/bin/go"
}

# ------------------------------------------------------------------- the cases
# setup_case NAME — a fresh case: the fakes, a source tree that looks like a
# Fugaro checkout (the fake go does not read it, the check does), a sandbox
# checkout with a bare remote beside it, and no branch made by any earlier case.
setup_case() {
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/tmp" "$dir/home" "$dir/state" "$dir/src/cmd/fugaro"
  write_fakes "$dir"
  : >"$dir/state/calls"
  : >"$dir/state/fugaro-calls"
  : >"$dir/state/go-calls"
  : >"$dir/src/go.mod"
  : >"$dir/src/cmd/fugaro/main.go"
  mkdir -p "$dir/sandbox"
  git init -q --bare "$dir/origin.git"
  git init -q -b main "$dir/sandbox"
  git -C "$dir/sandbox" config user.email check200@example.invalid
  git -C "$dir/sandbox" config user.name "Check 200 test"
  git -C "$dir/sandbox" config commit.gpgsign false
  printf 'workflows:\n  go: {}\n' >"$dir/sandbox/fugaro.yaml"
  git -C "$dir/sandbox" add fugaro.yaml
  git -C "$dir/sandbox" commit --quiet -m "the sandbox repository"
  git -C "$dir/sandbox" remote add origin "$dir/origin.git"
  git -C "$dir/sandbox" push --quiet -u origin main
  cp "$dir/sandbox/fugaro.yaml" "$dir/state/fugaro.yaml.before"
  # The owner's own uncommitted work, which is what a case about a file the
  # check must not touch is about: it is written after the copy above, so the
  # copy is the file as the check found it.
  if [ "${FAKE200_DIRTY:-no}" = yes ]; then
    printf 'name: a change of your own\n' >>"$dir/sandbox/fugaro.yaml"
  fi
  status=0
  out=""
  RUN200_ARGS=()
  RUN200_ENV=()
}

# run200 [ARGS...] — runs the check on the case at hand. RUN200_ARGS adds
# arguments, RUN200_ENV names environment variables (NAME=value), and the wait
# for the run to end is one second so a case about a run that never ends does
# not sit through the real hour.
run200() {
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    TMPDIR="$dir/tmp" HOME="$dir/home" \
    GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null \
    FAKE200_DIR="$dir/state" \
    CHECK200_RUN_TIMEOUT_SECONDS="${RUN_TIMEOUT:-1}" CHECK200_POLL_SECONDS=0 \
    PATH="$dir/bin:$PATH" \
    ${RUN200_ENV[@]+"${RUN200_ENV[@]}"} \
    bash "$script" ${RUN200_ARGS[@]+"${RUN200_ARGS[@]}"} "$@" 2>"$dir/err") || status=$?
  RUN200_ARGS=()
  RUN200_ENV=()
  RUN_TIMEOUT=1
}
run200_default() { run200 --sandbox "$dir/sandbox" --source "$dir/src" --yes "$@"; }

expect_block_keys() { # what
  local key
  expect_contains "$out" "CHECK 200 RESULT" "$1: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$out" "FACT $key:" "$1: the $key row"
  done
}

# expect_no_calls WHAT — a run that reached the network, launched anything or
# built anything. The account read at startup is a local config read, not a
# call, and it is the one line a case may find.
expect_no_calls() { # what
  local extra
  extra=$(grep -v '^gcloud config get-value account$' "$dir/state/calls" 2>/dev/null || true)
  [ -z "$extra" ] || fail "$1: a gcloud call was made: $(printf '%s' "$extra" | tr '\n' ';')"
  [ ! -s "$dir/state/fugaro-calls" ] || fail "$1: a fugaro call was made: $(tr '\n' ';' <"$dir/state/fugaro-calls")"
  [ ! -s "$dir/state/go-calls" ] || fail "$1: a go call was made: $(tr '\n' ';' <"$dir/state/go-calls")"
}

# expect_no_writes WHAT — nothing was written and nothing was launched: the
# release gate's two offline probes may have been made, and nothing else may.
expect_no_writes() { # what
  local calls extra
  extra=$(grep -v '^gcloud config get-value account$' "$dir/state/calls" 2>/dev/null || true)
  [ -z "$extra" ] || fail "$1: a gcloud call was made: $(printf '%s' "$extra" | tr '\n' ';')"
  [ ! -s "$dir/state/go-calls" ] || fail "$1: a go call was made: $(tr '\n' ';' <"$dir/state/go-calls")"
  calls=$(cat "$dir/state/fugaro-calls" 2>/dev/null || true)
  expect_absent "$calls" "settings" "$1: a settings call was made"
  expect_absent "$calls" "run --repo" "$1: a run was launched"
}

# fact_line KEY: the ONE line of the block that carries KEY, so a case about what
# a single row says reads that row and not the whole block.
fact_line() { # key
  printf '%s\n' "$out" | grep -m1 "^FACT $1:" || true
}

# expect_clean WHAT — the Restore really took the throwaway branch away: no
# local branch by that name, no head on origin, the checkout back on the branch
# it was on, and the fugaro.yaml the commit carried back as it was.
expect_clean() { # what
  local what=$1 branches head
  branches=$(git -C "$dir/sandbox" for-each-ref --format='%(refname:short)' refs/heads)
  case $'\n'"$branches"$'\n' in
  *$'\n'chk200-*$'\n'*) fail "$what: a throwaway branch was left in the checkout: $branches" ;;
  esac
  head=$(git -C "$dir/sandbox" symbolic-ref --short -q HEAD || true)
  [ "$head" = main ] || fail "$what: the checkout is on '$head' rather than back on main"
  head=$(git -C "$dir/origin.git" for-each-ref --format='%(refname:short)' refs/heads)
  case $'\n'"$head"$'\n' in
  *$'\n'chk200-*$'\n'*) fail "$what: a throwaway branch was left on origin: $head" ;;
  esac
  if ! diff -q "$dir/state/fugaro.yaml.before" "$dir/sandbox/fugaro.yaml" >/dev/null 2>&1; then
    fail "$what: the checkout's fugaro.yaml did not come back as it was"
  fi
}

expect_scratch_gone() { # what
  local scratch
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  if [ -z "$scratch" ]; then
    fail "$1: the script did not say where its scratch directory is"
    return 0
  fi
  [ -d "$scratch" ] && fail "$1: the scratch directory $scratch survived the run"
  [ -z "$(find "$dir/tmp" -mindepth 1 2>/dev/null)" ] || fail "$1: files were left in TMPDIR: $(find "$dir/tmp" -mindepth 1)"
  return 0
}

# ---------------------------------------------------------------- the tests
# The happy path: the three facts hold, and the Restore is a reading of a real
# repository rather than a claim about a fake.
test_the_three_facts_hold() {
  setup_case three-facts
  run200_default
  expect_status "$status" 0 "the three facts"
  expect_block_keys "the three facts"
  expect_contains "$(fact_line halt)" "PASS" "the three facts: the halt row"
  expect_contains "$(fact_line halt)" "reason=stage_budget detail=time" "the three facts: the reason and the detail"
  expect_contains "$(fact_line halt)" "failure_class=halted_governance" "the three facts: the class"
  expect_contains "$(fact_line checkpoint)" "PASS" "the three facts: the checkpoint row"
  expect_contains "$(fact_line checkpoint)" "trigger=stage_budget" "the three facts: the checkpoint's trigger"
  expect_contains "$(fact_line checkpoint)" "why_over='a stage passed its own time bound'" "the three facts: the checkpoint's sentence"
  expect_contains "$(fact_line edition-carrier)" "PASS" "the three facts: the carrier row"
  expect_contains "$(fact_line edition-carrier)" "edition=ee" "the three facts: the edition"
  expect_contains "$(fact_line digest-untagged)" "PASS" "the three facts: the digest row"
  expect_contains "$(fact_line source)" "core stub" "the three facts: the untagged build was proved untagged"
  expect_contains "$out" "FACT restore: PASS" "the three facts: the restore row"
  expect_clean "the three facts"
  expect_scratch_gone "the three facts"
  # The ceiling is the check's own edit of the checkout's fugaro.yaml, read back
  # with git diff — and the CLI is asked for no write of it, because it answers
  # none (the fake refuses the way the product does, so a call reappearing here
  # fails this case rather than being performed by a fake).
  expect_contains "$(fact_line config)" "governance.stage_timeout.implement = 1m is committed on chk200-" \
    "the three facts: the ceiling the check wrote"
  expect_absent "$(cat "$dir/state/fugaro-calls")" "settings" "the three facts: no settings write is asked of the CLI"
  expect_contains "$(cat "$dir/state/fugaro-calls")" "run --repo $dir/sandbox --ref chk200-" \
    "the three facts: the run is cut from the throwaway branch"
  expect_contains "$(cat "$dir/state/fugaro-calls")" "--task-file" "the three facts: the task is a file, as the plan names it"
}

test_a_run_that_did_not_halt_is_a_fail() {
  setup_case no-halt
  RUN200_ENV=(FAKE200_STATUS=succeeded FAKE200_HALT_REASON= FAKE200_HALT_DETAIL=)
  run200_default
  expect_status "$status" 1 "a run that did not halt"
  expect_contains "$(fact_line halt)" "FAIL" "a run that did not halt: the halt row"
  expect_contains "$(fact_line halt)" "ended succeeded rather than halted" "a run that did not halt: the ending is named"
  expect_clean "a run that did not halt"
}

test_a_halt_on_another_bound_is_a_fail() {
  setup_case another-bound
  RUN200_ENV=(FAKE200_HALT_REASON=run_cap FAKE200_HALT_DETAIL=)
  run200_default
  expect_status "$status" 1 "a halt on another bound"
  expect_contains "$(fact_line halt)" "FAIL" "a halt on another bound: the halt row"
  expect_contains "$(fact_line halt)" "reason=run_cap" "a halt on another bound: the reason it read"
}

test_a_halt_that_is_not_classified_as_governance_is_a_fail() {
  setup_case class
  RUN200_ENV=(FAKE200_FAILURE_CLASS=halted_budget)
  run200_default
  expect_status "$status" 1 "a halt classified as another kind"
  expect_contains "$(fact_line halt)" "failure_class is halted_budget" "a halt classified as another kind: the class it read"
}

test_a_record_with_no_checkpoint_is_a_fail() {
  setup_case no-checkpoint
  RUN200_ENV=(FAKE200_CHECKPOINT=none)
  run200_default
  expect_status "$status" 1 "a record with no checkpoint"
  expect_contains "$(fact_line checkpoint)" "FAIL" "a record with no checkpoint: the row"
  expect_contains "$(fact_line checkpoint)" "carries no checkpoint block" "a record with no checkpoint: the sentence"
}

test_a_checkpoint_naming_another_trigger_is_a_fail() {
  setup_case other-trigger
  RUN200_ENV=(FAKE200_CP_TRIGGER=review_ratio)
  run200_default
  expect_status "$status" 1 "a checkpoint naming another trigger"
  expect_contains "$(fact_line checkpoint)" "trigger is review_ratio" "a checkpoint naming another trigger: the row"
}

test_a_checkpoint_with_another_sentence_is_a_fail() {
  setup_case other-why
  RUN200_ENV=("FAKE200_CP_WHY=a stage passed its own budget")
  run200_default
  expect_status "$status" 1 "a checkpoint with another sentence"
  expect_contains "$(fact_line checkpoint)" "FAIL" "a checkpoint with another sentence: the row"
}

test_an_edition_carrier_that_says_core_is_a_fail() {
  setup_case core-image
  RUN200_ENV=(FAKE200_EDITION=core)
  run200_default
  expect_status "$status" 1 "a carrier that says core"
  expect_contains "$(fact_line edition-carrier)" "FAIL" "a carrier that says core: the row"
  expect_contains "$(fact_line edition-carrier)" "says edition='core'" "a carrier that says core: the edition it read"
}

test_a_build_record_that_is_absent_is_could_not_tell() {
  setup_case no-image
  RUN200_ENV=(FAKE200_NO_IMAGE=yes)
  run200_default
  expect_status "$status" 2 "a build record that is not there"
  expect_contains "$(fact_line edition-carrier)" "could not be read" "a build record that is not there: the row"
}

test_a_digest_that_refuses_is_a_fail() {
  setup_case digest-refused
  RUN200_ENV=(FAKE200_DIGEST_EXIT=1)
  run200_default
  expect_status "$status" 1 "a digest that refuses"
  expect_contains "$(fact_line digest-untagged)" "FAIL" "a digest that refuses: the row"
  expect_contains "$(fact_line digest-untagged)" "exited 1" "a digest that refuses: the status it read"
}

test_a_build_that_is_not_untagged_stops_the_digest_row() {
  setup_case tagged-source
  RUN200_ENV=(FAKE200_CORE_STUB=no)
  run200_default
  expect_status "$status" 1 "a source build that is not untagged"
  expect_contains "$(fact_line source)" "FAIL" "a source build that is not untagged: the source row"
  expect_contains "$(fact_line source)" "core stub" "a source build that is not untagged: the stub it did not see"
  expect_contains "$(fact_line digest-untagged)" "FAIL" "a source build that is not untagged: the digest row"
  expect_contains "$(fact_line digest-untagged)" "no untagged build to run the digest from" \
    "a source build that is not untagged: the digest row says why"
  expect_absent "$(cat "$dir/state/go-calls" 2>/dev/null || true)" "digest" "a source build that is not untagged: no digest was run"
}

test_a_run_that_never_ends_is_could_not_tell() {
  setup_case never-ends
  RUN200_ENV=(FAKE200_TERMINAL=false FAKE200_RUN_STATUS=running)
  run200_default
  expect_status "$status" 2 "a run that never ends"
  expect_contains "$(fact_line terminal)" "FAIL" "a run that never ends: the terminal row"
  expect_contains "$(fact_line terminal)" "had not ended" "a run that never ends: the sentence"
  expect_clean "a run that never ends"
}

test_the_record_of_another_run_is_a_fail() {
  setup_case other-record
  RUN200_ENV=(FAKE200_RECORD_RUN_ID=20261114-999999-ffff)
  run200_default
  expect_status "$status" 1 "a record of another run"
  expect_contains "$(fact_line result-json)" "FAIL" "a record of another run: the record row"
  expect_contains "$(fact_line result-json)" "records run_id=20261114-999999-ffff" "a record of another run: what it read"
}

# The check writes the ceiling itself on the branch it throws away, and it reads
# the edit back off the repository rather than off a fake. A file that already
# carries a governance: block is the one shape it will not write into: a second
# governance: key is a file the strict decoder refuses at the run's bootstrap,
# which would read as a finding about the release. So it stops and says so.
test_a_governance_block_already_there_stops_before_the_run() {
  setup_case governance-there
  printf 'governance:\n  review_ratio: 0.5\n' >>"$dir/sandbox/fugaro.yaml"
  git -C "$dir/sandbox" commit --quiet -am "a governance block of your own"
  cp "$dir/sandbox/fugaro.yaml" "$dir/state/fugaro.yaml.before"
  run200_default
  expect_status "$status" 2 "a fugaro.yaml that already carries a governance: block"
  expect_contains "$(fact_line config)" "already carries a governance: block" "a governance: block already there: the row"
  expect_contains "$(fact_line config)" "the ceiling has to sit at governance.stage_timeout.implement" "a governance: block already there: what it asks for"
  expect_contains "$(fact_line run-launched)" "nothing was launched" "a governance: block already there: nothing was launched"
  expect_absent "$(cat "$dir/state/fugaro-calls")" "run --repo" "a governance: block already there: no run"
  expect_contains "$(cat "$dir/sandbox/fugaro.yaml")" "review_ratio: 0.5" "a governance: block already there: your block is untouched"
  expect_clean "a governance: block already there"
}

test_a_dirty_fugaro_yaml_stops_before_writing() {
  FAKE200_DIRTY=yes setup_case dirty
  run200_default
  expect_status "$status" 2 "a fugaro.yaml with your own work in it"
  expect_contains "$(fact_line config)" "uncommitted changes" "a fugaro.yaml with your own work in it: the row"
  expect_absent "$(cat "$dir/state/fugaro-calls")" "settings" "a fugaro.yaml with your own work in it: nothing was written"
  expect_contains "$(git -C "$dir/sandbox" status --porcelain -- fugaro.yaml)" "fugaro.yaml" \
    "a fugaro.yaml with your own work in it: your work is still there"
}

test_belong_in_the_checkout_path_is_refused() {
  setup_case belong-path
  mkdir -p "$work/belong-path/belong-sandbox"
  git init -q -b main "$work/belong-path/belong-sandbox"
  : >"$work/belong-path/belong-sandbox/fugaro.yaml"
  run200 --sandbox "$work/belong-path/belong-sandbox" --source "$dir/src" --yes
  expect_status "$status" 1 "belong in the checkout path"
  expect_contains "$(fact_line belong)" "FAIL" "belong in the checkout path: the belong row"
  expect_no_calls "belong in the checkout path"
}

test_belong_in_the_repository_name_is_refused() {
  setup_case repo-name
  RUN200_ENV=(CHECK200_REPO=belong-alpha)
  run200_default
  expect_status "$status" 1 "belong in the repository name"
  expect_contains "$(fact_line belong)" "FAIL" "belong in the repository name: the belong row"
  expect_contains "$(fact_line belong)" "belong-alpha" "belong in the repository name: the name it matched"
  expect_no_calls "belong in the repository name"
}

test_belong_in_the_project_stops_the_sandbox_step() {
  setup_case project-name
  RUN200_ENV=(FAKE200_PROJECT=belong-sandbox)
  run200_default
  expect_status "$status" 1 "belong in the project"
  expect_contains "$(fact_line aborted)" "belongs to belong-sandbox" "belong in the project: the stop"
  expect_absent "$(cat "$dir/state/fugaro-calls")" "settings" "belong in the project: nothing was written"
  expect_absent "$(cat "$dir/state/fugaro-calls")" "run --repo" "belong in the project: no run"
}

test_a_coding_agents_session_is_refused() {
  setup_case agent-session
  RUN200_ENV=(CLAUDECODE=1)
  run200_default
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$(fact_line aborted)" "CLAUDECODE is set" "a coding agent's session: the stop"
  expect_no_calls "a coding agent's session"
}

test_an_older_release_stops() {
  setup_case older-release
  RUN200_ENV=(FAKE200_VERSION=0.12.0)
  run200_default
  expect_status "$status" 1 "an older release"
  expect_contains "$(fact_line release)" "FAIL" "an older release: the release row"
  expect_contains "$(fact_line release)" "predates release 0.13.0" "an older release: the sentence"
  expect_no_writes "an older release"
}

test_a_dry_run_prints_the_plan_and_makes_no_call() {
  setup_case dry-run
  run200 --sandbox "$dir/sandbox" --source "$dir/src" --dry-run
  expect_status "$status" 0 "a dry run"
  expect_contains "$(cat "$dir/err")" "CHECK 200 plan (dry run" "a dry run: the plan"
  expect_contains "$out" "FACT dry-run: PASS" "a dry run: the dry-run row"
  expect_contains "$out" "CHECK 200 RESULT" "a dry run: the block"
  expect_contains "$out" "FACT halt: INFO" "a dry run: the facts it did not measure are planned, never graded"
  expect_no_calls "a dry run"
  expect_clean "a dry run"
}

test_a_dry_run_still_refuses_belong() {
  setup_case dry-run-belong
  RUN200_ENV=(CHECK200_REPO=belong-alpha)
  run200 --sandbox "$dir/sandbox" --source "$dir/src" --dry-run
  expect_status "$status" 1 "a dry run against belong"
  expect_contains "$(fact_line belong)" "FAIL" "a dry run against belong: the belong row"
  expect_no_calls "a dry run against belong"
}

test_a_run_without_yes_asks_for_it() {
  setup_case no-yes
  run200 --sandbox "$dir/sandbox" --source "$dir/src"
  expect_status "$status" 2 "a run without --yes"
  expect_contains "$(cat "$dir/err")" "asks for --yes" "a run without --yes: the sentence"
  expect_contains "$(cat "$dir/err")" "--dry-run" "a run without --yes: the plan is offered"
  expect_no_calls "a run without --yes"
  expect_clean "a run without --yes"
}

test_usage_and_arguments() {
  setup_case usage
  run200 --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Check 200" "--help: the header"
  expect_contains "$out" "Usage:" "--help: the usage line"
  run200 --sandbox
  expect_status "$status" 2 "--sandbox without a value"
  expect_contains "$(cat "$dir/err")" "--sandbox needs a path" "--sandbox without a value: the sentence"
  run200 --source
  expect_status "$status" 2 "--source without a value"
  expect_contains "$(cat "$dir/err")" "--source needs a path" "--source without a value: the sentence"
  run200 --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$(cat "$dir/err")" "unknown argument: --nonsense" "an unknown argument: the sentence"
  run200 --source "$dir/src" --yes
  expect_status "$status" 2 "no --sandbox at all"
  expect_contains "$(cat "$dir/err")" "needs --sandbox" "no --sandbox at all: the sentence"
  expect_no_calls "usage and arguments"
}

test_a_checkout_that_is_not_one_stops() {
  setup_case not-a-checkout
  mkdir -p "$dir/plain"
  run200 --sandbox "$dir/plain" --source "$dir/src" --yes
  expect_status "$status" 2 "a directory that is not a checkout"
  expect_contains "$(cat "$dir/err")" "carries no fugaro.yaml" "a directory that is not a checkout: the sentence"
  expect_no_writes "a directory that is not a checkout"
}

test_a_source_tree_that_is_not_one_stops_the_digest_row() {
  setup_case no-source
  mkdir -p "$dir/empty"
  run200 --sandbox "$dir/sandbox" --source "$dir/empty" --yes
  expect_status "$status" 2 "a source tree that is not a Fugaro checkout"
  expect_contains "$(fact_line source)" "FAIL" "a source tree that is not one: the source row"
  expect_contains "$(fact_line digest-untagged)" "no untagged build to run the digest from" \
    "a source tree that is not one: the digest row says why"
  expect_clean "a source tree that is not one"
}

test_a_scratch_directory_is_removed_even_when_a_step_stops() {
  setup_case scratch
  RUN200_ENV=(FAKE200_EDITION=core)
  run200_default
  expect_status "$status" 1 "a failing run"
  expect_contains "$out" "FACT scratch-removed: PASS" "a failing run: the scratch row"
  expect_scratch_gone "a failing run"
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

t test_the_three_facts_hold
t test_a_run_that_did_not_halt_is_a_fail
t test_a_halt_on_another_bound_is_a_fail
t test_a_halt_that_is_not_classified_as_governance_is_a_fail
t test_a_record_with_no_checkpoint_is_a_fail
t test_a_checkpoint_naming_another_trigger_is_a_fail
t test_a_checkpoint_with_another_sentence_is_a_fail
t test_an_edition_carrier_that_says_core_is_a_fail
t test_a_build_record_that_is_absent_is_could_not_tell
t test_a_digest_that_refuses_is_a_fail
t test_a_build_that_is_not_untagged_stops_the_digest_row
t test_a_run_that_never_ends_is_could_not_tell
t test_the_record_of_another_run_is_a_fail
t test_a_governance_block_already_there_stops_before_the_run
t test_a_dirty_fugaro_yaml_stops_before_writing
t test_belong_in_the_checkout_path_is_refused
t test_belong_in_the_repository_name_is_refused
t test_belong_in_the_project_stops_the_sandbox_step
t test_a_coding_agents_session_is_refused
t test_an_older_release_stops
t test_a_dry_run_prints_the_plan_and_makes_no_call
t test_a_dry_run_still_refuses_belong
t test_a_run_without_yes_asks_for_it
t test_usage_and_arguments
t test_a_checkout_that_is_not_one_stops
t test_a_source_tree_that_is_not_one_stops_the_digest_row
t test_a_scratch_directory_is_removed_even_when_a_step_stops

if [ "$failures" -ne 0 ]; then
  echo "check200_test: $failures failure(s)" >&2
  exit 1
fi
echo "check200_test: all tests passed" >&2
