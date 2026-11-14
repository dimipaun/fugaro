#!/usr/bin/env bash
# Check 291's own test — what its script reads, and what it does, against fakes.
#
# Nothing here touches a cloud, a network, a credential or a real installation:
# `fugaro`, `gcloud` and `gh` are fakes written into each case's own bin
# directory, git is real (against a bare repository inside the case's own
# directory), and the transcripts the check reads are files the fake `fugaro`
# writes for the fake `gcloud` to serve. What it pins is the reading of each
# fact (the stamp, the drops, the caveat, the marker present and the reserved
# name absent, the refutation on a repository-built image), the guards (belong,
# a checkout that is not the repository named, a dirty tree, a missing --yes), a
# --dry-run that calls nothing at all, and a Restore that says what it could not
# undo. No live step of Check 291 is run by an agent and none is run here.
#
# Usage: bash scripts/live-checks/check291_test.sh [CHECK_TEST_ONLY=test_name]
#                                              [CHECK_TEST_SHARD=i/n]
set -uo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
SCRIPT="$HERE/check291.sh"
[ -f "$SCRIPT" ] || { echo "check291_test: $SCRIPT is not there" >&2; exit 1; }

failures=0
fail() { echo "check291_test: FAIL: $*" >&2; failures=$((failures + 1)); }

expect_status() { # expect_status WANT GOT LABEL
  if [ "$2" != "$1" ]; then fail "$3: exit $2, wanted $1"; fi
}
expect_contains() { # expect_contains FILE TEXT LABEL
  if ! grep -qF -- "$2" "$1"; then fail "$3: '$2' is not in $1"; fi
}
expect_absent() { # expect_absent FILE TEXT LABEL
  if grep -qF -- "$2" "$1"; then fail "$3: '$2' IS in $1"; fi
}
fact_line() { # fact_line FILE KEY — the block's own line for one fact
  sed -n "s/^FACT $2: //p" "$1" | head -1
}
fact_verdict() { # fact_verdict FILE KEY — PASS, FAIL or INFO
  fact_line "$1" "$2" | sed -n 's/^\([A-Z]*\) —.*/\1/p'
}

ROOT=""
cleanup_cases() {
  if [ -n "${CHECK291_KEEP:-}" ]; then
    [ -n "$ROOT" ] && echo "check291_test: kept $ROOT (CHECK291_KEEP)" >&2
    return 0
  fi
  [ -n "$ROOT" ] && [ -d "$ROOT" ] && rm -rf "$ROOT"
}
trap cleanup_cases EXIT

# setup_case NAME — one case's whole world, in its own directory:
#   origin/    a bare repository (git really pushes here)
#   checkout/  the sandbox checkout, with fugaro.yaml and origin configured
#   home/      HOME and XDG_CONFIG_HOME, holding the project's own config
#   state/     what the fakes read and write: the job's state, the held names,
#              the runs bucket and the transcripts under it, and a knobs/
#              directory a case writes to bend one reading
#   bin/       the fake fugaro, gcloud and gh
#   tmp/       TMPDIR, so the check's scratch directory lands here
setup_case() {
  local name=$1
  ROOT=$(mktemp -d "${TMPDIR:-/tmp}/chk291test.XXXXXXXX") || exit 1
  CASE=$ROOT/$name
  mkdir -p "$CASE"/{bin,state/knobs,state/bucket,home/.config/fugaro/projects,tmp,checkout}
  ORIGIN=$CASE/origin
  CHECKOUT=$CASE/checkout
  STATE=$CASE/state
  LOG=$STATE/fugaro.log
  GLOG=$STATE/gcloud.log
  HLOG=$STATE/gh.log
  : >"$LOG"
  : >"$GLOG"
  : >"$HLOG"

  git init -q --bare "$ORIGIN" >/dev/null 2>&1
  git -C "$CHECKOUT" init -q -b main >/dev/null 2>&1
  git -C "$CHECKOUT" config user.name "check291 test"
  git -C "$CHECKOUT" config user.email "check291@fugaro.invalid"
  cat >"$CHECKOUT/fugaro.yaml" <<'EOF'
version: 1
workflows:
  test:
    needs: base
    commands:
      build: |
        set -eu
        echo build
EOF
  git -C "$CHECKOUT" add fugaro.yaml
  git -C "$CHECKOUT" commit -q -m "fixture"
  cp "$CHECKOUT/fugaro.yaml" "$STATE/fugaro.yaml.orig"
  git -C "$CHECKOUT" remote add origin "$ORIGIN"
  git -C "$CHECKOUT" push -q -u origin main
  : >"$STATE/job-state"
  echo none >"$STATE/job-state"
  : >"$STATE/held"

  cat >"$CASE/home/.config/fugaro/projects/chk291-sandbox.yaml" <<EOF
version: 1
name: chk291-sandbox
gcp_project: chk291-proj
region: us-east5
runs_bucket: chk291-bucket
EOF

  cat >"$STATE/doctor.json" <<'EOF'
{"version": 3, "project": {"name": "chk291-sandbox", "gcp_project": "chk291-proj"}}
EOF

  cp "$CASE/home/.config/fugaro/projects/chk291-sandbox.yaml" "$STATE/ls-base"
  cat >"$STATE/image-ls.txt.base" <<'EOF'
base-image  base  sha256:0000
EOF

  fake_fugaro
  fake_gcloud
  fake_gh
  export CHK291_STATE=$STATE
  export CHK291_CHECKOUT=$CHECKOUT
}

fake_fugaro() {
  cat >"$CASE/bin/fugaro" <<'FAKE'
#!/usr/bin/env bash
# A fake fugaro: the readings Check 291 makes off its answers come out of the
# case's own state files, so a case bends a reading by writing one.
state=${CHK291_STATE:?}
co=${CHK291_CHECKOUT:?}
printf 'fugaro %s\n' "$*" >>"$state/fugaro.log"
knob() { [ -e "$state/knobs/$1" ]; }
entry=fugaro-check291
job=chk291-sandbox-test

job_line() {
  local jobstate held caveat names
  jobstate=$(cat "$state/job-state" 2>/dev/null || echo none)
  held=$(cat "$state/held" 2>/dev/null || true)
  caveat=" (an image's own ENV lives in the image and not in the job, so what that image declares is not in this line)"
  knob no_caveat && caveat=""
  local suffix=""
  knob valued && suffix=" (NEXUS_BASEURL=https://example.invalid/)"
  if [ "$jobstate" != stamp ]; then
    echo "info image-env-$job: the job's image is not catalog-stamped — no project image_catalog entry chose it (a repository-built image, a job deployed before the stamp, or a project job, which runs the installation's base image) — so a run of it passes none of its image's own variables"
    return
  fi
  if [ -n "$held" ]; then
    names="${held//$'\n'/, }"
    echo "info image-env-$job: the job's image is catalog entry $entry, and a run drops 1 name(s) the job sets itself: $names — Fugaro reserves them, so nothing an image sets under them can shadow the routing, the credentials or a declared secret$caveat$suffix"
  else
    echo "info image-env-$job: the job's image is catalog entry $entry, and a run drops none of the names the job sets itself — the rest of its environment is Fugaro's own, which configures the run$caveat$suffix"
  fi
}

case "$1" in
doctor)
  case "${2:-}" in
  --json) cat "$state/doctor.json" ;;
  --all)
    knob no_imageenv_line && exit 0
    job_line
    echo "info base-image: the base image is current"
    ;;
  *) cat "$state/doctor.json" ;;
  esac
  ;;
image)
  case "${2:-}" in
  render)
    # The derived Dockerfile the deployment would build for this workflow: its
    # FROM line is the base the sandbox's own jobs run today.
    echo "FROM us-east5-docker.pkg.dev/chk291-proj/fugaro/base:latest"
    echo "RUN echo derived"
    ;;
  ls)
    cat "$state/image-ls.txt.base"
    if [ -e "$state/entry-added" ]; then
      echo "$entry  base  sha256:1111"
    fi
    ;;
  add)
    name=$3
    [ -e "$state/knobs/image_add_fails" ] && { echo "fake: add refused" >&2; exit 1; }
    touch "$state/entry-added"
    printf '%s\n' "$name" >"$state/entry-name"
    ;;
  build)
    if [ "${3:-}" = "--workflow" ]; then
      echo "built us-east5-docker.pkg.dev/chk291-proj/fugaro/repo-test@sha256:2222"
      exit 0
    fi
    [ -e "$state/knobs/build_fails" ] && { echo "fake: build failed" >&2; exit 1; }
    echo "built us-east5-docker.pkg.dev/chk291-proj/fugaro/catalog-$3@sha256:1111"
    ;;
  rm)
    rm -f "$state/entry-added"
    ;;
  esac
  ;;
update)
  if [ "${2:-}" = "--repo" ]; then
    # What the checkout's fugaro.yaml says now is what the converge deploys: the
    # job carries the stamp exactly while a workflow names the entry.
    if grep -qE "^ +image_name: $entry\$" "$co/fugaro.yaml"; then
      echo stamp >"$state/job-state"
    elif grep -qE "^ +dockerfile:" "$co/fugaro.yaml"; then
      if [ -e "$state/knobs/refutation_still_stamped" ]; then echo stamp >"$state/job-state"; else echo dockerfile >"$state/job-state"; fi
    else
      echo none >"$state/job-state"
    fi
    # The converge redeploys the job from the deployment's own environment, so a
    # name set on it by hand is gone afterwards.
    : >"$state/held"
  fi
  echo "updated"
  ;;
run)
  n=$(cat "$state/run-count" 2>/dev/null || echo 0)
  n=$((n + 1))
  echo "$n" >"$state/run-count"
  id="00000000-0000-4000-8000-00000000000$n"
  echo "launched ${id%%-*}"
  echo "  branch fugaro/$id cut from main"
  echo "  logs https://fugaro.invalid/logs/$id"
  d="$state/bucket/runs/chk291-sandbox/$id/transcripts"
  mkdir -p "$d"
  if [ "$n" = 1 ]; then
    printf '%s\n' '{"type":"result","text":"marker: present reserved: absent"}' >"$d/implement-1.jsonl"
    [ -e "$state/knobs/run1_marker_absent" ] && printf '%s\n' '{"text":"marker: absent reserved: absent"}' >"$d/implement-1.jsonl"
    [ -e "$state/knobs/run1_reserved_present" ] && printf '%s\n' '{"text":"marker: present reserved: present"}' >"$d/implement-1.jsonl"
    [ -e "$state/knobs/run1_value" ] && printf '%s\n' '{"text":"marker: present reserved: absent env=https://example.invalid/x"}' >"$d/implement-1.jsonl"
    [ -e "$state/knobs/run1_no_reserved_line" ] && printf '%s\n' '{"text":"marker: present"}' >"$d/implement-1.jsonl"
  else
    printf '%s\n' '{"text":"marker: absent reserved: absent"}' >"$d/implement-1.jsonl"
  fi
  # The branch the run cut, which the Restore's `gh pr close --delete-branch`
  # takes off the remote again.
  git -C "$co" push -q origin "HEAD:refs/heads/fugaro/$id"
  ;;
runs)
  case "${2:-}" in
  show)
    printf '{"version": 2, "repo": "chk291-sandbox", "run": "chk291-sandbox/%s"}\n' "$5"
    ;;
  esac
  ;;
*)
  exit 0
  ;;
esac
exit 0
FAKE
  chmod +x "$CASE/bin/fugaro"
}

fake_gcloud() {
  cat >"$CASE/bin/gcloud" <<'FAKE'
#!/usr/bin/env bash
# A fake gcloud: the job's held names and its image come out of the case's own
# state, and `storage ls`/`cat` serve the runs bucket the fake fugaro writes into.
state=${CHK291_STATE:?}
printf 'gcloud %s\n' "$*" >>"$state/gcloud.log"
bucket=chk291-bucket

find_uri() {
  local a
  for a in "$@"; do
    case "$a" in gs://*) printf '%s' "$a"; return 0 ;; esac
  done
  return 1
}

case "$1" in
run)
  [ "${2:-}" = jobs ] || exit 0
  case "${3:-}" in
  update)
    shift 3
    while [ $# -gt 0 ]; do
      case "$1" in
      --update-env-vars) printf '%s\n' "${2%%=*}" >>"$state/held" ;;
      esac
      shift
    done
    ;;
  describe)
    # The job's image follows the checkout's own fugaro.yaml: a dockerfile: is
    # the repository's derived image, an image_name:/nothing is the catalog
    # entry's. A knob that bends the DOCTOR line must not bend this one too, or a
    # case could not tell which reading failed.
    if grep -qE "^ +dockerfile:" "${CHK291_CHECKOUT:?}/fugaro.yaml"; then
      echo "us-east5-docker.pkg.dev/chk291-proj/fugaro/repo-test@sha256:2222"
    else
      echo "us-east5-docker.pkg.dev/chk291-proj/fugaro/catalog-fugaro-check291@sha256:1111"
    fi
    ;;
  esac
  ;;
storage)
  uri=$(find_uri "$@") || exit 1
  path=${uri#gs://$bucket/}
  case "${2:-}" in
  ls)
    [ "${3:-}" = "--recursive" ] || exit 0
    ls -1 "$state/bucket/$path" 2>/dev/null | sed "s|^|gs://$bucket/$path|"
    ;;
  cat)
    for a in "$@"; do
      case "$a" in
      gs://*) cat "${state}/bucket/${a#gs://$bucket/}" 2>/dev/null ;;
      esac
    done
    ;;
  esac
  ;;
esac
exit 0
FAKE
  chmod +x "$CASE/bin/gcloud"
}

fake_gh() {
  cat >"$CASE/bin/gh" <<'FAKE'
#!/usr/bin/env bash
# A fake gh: `pr close` really deletes the branch on the case's own remote, so
# the check's read-back of the branch is a reading of the remote and not of the
# fake's word.
state=${CHK291_STATE:?}
co=${CHK291_CHECKOUT:?}
printf 'gh %s\n' "$*" >>"$state/gh.log"
case "$1" in
pr)
  case "${2:-}" in
  close)
    [ -e "$state/knobs/pr_close_fails" ] && { echo "fake: close refused" >&2; exit 1; }
    git -C "$co" push -q origin --delete "$3" 2>/dev/null || true
    ;;
  view)
    if [ -e "$state/knobs/pr_still_open" ]; then echo OPEN; else echo CLOSED; fi
    ;;
  esac
  ;;
esac
exit 0
FAKE
  chmod +x "$CASE/bin/gh"
}

# run291 ARGS... — the check, from the case's own checkout, with the fakes on
# PATH, HOME and XDG_CONFIG_HOME inside the case, and TMPDIR inside it too. Its
# own stdout (the RESULT block) lands in $CASE/out and stderr in $CASE/err.
run291() {
  : >"$LOG"
  : >"$GLOG"
  : >"$HLOG"
  (
    cd "$CHECKOUT" || exit 1
    PATH="$CASE/bin:$PATH" \
      HOME="$CASE/home" \
      XDG_CONFIG_HOME="$CASE/home/.config" \
      TMPDIR="$CASE/tmp" \
      CHK291_STATE="$STATE" \
      CHK291_CHECKOUT="$CHECKOUT" \
      bash "$SCRIPT" "$@"
  ) >"$CASE/out" 2>"$CASE/err"
  echo "$?" >"$CASE/status"
}

status_of() { cat "$CASE/status"; }

# ------------------------------------------------------------------ the cases
set_origin() { git -C "$CHECKOUT" remote set-url origin "$1"; }

# The whole block's rows, in the order the script prints them.
REAL_KEYS="belong installation checkout workflow consent entry stamp reserved converged run-marker run-value refutation refutation-run restore"

assert_all_pass() { # assert_all_pass OUT LABEL
  local key
  for key in $REAL_KEYS; do
    local v
    v=$(fact_verdict "$1" "$key")
    [ "$v" = PASS ] || fail "$2: fact $key reads '${v:-missing}' — $(fact_line "$1" "$key")"
  done
}

test_usage_and_arguments() {
  setup_case usage
  run291 --help
  expect_status 0 "$(status_of)" "--help"
  expect_contains "$CASE/out" "usage: bash scripts/live-checks/check291.sh" "--help prints the usage"
  expect_absent "$LOG" "fugaro " "--help calls nothing"

  run291 --nonsense
  expect_status 2 "$(status_of)" "an unknown argument"

  run291 --repo owner/sandbox
  expect_status 2 "$(status_of)" "--region is required"

  run291
  expect_status 2 "$(status_of)" "--repo is required"

  run291 --repo owner/sandbox --region us-east5 --workflow 'a b'
  expect_status 2 "$(status_of)" "a workflow name with a space"
  expect_contains "$CASE/err" "--workflow takes a workflow name" "the workflow guard says why"
}

test_dry_run_calls_nothing() {
  setup_case dryrun
  run291 --repo "$ORIGIN" --region us-east5 --dry-run
  expect_status 0 "$(status_of)" "a dry run"
  expect_contains "$CASE/out" "CHECK 291 PLAN" "the plan is printed"
  expect_contains "$CASE/out" "this dry run made no cloud call and no fugaro call at all" "the plan says nothing was called"
  expect_contains "$CASE/out" "FACT dry-run: PASS" "the dry-run row"
  expect_absent "$CASE/out" "FACT installation:" "a dry run offers no installation row"
  # No call at all: not one fugaro, gcloud or gh invocation reached the fakes.
  expect_absent "$LOG" "fugaro " "a dry run calls no fugaro"
  expect_absent "$GLOG" "gcloud " "a dry run calls no gcloud"
  expect_absent "$HLOG" "gh " "a dry run calls no gh"
  # And it changed nothing: the checkout is clean and no entry was published.
  [ -z "$(git -C "$CHECKOUT" status --porcelain)" ] || fail "a dry run left the checkout dirty"
  [ ! -e "$STATE/entry-added" ] || fail "a dry run published a catalog entry"
}

test_happy_path() {
  setup_case happy
  run291 --repo "$ORIGIN" --region us-east5 --yes
  expect_status 0 "$(status_of)" "the happy path"
  assert_all_pass "$CASE/out" "the happy path"

  # What it read: the entry's own line, the stamp, the reservation and the two
  # runs' lines.
  expect_contains "$CASE/out" "catalog entry fugaro-check291 is published and built" "the entry's own catalog line"
  expect_contains "$CASE/out" "a run drops none of the names the job sets itself" "the stamp's drops"
  expect_contains "$CASE/out" "GOOGLE_APPLICATION_CREDENTIALS" "the reservation's name"
  expect_contains "$CASE/out" "marker: present" "the first run's marker"
  expect_contains "$CASE/out" "marker: absent" "the refutation's marker"
  expect_contains "$CASE/out" "reads not catalog-stamped" "the refutation's doctor line"
  expect_contains "$CASE/out" "no 'example.invalid'" "the value row"

  # What it did, in the order the check's own steps do it.
  expect_contains "$LOG" "fugaro image add fugaro-check291 --inline" "the entry is added"
  expect_contains "$LOG" " --tags base" "the entry carries the workflow's own tags"
  expect_contains "$LOG" "fugaro image build fugaro-check291" "the entry is built"
  expect_contains "$LOG" "fugaro image build --workflow test" "the refutation's image is built with --workflow (D1144)"
  expect_contains "$LOG" "fugaro image rm fugaro-check291" "the Restore removes the entry"
  expect_contains "$GLOG" "--update-env-vars GOOGLE_APPLICATION_CREDENTIALS=/dev/null" "the reserved name is set on the job by hand"
  expect_contains "$HLOG" "gh pr close fugaro/" "the Restore closes the pull requests"
  [ "$(grep -c 'gh pr close' "$HLOG")" = 2 ] || fail "the Restore closed $(grep -c 'gh pr close' "$HLOG") pull request(s), wanted 2"

  # And what is left behind: nothing of this check's.
  [ ! -e "$STATE/entry-added" ] || fail "the catalog entry is still published"
  [ ! -e "$CHECKOUT/.fugaro/test.Dockerfile" ] || fail ".fugaro/test.Dockerfile is still there"
  [ ! -e "$STATE/held" ] || [ ! -s "$STATE/held" ] || fail "a name is still set on the job"
  cmp -s "$STATE/fugaro.yaml.orig" "$CHECKOUT/fugaro.yaml" ||
    fail "fugaro.yaml is not byte-for-byte what it was: $(diff "$STATE/fugaro.yaml.orig" "$CHECKOUT/fugaro.yaml" | head -5)"
  [ -z "$(git -C "$CHECKOUT" ls-remote origin 'refs/heads/fugaro/*')" ] || fail "a run's branch is still on origin"
  [ -z "$(git -C "$CHECKOUT" status --porcelain)" ] || fail "the Restore left the checkout dirty"
}

test_repo_belong_is_refused() {
  setup_case belongrepo
  run291 --repo belong/sandbox --region us-east5 --yes
  expect_status 2 "$(status_of)" "a repository named belong"
  expect_contains "$CASE/out" "FACT belong: FAIL" "the belong row"
  expect_absent "$LOG" "fugaro image" "nothing was created"

  run291 --repo edgeappinc/sandbox --region us-east5 --yes
  expect_status 2 "$(status_of)" "a repository named edgeappinc"
  expect_contains "$CASE/out" "edgeappinc" "the refused name is named"
}

test_the_origin_is_refused_by_name() {
  setup_case belongorigin
  set_origin "https://github.com/belong/sandbox.git"
  run291 --repo belong/sandbox --region us-east5 --yes
  expect_status 2 "$(status_of)" "an origin that is belong"
}

test_a_checkout_of_another_repository_is_refused() {
  setup_case otherrepo
  set_origin "https://github.com/other/sandbox.git"
  run291 --repo owner/sandbox --region us-east5 --yes
  expect_status 2 "$(status_of)" "a checkout of another repository"
  expect_contains "$CASE/out" "FACT checkout: FAIL" "the checkout row"
}

test_a_dirty_checkout_is_refused() {
  setup_case dirty
  echo "uncommitted" >>"$CHECKOUT/fugaro.yaml"
  run291 --repo "$ORIGIN" --region us-east5 --yes
  expect_status 2 "$(status_of)" "a dirty checkout"
  expect_contains "$CASE/out" "FACT checkout: FAIL" "the checkout row"
  expect_absent "$LOG" "fugaro image" "nothing was created"
  [ "$(tail -1 "$CHECKOUT/fugaro.yaml")" = uncommitted ] || fail "the check overwrote the uncommitted change"
}

test_a_missing_yes_is_refused_before_anything_is_created() {
  setup_case noconsent
  run291 --repo "$ORIGIN" --region us-east5
  expect_status 2 "$(status_of)" "a run without --yes"
  expect_contains "$CASE/out" "FACT consent: FAIL" "the consent row"
  expect_contains "$CASE/out" "two trivial runs" "the bound is printed in the block"
  expect_contains "$CASE/err" "--yes to consent" "the bound is printed to the owner"
  expect_absent "$LOG" "fugaro image" "nothing was created"
  [ ! -e "$STATE/entry-added" ] || fail "an entry was published without consent"
}

test_a_workflow_that_already_names_an_image_is_refused() {
  setup_case wfimage
  python3 - "$CHECKOUT/fugaro.yaml" <<'PY'
import sys
p = sys.argv[1]
s = open(p).read().replace("  test:\n", "  test:\n    image: some.registry/other:1\n", 1)
open(p, "w").write(s)
PY
  git -C "$CHECKOUT" commit -q -am "the workflow names an image"
  run291 --repo "$ORIGIN" --region us-east5 --yes
  expect_status 2 "$(status_of)" "a workflow that names its own image"
  expect_contains "$CASE/out" "FACT workflow: FAIL" "the workflow row"
  expect_absent "$LOG" "fugaro image add" "nothing was created"
}

test_two_workflows_need_a_name() {
  setup_case twowf
  python3 - "$CHECKOUT/fugaro.yaml" <<'PY'
import sys
p = sys.argv[1]
s = open(p).read()
s += "  second:\n    commands:\n      build: |\n        set -eu\n        echo second\n"
open(p, "w").write(s)
PY
  git -C "$CHECKOUT" commit -q -am "a second workflow"
  run291 --repo "$ORIGIN" --region us-east5 --yes
  expect_status 2 "$(status_of)" "two workflows with no --workflow"
  expect_contains "$CASE/err" "--workflow <name>" "the stop names the flag"
}

# ------------------------------------------------- the readings a case bends
test_a_marker_that_never_arrives_is_an_answer() {
  setup_case markerabsent
  : >"$STATE/knobs/run1_marker_absent"
  run291 --repo "$ORIGIN" --region us-east5 --yes
  expect_status 1 "$(status_of)" "a first run whose marker is absent"
  expect_contains "$CASE/out" "FACT run-marker: FAIL" "the marker row"
  expect_contains "$CASE/out" "did not reach the environment of a command the run ran through fugaro verify" "the row says what the reading means"
  [ "$(fact_verdict "$CASE/out" run-value)" = FAIL ] || fail "a run that stopped at the marker read past it"
}

test_a_reserved_name_that_reaches_the_run_is_an_answer() {
  setup_case reservedpresent
  : >"$STATE/knobs/run1_reserved_present"
  run291 --repo "$ORIGIN" --region us-east5 --yes
  expect_status 1 "$(status_of)" "a first run whose reserved name is present"
  expect_contains "$CASE/out" "FACT run-marker: FAIL" "the marker row"
  expect_contains "$CASE/out" "which is the one thing the passthrough exists to prevent" "the row says which reservation was defeated"
}

test_a_value_in_the_transcript_is_an_answer() {
  setup_case valueleak
  : >"$STATE/knobs/run1_value"
  run291 --repo "$ORIGIN" --region us-east5 --yes
  expect_status 1 "$(status_of)" "a transcript carrying the image's value"
  expect_contains "$CASE/out" "FACT run-value: FAIL" "the value row"
  expect_contains "$CASE/out" "D1375 says no reading of it ever does" "the row cites the rule"
}

test_a_stamp_whose_line_lost_its_caveat_is_an_answer() {
  setup_case nocaveat
  : >"$STATE/knobs/no_caveat"
  run291 --repo "$ORIGIN" --region us-east5 --yes
  expect_status 1 "$(status_of)" "a doctor line with no caveat"
  expect_contains "$CASE/out" "FACT stamp: FAIL" "the stamp row"
  expect_contains "$CASE/out" "carries no caveat that an image's own ENV is not in it" "the row says what is missing"
}

test_a_stamp_whose_line_carries_a_value_is_an_answer() {
  setup_case valued
  : >"$STATE/knobs/valued"
  run291 --repo "$ORIGIN" --region us-east5 --yes
  expect_status 1 "$(status_of)" "a doctor line carrying a value"
  expect_contains "$CASE/out" "FACT stamp: FAIL" "the stamp row"
  expect_contains "$CASE/out" "never a value (D1375)" "the row cites the rule"
}

test_a_refutation_that_stays_stamped_is_an_answer() {
  setup_case stillstamped
  : >"$STATE/knobs/refutation_still_stamped"
  run291 --repo "$ORIGIN" --region us-east5 --yes
  expect_status 1 "$(status_of)" "a job that keeps the stamp on a repository-built image"
  expect_contains "$CASE/out" "FACT refutation: FAIL" "the refutation row"
  expect_contains "$CASE/out" "still reads catalog-stamped" "the row names the reading"
  expect_contains "$CASE/out" "chk291-sandbox-test" "the row names the job"
  # The second run never happened: the refutation is what would be read off it.
  [ "$(cat "$STATE/run-count" 2>/dev/null || echo 0)" = 1 ] || fail "a second run was launched although the refutation had already failed"
}

test_doctor_with_no_image_env_line_stops() {
  setup_case noline
  : >"$STATE/knobs/no_imageenv_line"
  run291 --repo "$ORIGIN" --region us-east5 --yes
  expect_status 2 "$(status_of)" "a doctor that reports no image-env line"
  expect_contains "$CASE/err" "too old for it" "the stop says why"
}

test_a_build_that_fails_stops_and_is_restored() {
  setup_case buildfails
  : >"$STATE/knobs/build_fails"
  run291 --repo "$ORIGIN" --region us-east5 --yes
  expect_status 2 "$(status_of)" "a build that failed"
  expect_contains "$CASE/out" "FACT entry: FAIL" "the entry row"
  # The entry it had already added is removed again.
  [ ! -e "$STATE/entry-added" ] || fail "the entry is still published after a failed build"
  cmp -s "$STATE/fugaro.yaml.orig" "$CHECKOUT/fugaro.yaml" || fail "the Restore did not put fugaro.yaml back"
}

test_a_restore_that_could_not_close_a_pull_request_says_so() {
  setup_case restorefails
  : >"$STATE/knobs/pr_close_fails"
  run291 --repo "$ORIGIN" --region us-east5 --yes
  expect_status 1 "$(status_of)" "a Restore that could not close a pull request"
  expect_contains "$CASE/out" "FACT restore: FAIL" "the restore row"
  expect_contains "$CASE/out" "STILL TO DO" "the row says what is left"
  expect_contains "$CASE/out" "gh pr close fugaro/" "the row names the command"
}

# ------------------------------------------------------------------- the run
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

bash -n "$SCRIPT" || { echo "check291_test: $SCRIPT does not parse" >&2; exit 1; }

t test_usage_and_arguments
t test_dry_run_calls_nothing
t test_happy_path
t test_repo_belong_is_refused
t test_the_origin_is_refused_by_name
t test_a_checkout_of_another_repository_is_refused
t test_a_dirty_checkout_is_refused
t test_a_missing_yes_is_refused_before_anything_is_created
t test_a_workflow_that_already_names_an_image_is_refused
t test_two_workflows_need_a_name
t test_a_marker_that_never_arrives_is_an_answer
t test_a_reserved_name_that_reaches_the_run_is_an_answer
t test_a_value_in_the_transcript_is_an_answer
t test_a_stamp_whose_line_lost_its_caveat_is_an_answer
t test_a_stamp_whose_line_carries_a_value_is_an_answer
t test_a_refutation_that_stays_stamped_is_an_answer
t test_doctor_with_no_image_env_line_stops
t test_a_build_that_fails_stops_and_is_restored
t test_a_restore_that_could_not_close_a_pull_request_says_so

if [ "$failures" -ne 0 ]; then
  echo "check291_test: $failures failure(s)" >&2
  exit 1
fi
echo "check291_test: all tests passed" >&2
