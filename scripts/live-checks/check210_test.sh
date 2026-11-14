#!/usr/bin/env bash
# Tests for scripts/live-checks/check210.sh, with fake `fugaro`, `gcloud`, `git`
# and `gh` on PATH: NOTHING here reaches a cloud, a project, a bucket, a forge or
# a run, no agent runs a live command, and the fakes record every call they are
# given — in which directory it was made — so a case can assert what the check
# did and did not do.
#
# The fakes are a small model of one sandbox machine: the fake `fugaro` answers
# version, `ab --help`, `doctor --json`, `config layer --json`, the launches (each
# one a counter, so the run IDs are the case's) and the runs list; the fake
# `gcloud` serves a record per run ID out of the case's own directory and lists
# an object unless the case says it is missing; the fake `git` holds a branch
# list the case writes and really deletes from it, and the fake `gh` holds pull
# request states it really moves from OPEN to CLOSED — so the teardown's own
# read-backs are readings of state that changed, not of a log.
#
# The cases that matter are the ones where that model is bent: a build older than
# the release, a build without the `ab` command, a layer with no `ab:` block, a
# rate that is not the premise's, no challenger, a repository that opted out, a
# layer whose `ab:` block is written in flow style, no pair in the bound, a
# challenger that did not start, an arm that never ends, a batch with a third
# row, a challenger whose record carries a branch, a publish mode or an outcome,
# a challenger that really pushed a branch, a champion with no pull request, a
# missing patch object, a report without the pair, a teardown that cannot close
# what it opened, and — of the Restore itself — a launch still going when the
# check gives up, a cancel that answers while the run keeps going, and a launch
# the runs list does not carry.
#
# Every command is bounded (see `bounded` below): a check that hangs fails here
# with the case's own name rather than hanging the suite that runs this file.
#
# Usage: bash scripts/live-checks/check210_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check210.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK210_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK210_TEST_HEAD:-900}")" ;;
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

# Every command this file runs is bounded, and so is every case: an earlier
# live-check test (check340_test.sh) hung for 25 minutes, and the suite that runs
# this file has one deadline for all of them. `timeout` is coreutils and is not
# on a stock Mac, so the fallback is a watchdog of the same shape.
TIMEOUT_BIN=""
for candidate in timeout gtimeout; do
  if command -v "$candidate" >/dev/null 2>&1; then
    TIMEOUT_BIN=$candidate
    break
  fi
done
CASE_TIMEOUT=${CHECK210_TEST_TIMEOUT:-60}

bounded() { # bounded CMD...
  local rc=0
  if [ -n "$TIMEOUT_BIN" ]; then
    "$TIMEOUT_BIN" "$CASE_TIMEOUT" "$@" || rc=$?
    return "$rc"
  fi
  "$@" &
  local pid=$!
  ( sleep "$CASE_TIMEOUT"; kill -TERM "$pid" 2>/dev/null ) &
  local watcher=$!
  wait "$pid" || rc=$?
  kill "$watcher" 2>/dev/null || true
  wait "$watcher" 2>/dev/null || true
  return "$rc"
}

project=fugaro-chk210-test
# The repository's slug, as `fugaro runs ls --json` carries it in a row's `run`
# ref: ONE path segment, where the launch names the repository as `owner/name`.
# The record's object name is built from this one, so a case that reads the key
# back is reading the check's own choice.
slug=acme-sandbox-9f8e7d6c
# The two models the fixtures name: the champion the installation is configured
# with and the challenger the owner named in the project layer.
champion_model=claude-opus-4-1
challenger_model=claude-sonnet-5
# The run IDs the fake launcher hands out, by launch number: the check's own
# counter is what names them, and a challenger's is its champion's with a `b`.
run_of() { printf 'chk210-run-%s' "$1"; }
challenger_of() { printf 'chk210-run-%sb' "$1"; }

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check210.sh's test only. It
# answers the account read the check makes, serves ONE run record per run ID out
# of the case's own directory (so the two arms' records are two documents), and
# lists an object unless the case planted a marker saying it is missing. Nothing
# here reaches a real project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}

# The run ID out of a runs/<slug>/<id>/... object name: the segment after the
# slug, which is what the record and the patch object are keyed by.
run_of_url() {
  printf '%s' "$1" | awk -F/ '{ s = 0; for (i = 1; i <= NF; i++) if ($i == "runs") s = i; if (s) print $(s + 2) }'
}

case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    case "$mode" in
    belong-account) printf 'chk210-owner@belong.example\n' ;;
    *) printf 'chk210-owner@example.com\n' ;;
    esac
  fi
  ;;
storage)
  case "${2:-}" in
  cat)
    url=${3:-}
    id=$(run_of_url "$url")
    if [ "$mode" = record-missing ]; then
      echo "ERROR: (gcloud.storage.cat) NotFound: No such object: $url" >&2
      exit 1
    fi
    if [ ! -f "$FAKE_JSON_DIR/records/$id.json" ]; then
      echo "ERROR: (gcloud.storage.cat) NotFound: No such object: $url" >&2
      exit 1
    fi
    cat "$FAKE_JSON_DIR/records/$id.json"
    ;;
  ls)
    url=${3:-}
    id=$(run_of_url "$url")
    if [ -f "$FAKE_JSON_DIR/missing-patch-$id" ]; then
      echo "ERROR: One or more URLs matched no objects: $url" >&2
      exit 1
    fi
    printf '%s\n' "$url"
    ;;
  *)
    echo "gcloud: unexpected storage subcommand ${2:-}" >&2
    exit 1
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
# A stand-in for `fugaro`, for scripts/live-checks/check210.sh's test only. The
# `--json` reads come from files the case wrote, `ab --help` prints the usage
# line the real command prints (the check's offline feature probe), the launches
# are a counter the case reads back, `cancel` really ends the run it names (the
# teardown's read-back is what sees it), and every call is logged with the
# directory it was made in.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}

# The rows `fugaro runs ls` answers with: one per launch the fake launcher made,
# plus the challenger's own row for a launch that sampled a pair. The mode bends
# the shape a case is about.
# ended_yes ID: whether a run the fake `cancel` stopped reads as ended. The
# cancel really changes what the runs list says, which is what makes the
# teardown's own read-back a reading of state that changed.
ended_yes() {
  [ -f "$FAKE_JSON_DIR/ended" ] && grep -qx "$1" "$FAKE_JSON_DIR/ended"
}

rows_doc() {
  local want=$1 id batch arm pr branch outcome terminal status runref
  local first=1
  printf '{"project":"%s","runs":[' "$FAKE_PROJECT"
  while read -r id batch; do
    [ -n "$id" ] || continue
    for arm in champion challenger; do
      [ "$arm" = challenger ] && [ "$batch" = "-" ] && continue
      if [ "$arm" = challenger ]; then
        id="$id"b
      fi
      if [ -n "$want" ] && [ "$batch" != "$want" ]; then continue; fi
      if [ -f "$FAKE_JSON_DIR/omit" ] && grep -qx "$id" "$FAKE_JSON_DIR/omit"; then continue; fi
      terminal=true
      status=succeeded
      branch="fugaro/$id"
      outcome=pr
      pr=41
      if [ "$arm" = challenger ]; then
        branch=""
        outcome=none
        pr=0
        case "$mode" in
        # An arm that does not end on its own — and that the teardown's cancel
        # does end, which is what the run's own row reads afterwards.
        arms-not-ended)
          if ! ended_yes "$id"; then
            terminal=false
            status=running
          fi
          ;;
        esac
      fi
      # A launch in flight: the run has not ended, and it reads as ended only
      # once something stops it. `rows-running-stuck` is the run nothing stops.
      case "$mode" in
      rows-running | rows-running-stuck)
        if [ "$mode" = rows-running-stuck ] || ! ended_yes "$id"; then
          terminal=false
          status=running
        fi
        ;;
      esac
      runref="$FAKE_SLUG/$id"
      # The record's key is read out of this ref, so a row whose ref names
      # another run is the one shape that would make the check read a record
      # that is not its arm's.
      if [ "$mode" = arms-renamed ]; then runref="$FAKE_SLUG/$id-elsewhere"; fi
      [ "$first" = 1 ] || printf ','
      first=0
      printf '{"run":"%s","repo":"acme/sandbox","run_id":"%s","status":"%s","stage":"finalize","terminal":%s,"settled":%s,"outcome":"%s","branch":"%s","pr":%s,"batch":"%s"}' \
        "$runref" "$id" "$status" "$terminal" "$terminal" "$outcome" "$branch" "$pr" "$batch"
    done
    if [ "$mode" = batch-extra ] && [ "$batch" = "$want" ]; then
      printf ',{"run":"%s/%s-extra","repo":"acme/sandbox","run_id":"%s-extra","status":"succeeded","stage":"finalize","terminal":true,"settled":true,"outcome":"none","branch":"","pr":0,"batch":"%s"}' \
        "$FAKE_SLUG" "$id" "$id" "$batch"
    fi
  done <"$FAKE_JSON_DIR/launched"
  printf '],"totals":{},"warnings":[]}\n'
}

case "${1:-}" in
version)
  case "$mode" in
  old-version) printf '0.23.0\n' ;;
  dev-build) printf 'dev-9f8e7d6\n' ;;
  *) printf '0.24.0\n' ;;
  esac
  ;;
ab)
  # `ab --help` is the check's offline feature probe, so the fake answers it
  # with the usage line the real command prints rather than with the report —
  # and before the modes below, because a build whose report fails still has the
  # command.
  case "${2:-}" in
  --help | -h)
    case "$mode" in
    no-feature)
      printf 'fugaro: this build was made without the features this command needs (see D522)\n' >&2
      exit 4
      ;;
    esac
    printf 'Usage:\n  fugaro ab [flags]\n\nFlags:\n      --repo string    only this repository (owner/name)\n      --since string   how far back to read: Nd, a Go duration, or 0 (default "30d")\n      --json           print machine-readable output\n'
    exit 0
    ;;
  esac
  case "$mode" in
  ab-fails)
    printf 'fugaro: the report could not be read\n' >&2
    exit 1
    ;;
  esac
  cat "$FAKE_JSON_DIR/ab.json"
  ;;
doctor)
  cat "$FAKE_JSON_DIR/doctor.json"
  ;;
config)
  if [ "${2:-}" = "layer" ]; then
    case "$mode" in
    layer-missing)
      printf 'fugaro: no project layer applies to this checkout (its fugaro.yaml needs gcp_project:, and the project must publish one with fugaro config publish)\n' >&2
      exit 1
      ;;
    layer-unreadable)
      printf 'not a document at all\n'
      exit 0
      ;;
    esac
    cat "$FAKE_JSON_DIR/layer.json"
  else
    echo "fugaro: unexpected config subcommand ${2:-}" >&2
    exit 1
  fi
  ;;
run)
  case "$mode" in
  run-fails)
    printf 'fugaro: the launch was refused\n' >&2
    exit 1
    ;;
  launch-hangs)
    sleep 300
    ;;
  launch-nameless)
    printf '{"project":"%s","run":"","repo":"acme/sandbox","run_id":"","branch":"","status":"launched"}\n' "$FAKE_PROJECT"
    exit 0
    ;;
  esac
  n=$(cat "$FAKE_JSON_DIR/launch-count" 2>/dev/null || echo 0)
  n=$((n + 1))
  printf '%s\n' "$n" >"$FAKE_JSON_DIR/launch-count"
  id="chk210-run-${n}"
  after=${FAKE_PAIR_AFTER:-0}
  if [ "$n" -gt "$after" ]; then
    batch="ab-pair-${id}"
    printf '%s %s\n' "$id" "$batch" >>"$FAKE_JSON_DIR/launched"
    printf '{"project":"%s","run":"%s","repo":"acme/sandbox","run_id":"%s","branch":"fugaro/%s","execution":"exec","status":"launched","pair":{"id":"pair-%s","batch":"%s","challenger":"%sb"}}\n' \
      "$FAKE_PROJECT" "$n" "$id" "$id" "$id" "$batch" "$id"
    exit 0
  fi
  printf '%s %s\n' "$id" "-" >>"$FAKE_JSON_DIR/launched"
  case "$mode" in
  pair-skipped)
    printf '{"project":"%s","run":"%s","repo":"acme/sandbox","run_id":"%s","branch":"fugaro/%s","execution":"exec","status":"launched","pair":{"reason":"%s"}}\n' \
      "$FAKE_PROJECT" "$n" "$id" "$id" "${FAKE_SKIP_REASON:-daily-count}"
    ;;
  pair-broken)
    printf '{"project":"%s","run":"%s","repo":"acme/sandbox","run_id":"%s","branch":"fugaro/%s","execution":"exec","status":"launched","pair":{"id":"pair-%s","batch":"ab-pair-%s","reason":"%s"}}\n' \
      "$FAKE_PROJECT" "$n" "$id" "$id" "$id" "$id" "${FAKE_SKIP_REASON:-not-allowed}"
    ;;
  *)
    printf '{"project":"%s","run":"%s","repo":"acme/sandbox","run_id":"%s","branch":"fugaro/%s","execution":"exec","status":"launched"}\n' \
      "$FAKE_PROJECT" "$n" "$id" "$id"
    ;;
  esac
  ;;
cancel)
  # The teardown's stop. It really changes what the runs list answers: the run
  # reads as ended afterwards, which is the state the check reads back.
  id=${2:-}
  printf '%s\n' "$id" >>"$FAKE_JSON_DIR/cancelled"
  case "$mode" in
  cancel-fails)
    printf 'fugaro: the run could not be cancelled\n' >&2
    exit 1
    ;;
  rows-running-stuck)
    printf '{"project":"%s","run":"%s","status":"running"}\n' "$FAKE_PROJECT" "$id"
    exit 0
    ;;
  esac
  printf '%s\n' "$id" >>"$FAKE_JSON_DIR/ended"
  printf '{"project":"%s","run":"%s","status":"cancelled","marker":true,"hard":true}\n' "$FAKE_PROJECT" "$id"
  ;;
runs)
  case "$mode" in
  rows-fails)
    printf 'fugaro: the runs could not be listed\n' >&2
    exit 1
    ;;
  rows-unreadable)
    printf 'not a document at all\n'
    exit 0
    ;;
  esac
  batch=""
  prev=""
  for a in "$@"; do
    [ "$prev" = "--batch" ] && batch=$a
    prev=$a
  done
  rows_doc "$batch"
  ;;
*)
  echo "fugaro: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# The fake `git` holds the remote's branch list in a file the case writes and
# really deletes from it, so the teardown's own read-back is a reading of state
# that changed.
write_fake_git() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/git" <<'GIT_EOF'
#!/usr/bin/env bash
# A stand-in for `git`, for scripts/live-checks/check210.sh's test only. It
# answers the two calls the check makes: the remote's branch list, and the
# delete. Nothing here reaches a real remote.
set -euo pipefail
printf 'git %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/git.calls"
mode=${FAKE_MODE:-default}
branches="$FAKE_JSON_DIR/branches"

case "${1:-}" in
ls-remote)
  if [ "$mode" = remote-fails ]; then
    printf 'fatal: could not read from remote repository\n' >&2
    exit 128
  fi
  pattern=${4:-}
  [ -f "$branches" ] || exit 0
  while read -r b; do
    [ -n "$b" ] || continue
    ref="refs/heads/$b"
    case "$ref" in
    $pattern) printf '0000000000000000000000000000000000000000\t%s\n' "$ref" ;;
    esac
  done <"$branches"
  ;;
push)
  if [ "$mode" = push-fails ]; then
    printf '! [remote rejected] (pre-receive hook declined)\n' >&2
    exit 1
  fi
  grep -vx "${4:-}" "$branches" >"$branches.tmp" || true
  mv "$branches.tmp" "$branches"
  ;;
*)
  echo "git: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
GIT_EOF
  chmod +x "$1/bin/git"
}

# The fake `gh` holds pull request states and really moves them, so the
# teardown's own read-back is a reading of state that changed.
write_fake_gh() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gh" <<'GH_EOF'
#!/usr/bin/env bash
# A stand-in for `gh`, for scripts/live-checks/check210.sh's test only. It holds
# the state of the pull requests the case planted and moves it on `pr close`.
set -euo pipefail
printf 'gh %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gh.calls"
mode=${FAKE_MODE:-default}
prs="$FAKE_JSON_DIR/prs"

state_of() {
  awk -v n="$1" '$1 == n { print $2; found = 1 } END { if (!found) print "OPEN" }' "$prs" 2>/dev/null
}
set_state() {
  awk -v n="$1" -v s="$2" '$1 == n { $2 = s } { print }' "$prs" >"$prs.tmp" 2>/dev/null || true
  mv "$prs.tmp" "$prs"
}

case "${1:-}" in
pr)
  case "${2:-}" in
  view)
    printf '{"number":%s,"state":"%s"}\n' "${3:-0}" "$(state_of "${3:-0}")"
    ;;
  close)
    if [ "$mode" = pr-close-fails ]; then
      printf 'gh: could not close pull request %s\n' "${3:-0}" >&2
      exit 1
    fi
    set_state "${3:-0}" CLOSED
    ;;
  *)
    echo "gh: unexpected pr subcommand ${2:-}" >&2
    exit 1
    ;;
  esac
  ;;
*)
  echo "gh: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
GH_EOF
  chmod +x "$1/bin/gh"
}

# ------------------------------------------------------------------ documents
write_docs() { # dir
  local dir=$1 json=$1/json
  mkdir -p "$json/records"
  write_doctor "$dir"
  write_layer "$dir" "block" 0.1 "$challenger_model"
  write_ab "$dir" "$challenger_model" 1
  cat >"$json/layer.json" <<JSON
{"where":"gs://fugaro-config-${project}/projects/${project}/layer.yaml","generation":7,"sha256":"3f2a1b0c","yaml":"$(layer_yaml block 0.1 "$challenger_model" 5)"}
JSON
}

# layer_yaml STYLE RATE CHALLENGERS [MAX_PAIRS]: the `ab:` block as the project
# layer's file writes it, JSON-escaped for the layer document. STYLE is `block`
# or `flow` (the two shapes a YAML reader here has to read).
layer_yaml() { # layer_yaml STYLE RATE CHALLENGERS MAX
  local style=$1 rate=$2 challengers=$3 max=${4:-5}
  if [ "$style" = "flow" ]; then
    printf 'agent:\\n  models:\\n    coder: %s\\nab: {rate: %s, challengers: [%s], max_pairs_per_day: %s}\\nworkflow: agent\\n' \
      "$champion_model" "$rate" "$challengers" "$max"
    return 0
  fi
  printf 'agent:\\n  models:\\n    coder: %s\\nab:\\n  rate: %s\\n  challengers:\\n    - %s\\n  max_pairs_per_day: %s\\nworkflow: agent\\n' \
    "$champion_model" "$rate" "$challengers" "$max"
}

# write_layer DIR STYLE RATE CHALLENGERS [MAX]: the project layer as
# `fugaro config layer --json` hands it over. CHALLENGERS "" is a block with an
# empty list; RATE "none" drops the key.
write_layer() { # write_layer DIR STYLE RATE CHALLENGERS [MAX]
  local dir=$1 style=$2 rate=$3 challengers=$4 max=${5:-5} yaml
  if [ "$rate" = "none" ]; then
    yaml=$(printf 'ab:\\n  max_pairs_per_day: %s\\n' "$max")
  elif [ -z "$challengers" ]; then
    yaml=$(printf 'ab:\\n  rate: %s\\n  challengers: []\\n  max_pairs_per_day: %s\\n' "$rate" "$max")
  else
    yaml=$(layer_yaml "$style" "$rate" "$challengers" "$max")
  fi
  cat >"$dir/json/layer.json" <<JSON
{"where":"gs://fugaro-config-${project}/projects/${project}/layer.yaml","generation":7,"sha256":"3f2a1b0c","yaml":"$yaml"}
JSON
}

write_doctor() { # write_doctor DIR
  cat >"$1/json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[],"project":{"name":"chk210","gcp_project":"$project","region":"us-east5"}}
JSON
}

# write_champion DIR RUN_ID: the champion's own record — the arm that publishes
# as an unsampled run does, plus the pair block A3.3 writes.
write_champion() { # write_champion DIR RUN_ID
  local id=$2
  cat >"$1/json/records/$id.json" <<JSON
{"version":1,"run_id":"$id","repo":"acme/sandbox","workflow":"agent","branch":"fugaro/$id","status":"succeeded","stage":"finalize","outcome":"pr","pr":{"number":41,"url":"https://example.invalid/acme/sandbox/pull/41"},"patch_object":"runs/$slug/$id/ab/patch.diff","patch_truncated":false,"ab":{"pair_id":"pair-$id","arm":"champion"}}
JSON
}

# write_challenger DIR RUN_ID: the challenger's own record — the shadow's whole
# claim: the arm, the publish mode, the owner's model, no branch, no pull
# request, outcome none. It carries no `pr` field at all: runstore.Record.PR is
# a pointer with `omitempty`, so a run that opened no pull request has none.
write_challenger() { # write_challenger DIR RUN_ID
  local id=$2
  cat >"$1/json/records/$id.json" <<JSON
{"version":1,"run_id":"$id","repo":"acme/sandbox","workflow":"agent","branch":"","status":"succeeded","stage":"finalize","outcome":"none","patch_object":"runs/$slug/$id/ab/patch.diff","patch_truncated":false,"ab":{"pair_id":"pair-${id%b}","arm":"challenger","publish":"none","challenger":"$challenger_model","challenger_source":"owner"}}
JSON
}

# write_pair_records DIR RUN_NUMBER: both records of the pair the Nth launch
# sampled, which is what the happy path and most bends start from.
write_pair_records() { # write_pair_records DIR N
  write_champion "$1" "$(run_of "$2")"
  write_challenger "$1" "$(challenger_of "$2")"
}

# write_ab DIR CHALLENGER PAIRS: `fugaro ab --json`, with one row per challenger
# named and the pair count the case is about.
write_ab() { # write_ab DIR CHALLENGER PAIRS
  cat >"$1/json/ab.json" <<JSON
{"project":"chk210","source":"run-records","since":"2026-11-13T00:00:00Z","until":"2026-11-14T00:00:00Z","rows":[{"mode":"coder","tier":"standard","champion":"$champion_model","challenger":"$2","pairs":$3,"wins":0,"losses":0,"ties":0,"champion_halts":0,"challenger_halts":0,"champion_follow_ups":0,"judge_families":[],"judge_same_family":false,"unjudged":$3,"truncated":false}],"warnings":[]}
JSON
}

# The remote's branch list and the pull request states: the two things the
# teardown really changes, and the two the check reads back.
write_remote() { # write_remote DIR [BRANCH...]
  local dir=$1
  shift
  : >"$dir/json/branches"
  local b
  for b in "$@"; do printf '%s\n' "$b" >>"$dir/json/branches"; done
}

write_prs() { # write_prs DIR NUMBER STATE [NUMBER STATE...]
  local dir=$1
  shift
  : >"$dir/json/prs"
  while [ $# -ge 2 ]; do
    printf '%s %s\n' "$1" "$2" >>"$dir/json/prs"
    shift 2
  done
}

# A repository checkout: the check refuses a path that is not one, and every
# command is run from the checkout it belongs to. The file is also where a
# repository's own `ab:` opt-out lives.
write_checkout() { # write_checkout DIR [AB_RATE]
  mkdir -p "$1"
  printf 'project: chk210\n' >"$1/fugaro.yaml"
  if [ -n "${2:-}" ]; then
    printf 'ab:\n  rate: %s\n' "$2" >>"$1/fugaro.yaml"
  fi
}

# prepare DIR [PAIR_AFTER]: the fixtures the happy path uses, with the first
# launch sampling a pair unless the case says otherwise.
prepare() { # prepare DIR [PAIR_AFTER]
  local dir=$1 after=${2:-0}
  mkdir -p "$dir/home" "$dir/state" "$dir/logs" "$dir/tmp" "$dir/json/records"
  write_fake_gcloud "$dir"
  write_fake_fugaro "$dir"
  write_fake_git "$dir"
  write_fake_gh "$dir"
  write_docs "$dir"
  write_checkout "$dir/sandbox"
  write_checkout "$dir/other"
  : >"$dir/json/launched"
  FAKE_PAIR_AFTER=$after
  if [ "$after" = 0 ]; then
    write_pair_records "$dir" 1
    write_remote "$dir" "fugaro/$(run_of 1)"
  else
    write_remote "$dir" "fugaro/$(run_of 1)" "fugaro/$(run_of 2)" "fugaro/$(run_of $((after + 1)))"
  fi
  write_prs "$dir" 41 OPEN
}

run_check() { # run_check DIR ENV... [-- ARGS...]
  local dir=$1
  shift
  run_one "$dir" "$script" "$@"
}

run_copy() { # run_copy DIR COPY ENV... [-- ARGS...]: the same run, another script
  local dir=$1 copy=$2
  shift 2
  run_one "$dir" "$copy" "$@"
}

run_one() { # run_one DIR SCRIPT ENV... [-- ARGS...]
  local dir=$1 target=$2
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
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/git.calls"
  : >"$dir/logs/gh.calls"
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
  bounded env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_MODE="${FAKE_MODE:-default}" \
    FAKE_JSON_DIR="$dir/json" FAKE_PROJECT="$project" FAKE_SLUG="$slug" \
    FAKE_PAIR_AFTER="${FAKE_PAIR_AFTER:-0}" \
    CHECK210_SANDBOX="$dir/sandbox" CHECK210_YES=1 CHECK210_MAX_LAUNCHES=3 \
    CHECK210_RUN_TIMEOUT_SECONDS=6 CHECK210_POLL_SECONDS=1 \
    CHECK210_CANCEL_TIMEOUT_SECONDS=3 \
    CHECK210_CLI_TIMEOUT_SECONDS=20 CHECK210_LAUNCH_TIMEOUT_SECONDS=20 \
    CHECK210_DOCTOR_TIMEOUT_SECONDS=20 \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$target" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  calls=$(cat "$dir/logs/fugaro.calls")
  gcalls=$(cat "$dir/logs/gcloud.calls")
  gitcalls=$(cat "$dir/logs/git.calls")
  ghcalls=$(cat "$dir/logs/gh.calls")
}

# ------------------------------------------------------------------ assertions
assert_scratch_removed() { # dir what
  [ -n "$scratch" ] || {
    fail "$2: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$2: the scratch directory $scratch is still there"
}

assert_no_run() { # dir what
  expect_absent "$calls" "run --repo" "$2: no run is launched"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check210.sh --sandbox <a sandbox repository checkout> --yes" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, that the launches are billable, that the check is theirs
  # and that the teardown is part of it.
  expect_contains "$out" "up to THIRTY sandbox runs and what their tasks cost" "--help: the cost of the launches is stated"
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  expect_contains "$out" "closed and deleted by the Restore" "--help: the teardown is part of the check"
  assert_no_run "$dir" "--help"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK210_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 210 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version row"
  expect_contains "$out" "FACT agent-session: PASS" "dry run: the session guard"
  expect_contains "$out" "FACT belong: PASS" "dry run: the belong guard"
  expect_contains "$out" "FACT bound: PASS" "dry run: the spend is stated"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore row"
  expect_contains "$out" "CHECK 210 RESULT END (exit 0)" "dry run: the end line"
  # A row the plan did not measure reads INFO, never PASS: a plan is not a
  # reading, and a block that said PASS would be one nobody took.
  expect_contains "$out" "FACT layer: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$out" "FACT patch-objects: INFO — planned" "dry run: the patch row is planned too"
  expect_contains "$err" "up to 3 sandbox runs" "dry run: the plan names the spend"
  expect_absent "$calls" "doctor" "dry run: the installation is not read"
  expect_absent "$calls" "config layer" "dry run: the layer is not read"
  expect_absent "$gcalls" "storage" "dry run: the bucket is not read"
  expect_absent "$gitcalls" "ls-remote" "dry run: the remote is not read"
  assert_no_run "$dir" "dry run"
  assert_scratch_removed "$dir" "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "FACT sandbox: PASS" "happy path: the checkout and its project"
  expect_contains "$out" "belongs to ${project}" "happy path: the project it read"
  expect_contains "$out" "FACT layer: PASS" "happy path: the premise"
  expect_contains "$out" "sets ab.rate: 0.1 and names 1 challenger(s): ${challenger_model}" "happy path: what the premise read"
  expect_contains "$out" "FACT pair: PASS" "happy path: the launch that sampled"
  expect_contains "$out" "pair pair-$(run_of 1) on batch ab-pair-$(run_of 1)" "happy path: the pair it sampled"
  expect_contains "$out" "FACT arms: PASS" "happy path: the two arms"
  expect_contains "$out" "FACT champion: PASS" "happy path: the champion's record"
  expect_contains "$out" "FACT challenger: PASS" "happy path: the challenger's record"
  expect_contains "$out" "carries ab.arm challenger, ab.publish none, ab.challenger ${challenger_model} with challenger_source owner, outcome none, and NO branch and NO pull request" "happy path: what the shadow's record says"
  expect_contains "$out" "FACT branch: PASS" "happy path: the remote's own branch list"
  expect_contains "$out" "FACT pr: PASS" "happy path: one pull request"
  expect_contains "$out" "FACT patch-objects: PASS" "happy path: both patch objects"
  expect_contains "$out" "FACT ab-report: PASS" "happy path: the pair in the report"
  expect_contains "$out" "CHECK 210 RESULT END (exit 0)" "happy path: the end line"
  assert_scratch_removed "$dir" "happy path"
  # The pair's own commands: the launch is made from the checkout, the record is
  # read out of the project's bucket, and the remote is read in the checkout.
  expect_contains "$calls" "run --repo $dir/sandbox --json" "happy path: the launch command"
  expect_contains "$calls" "--json Add a line reading 'check 210' to CHECK210.md" "happy path: the small task the launches are made with"
  expect_contains "$gcalls" "storage cat gs://fugaro-runs-${project}/runs/${slug}/$(run_of 1)/result.json --project=${project}" "happy path: the champion's record key"
  expect_contains "$gcalls" "storage cat gs://fugaro-runs-${project}/runs/${slug}/$(challenger_of 1)/result.json --project=${project}" "happy path: the challenger's record key"
  expect_contains "$calls" "runs ls --repo $dir/sandbox --batch ab-pair-$(run_of 1) --json" "happy path: the pair read back through the command the plan names"
  expect_contains "$calls" "ab --repo acme/sandbox --since 1d --json" "happy path: the report"
}

test_a_launch_loop_reaches_the_pair_after_its_first_samples() {
  # The plan's sentence is launches until a pair appears, so the pair is not the
  # first launch: the champion is the third, and the launches before it are NOT
  # graded as if they were the pair's.
  local dir=$work/late
  prepare "$dir" 2
  write_pair_records "$dir" 3
  run_check "$dir" CHECK210_MAX_LAUNCHES=5
  expect_status "$status" 0 "a pair after two samples"
  expect_contains "$out" "FACT pair: PASS" "a pair after two samples: the row"
  expect_contains "$out" "launch 3 of at most 5 was sampled" "a pair after two samples: which launch"
  expect_contains "$out" "FACT champion: PASS" "a pair after two samples: the third launch's champion"
  expect_contains "$out" "runs/${slug}/$(run_of 3)/result.json" "a pair after two samples: the champion's record"
  expect_contains "$out" "the pair's id pair-$(run_of 3)" "a pair after two samples: the pair id is the champion's"
  # The first two launches are real runs: the check launches them and reports no
  # pair for each, and it must not have stopped at the first.
  expect_contains "$calls" "run --repo $dir/sandbox --json" "a pair after two samples: the loop launched"
  expect_contains "$out" "FACT restore: PASS" "a pair after two samples: the teardown"
  expect_contains "$gitcalls" "push origin --delete fugaro/$(run_of 1)" "a pair after two samples: the first launch's branch is deleted too"
  expect_contains "$gitcalls" "push origin --delete fugaro/$(run_of 3)" "a pair after two samples: the champion's branch is deleted"
}

test_the_teardown_closes_the_pull_request_and_deletes_the_branch() {
  local dir=$work/teardown
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the teardown"
  expect_contains "$gitcalls" "push origin --delete fugaro/$(run_of 1)" "the teardown: the branch is deleted"
  expect_contains "$ghcalls" "pr close 41 --repo acme/sandbox" "the teardown: the pull request is closed"
  # Read back afterwards: the branch really left the remote's list and the pull
  # request really moved to CLOSED, which is what the row claims.
  expect_absent "$(cat "$dir/json/branches")" "fugaro/$(run_of 1)" "the teardown: the branch is gone from the remote"
  expect_contains "$(cat "$dir/json/prs")" "41 CLOSED" "the teardown: the pull request is closed where gh holds it"
  expect_contains "$out" "FACT restore: PASS" "the teardown: the row"
  expect_contains "$out" "every pull request their champions opened is closed, each read back" "the teardown: what the row says"
  # And the challenger's own branch is never deleted, because it never existed:
  # the row would be a claim about a branch nobody pushed.
  expect_absent "$gitcalls" "push origin --delete fugaro/$(challenger_of 1)" "the teardown: no delete for the shadow's branch"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/nosandbox
  prepare "$dir"
  run_check "$dir" CHECK210_SANDBOX=
  expect_status "$status" 2 "a missing sandbox"
  expect_contains "$err" "Check 210 needs --sandbox" "a missing sandbox: the message"
  assert_no_run "$dir" "a missing sandbox"
}

test_a_path_that_is_not_a_checkout_stops_the_check() {
  local dir=$work/notacheckout
  prepare "$dir"
  mkdir -p "$dir/empty"
  run_check "$dir" CHECK210_SANDBOX="$dir/empty"
  expect_status "$status" 2 "a path that is not a checkout"
  expect_contains "$err" "carries no fugaro.yaml" "a path that is not a checkout: the message"
  assert_no_run "$dir" "a path that is not a checkout"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "xtrace"
  # A refusal that stops the check records the row it stopped on as `aborted`:
  # the guard's own key reads `FAIL — not reached`, which is what it is, and the
  # sentence the owner reads is on stderr.
  expect_contains "$out" "FACT aborted: FAIL" "xtrace: the row"
  expect_contains "$out" "FACT xtrace: FAIL — not reached" "xtrace: the guard's own row was never reached"
  expect_contains "$err" "Run it without -x" "xtrace: the remedy"
  assert_no_run "$dir" "xtrace"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$out" "FACT aborted: FAIL" "a coding agent's session: the row"
  expect_contains "$err" "Check 210 is yours to run at your own terminal" "a coding agent's session: the reason"
  assert_no_run "$dir" "a coding agent's session"
}

test_a_project_naming_belong_is_refused() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" FAKE_MODE=belong-account
  # The account naming belong is refused before anything is read: the check is
  # never run against the production installation, and the guard is by name.
  expect_status "$status" 1 "an account naming belong"
  expect_contains "$out" "FACT aborted: FAIL" "an account naming belong: the row"
  expect_contains "$err" "names belong" "an account naming belong: the reason"
  expect_contains "$err" "never run there" "an account naming belong: the refusal"
  assert_no_run "$dir" "an account naming belong"
}

test_a_checkout_of_a_belong_project_is_refused() {
  local dir=$work/belongproj
  prepare "$dir"
  cat >"$dir/json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[],"project":{"name":"belong","gcp_project":"belong-prod","region":"us-east5"}}
JSON
  run_check "$dir"
  expect_status "$status" 1 "a checkout of a belong project"
  expect_contains "$out" "FACT aborted: FAIL" "a checkout of a belong project: the row"
  expect_contains "$err" "this check is never run there" "a checkout of a belong project: the reason"
  assert_no_run "$dir" "a checkout of a belong project"
}

test_yes_is_required() {
  local dir=$work/noyes
  prepare "$dir"
  run_check "$dir" CHECK210_YES=
  expect_status "$status" 2 "no --yes"
  expect_contains "$err" "it asks for --yes" "no --yes: the message"
  expect_contains "$err" "Nothing has been launched" "no --yes: what did not happen"
  assert_no_run "$dir" "no --yes"
}

test_a_build_older_than_the_release_is_refused() {
  local dir=$work/old
  prepare "$dir"
  run_check "$dir" FAKE_MODE=old-version
  expect_status "$status" 1 "a build older than the release"
  expect_contains "$out" "FACT fugaro-version: PASS" "an old build: the version it read"
  expect_contains "$out" "0.23.0" "an old build: the version"
  expect_contains "$out" "FACT release: FAIL" "an old build: the row"
  expect_contains "$out" "predates release 0.24.0" "an old build: the reason"
  expect_contains "$out" "Run 'fugaro update' at your terminal" "an old build: the remedy"
  assert_no_run "$dir" "an old build"
}

test_a_build_without_the_ab_command_is_refused() {
  # `fugaro ab` is the Subscription's, so a core build answers with D522's stub:
  # the probe is offline and names the command's own flags.
  local dir=$work/nofeature
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-feature
  expect_status "$status" 1 "a build without the ab command"
  expect_contains "$out" "FACT release: FAIL" "a build without the ab command: the row"
  expect_contains "$out" "does not name the command's own --since flag" "a build without the ab command: the probe"
  assert_no_run "$dir" "a build without the ab command"
}

test_a_dev_build_is_decided_by_the_feature_probe() {
  local dir=$work/dev
  prepare "$dir"
  run_check "$dir" FAKE_MODE=dev-build
  expect_status "$status" 0 "a dev build"
  expect_contains "$out" "FACT release: PASS" "a dev build: the row"
  expect_contains "$out" "not a release number" "a dev build: why the probe decides"
}

test_a_layer_without_an_ab_block_is_a_finding() {
  local dir=$work/noab
  prepare "$dir"
  cat >"$dir/json/layer.json" <<JSON
{"where":"gs://fugaro-config-${project}/projects/${project}/layer.yaml","generation":7,"sha256":"3f2a1b0c","yaml":"agent:\\n  models:\\n    coder: ${champion_model}\\nworkflow: agent\\n"}
JSON
  run_check "$dir"
  expect_status "$status" 1 "a layer with no ab block"
  expect_contains "$out" "FACT layer: FAIL" "a layer with no ab block: the row"
  expect_contains "$out" "carries no top-level ab: block" "a layer with no ab block: what it found"
  expect_contains "$out" "its top-level keys are agent,workflow" "a layer with no ab block: what it does carry"
  assert_no_run "$dir" "a layer with no ab block"
}

test_a_layer_that_switched_ab_off_is_a_finding() {
  local dir=$work/rate0
  prepare "$dir"
  write_layer "$dir" block 0 "$challenger_model"
  run_check "$dir"
  expect_status "$status" 1 "a layer at rate 0"
  expect_contains "$out" "FACT layer: FAIL" "a layer at rate 0: the row"
  expect_contains "$out" "sets ab.rate: 0" "a layer at rate 0: what it found"
  assert_no_run "$dir" "a layer at rate 0"
}

test_a_layer_rate_that_is_not_the_premise_is_a_finding() {
  # The plan's sentence is `ab.rate: 0.10`, and a loop bounded for that rate
  # measures nothing at another: the comparison is numeric, so 0.10 and 0.1 are
  # one rate and 0.02 is not.
  local dir=$work/rate-low
  prepare "$dir"
  write_layer "$dir" block 0.02 "$challenger_model"
  run_check "$dir"
  expect_status "$status" 1 "a layer at 0.02"
  expect_contains "$out" "FACT layer: FAIL" "a layer at 0.02: the row"
  expect_contains "$out" "sets ab.rate: 0.02 and this check's premise is 0.1" "a layer at 0.02: what it found"
  expect_contains "$out" "widen the bound with --max-launches" "a layer at 0.02: the other lever"
  assert_no_run "$dir" "a layer at 0.02"
}

test_a_layer_whose_rate_is_written_as_0_10_holds() {
  # The plan writes the rate as 0.10 and a file may write it either way: the
  # comparison is numeric, so the row must not fail on the spelling.
  local dir=$work/rate-ten
  prepare "$dir"
  write_layer "$dir" block 0.10 "$challenger_model"
  run_check "$dir"
  expect_status "$status" 0 "a rate written as 0.10"
  expect_contains "$out" "FACT layer: PASS" "a rate written as 0.10: the row"
}

test_a_flow_style_ab_block_is_read() {
  # A layer file may write the block in flow style, and the premise is the same
  # block: a reader that only knows the indented form would report a project
  # that samples as one that does not.
  local dir=$work/flow
  prepare "$dir"
  write_layer "$dir" flow 0.1 "$challenger_model"
  run_check "$dir"
  expect_status "$status" 0 "a flow style ab block"
  expect_contains "$out" "FACT layer: PASS" "a flow style ab block: the row"
  expect_contains "$out" "names 1 challenger(s): ${challenger_model}" "a flow style ab block: the challenger it read"
}

test_a_layer_with_no_challenger_is_a_finding() {
  local dir=$work/nochallenger
  prepare "$dir"
  write_layer "$dir" block 0.1 ""
  run_check "$dir"
  expect_status "$status" 1 "a layer with no challenger"
  expect_contains "$out" "FACT layer: FAIL" "a layer with no challenger: the row"
  expect_contains "$out" "names no challenger" "a layer with no challenger: what it found"
  expect_contains "$out" "'no-challenger'" "a layer with no challenger: the closed reason it would skip with"
  assert_no_run "$dir" "a layer with no challenger"
}

test_a_repository_that_opted_out_is_a_finding() {
  # D556: a repository may lower the project's rate to zero, and a check that
  # read only the layer would launch at a rate the repository had already
  # refused.
  local dir=$work/optout
  prepare "$dir"
  write_checkout "$dir/sandbox" 0
  run_check "$dir"
  expect_status "$status" 1 "a repository that opted out"
  expect_contains "$out" "FACT layer: FAIL" "a repository that opted out: the row"
  expect_contains "$out" "carries an ab: block with rate 0, which is the opt-out" "a repository that opted out: what it found"
  assert_no_run "$dir" "a repository that opted out"
}

test_a_layer_that_cannot_be_read_is_not_measured() {
  local dir=$work/nolayer
  prepare "$dir"
  run_check "$dir" FAKE_MODE=layer-missing
  expect_status "$status" 2 "a layer that cannot be read"
  expect_contains "$out" "FACT layer: FAIL" "a layer that cannot be read: the row"
  expect_contains "$err" "could NOT measure layer" "a layer that cannot be read: the exit names the row"
  expect_contains "$out" "one 'fugaro config publish' away" "a layer that cannot be read: the remedy"
  assert_no_run "$dir" "a layer that cannot be read"
}

test_a_layer_document_that_cannot_be_read_is_not_measured() {
  local dir=$work/layerjunk
  prepare "$dir"
  run_check "$dir" FAKE_MODE=layer-unreadable
  expect_status "$status" 2 "a layer document that cannot be read"
  expect_contains "$out" "FACT layer: FAIL" "a layer document that cannot be read: the row"
  expect_contains "$err" "could NOT measure layer" "a layer document that cannot be read: the exit names the row"
  assert_no_run "$dir" "a layer document that cannot be read"
}

test_no_pair_in_the_bound_is_a_finding() {
  local dir=$work/nopair
  prepare "$dir" 99
  run_check "$dir" CHECK210_MAX_LAUNCHES=3 FAKE_MODE=pair-skipped
  expect_status "$status" 1 "no pair in the bound"
  expect_contains "$out" "FACT pair: FAIL" "no pair in the bound: the row"
  expect_contains "$out" "3 launches and none sampled a pair" "no pair in the bound: the bound it spent"
  expect_contains "$out" "were skipped with a closed reason: daily-count, daily-count, daily-count" "no pair in the bound: the reasons"
  expect_contains "$out" "widens the bound" "no pair in the bound: the one lever it has"
  # The launches really happened, three of them, and none was a launch of the
  # pair: the row above is a reading of the loop, not of a missing command.
  [ "$(grep -c 'run --repo' "$dir/logs/fugaro.calls")" = 3 ] || fail "no pair in the bound: expected 3 launches, got $(grep -c 'run --repo' "$dir/logs/fugaro.calls")"
}

test_a_launch_that_fails_is_a_finding() {
  local dir=$work/launchfails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=run-fails
  expect_status "$status" 1 "a launch that fails"
  expect_contains "$out" "FACT pair: FAIL" "a launch that fails: the row"
  expect_contains "$out" "exited 1 on launch 1" "a launch that fails: the exit"
}

test_a_launch_that_names_no_run_is_a_finding() {
  local dir=$work/nameless
  prepare "$dir"
  run_check "$dir" FAKE_MODE=launch-nameless
  expect_status "$status" 1 "a launch that names no run"
  expect_contains "$out" "FACT pair: FAIL" "a launch that names no run: the row"
  expect_contains "$out" "named no run" "a launch that names no run: what it found"
}

test_a_challenger_that_did_not_start_is_a_finding() {
  # A3.0: the pair is sampled and the champion launches alone when the second
  # arm does not start — the champion is real work, and the row says so.
  local dir=$work/nostart
  prepare "$dir" 99
  run_check "$dir" CHECK210_MAX_LAUNCHES=1 FAKE_MODE=pair-broken
  expect_status "$status" 1 "a challenger that did not start"
  expect_contains "$out" "FACT pair: FAIL" "a challenger that did not start: the row"
  expect_contains "$out" "sampled a pair (pair-$(run_of 1)) and the challenger did not start" "a challenger that did not start: what it found"
  expect_contains "$out" "this check's teardown cancels it with 'fugaro cancel $(run_of 1) --now'" "a challenger that did not start: the champion is real work, and the teardown stops it"
}

test_an_arm_that_never_ends_is_not_measured() {
  local dir=$work/neverends
  prepare "$dir"
  run_check "$dir" FAKE_MODE=arms-not-ended CHECK210_RUN_TIMEOUT_SECONDS=2
  expect_status "$status" 2 "an arm that never ends"
  expect_contains "$out" "FACT arms: FAIL" "an arm that never ends: the row"
  expect_contains "$err" "could NOT measure arms" "an arm that never ends: the exit names the row"
  expect_contains "$out" "had not both ended" "an arm that never ends: what it found"
  expect_contains "$out" "'fugaro runs show $(run_of 1)' or 'fugaro runs show $(challenger_of 1)' reads what they did" "an arm that never ends: the runs are real, and the teardown stops the one that is still going"
  expect_contains "$calls" "cancel $(challenger_of 1) --now --json" "an arm that never ends: the arm that had not ended is cancelled"
  expect_contains "$out" "FACT restore: PASS" "an arm that never ends: the teardown reads it back as ended"
}

test_a_batch_with_a_third_row_is_a_finding() {
  local dir=$work/thirdrow
  prepare "$dir"
  run_check "$dir" FAKE_MODE=batch-extra
  expect_status "$status" 1 "a batch with a third row"
  expect_contains "$out" "FACT arms: FAIL" "a batch with a third row: the row"
  expect_contains "$out" "answered 3 row(s) and the pair is two" "a batch with a third row: what it found"
}

test_rows_keyed_to_another_run_are_a_finding() {
  local dir=$work/otherrun
  prepare "$dir"
  run_check "$dir" FAKE_MODE=arms-renamed
  expect_status "$status" 1 "rows keyed to another run"
  expect_contains "$out" "FACT arms: FAIL" "rows keyed to another run: the row"
  expect_contains "$out" "this check's arms are $(run_of 1) and $(challenger_of 1)" "rows keyed to another run: what it found"
}

test_a_champion_record_that_cannot_be_read_is_not_measured() {
  local dir=$work/norecord
  prepare "$dir"
  rm -f "$dir/json/records/$(run_of 1).json"
  run_check "$dir"
  expect_status "$status" 2 "a champion record that cannot be read"
  expect_contains "$out" "FACT champion: FAIL" "a champion record that cannot be read: the row"
  expect_contains "$err" "could NOT measure champion" "a champion record that cannot be read: the exit names the row"
  expect_contains "$out" "$(run_of 1)/result.json could not be read" "a champion record that cannot be read: the key it looked for"
}

test_a_champion_record_of_another_run_is_a_finding() {
  local dir=$work/otherrecord
  prepare "$dir"
  python3 - "$dir/json/records/$(run_of 1).json" <<'PY'
import json, sys
p = sys.argv[1]
d = json.load(open(p))
d["run_id"] = "chk210-someone-else"
json.dump(d, open(p, "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a champion record of another run"
  expect_contains "$out" "FACT champion: FAIL" "a champion record of another run: the row"
  expect_contains "$out" "records run_id=chk210-someone-else" "a champion record of another run: what it found"
}

test_a_champion_whose_arm_is_not_the_champion_is_a_finding() {
  local dir=$work/champarm
  prepare "$dir"
  python3 - "$dir/json/records/$(run_of 1).json" <<'PY'
import json, sys
p = sys.argv[1]
d = json.load(open(p))
d["ab"]["arm"] = "challenger"
json.dump(d, open(p, "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a champion whose arm is not the champion"
  expect_contains "$out" "FACT champion: FAIL" "a champion whose arm is not the champion: the row"
  expect_contains "$out" "carries ab.arm 'challenger'" "a champion whose arm is not the champion: what it found"
}

test_a_challenger_document_that_cannot_be_read_is_not_measured() {
  local dir=$work/challengerjunk
  prepare "$dir"
  rm -f "$dir/json/records/$(challenger_of 1).json"
  run_check "$dir"
  expect_status "$status" 2 "a challenger record that cannot be read"
  expect_contains "$out" "FACT challenger: FAIL" "a challenger record that cannot be read: the row"
  expect_contains "$err" "could NOT measure challenger" "a challenger record that cannot be read: the exit names the row"
}

test_a_challenger_that_published_is_a_finding() {
  # The one thing the shadow may never be: a record whose own publish mode is
  # not `none` is an arm whose publishes are a question rather than a reading.
  local dir=$work/published
  prepare "$dir"
  python3 - "$dir/json/records/$(challenger_of 1).json" <<'PY'
import json, sys
p = sys.argv[1]
d = json.load(open(p))
d["ab"]["publish"] = "pr"
json.dump(d, open(p, "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a challenger that published"
  expect_contains "$out" "FACT challenger: FAIL" "a challenger that published: the row"
  expect_contains "$out" "carries ab.publish 'pr'" "a challenger that published: what it found"
}

test_a_challenger_from_another_source_is_a_finding() {
  local dir=$work/source
  prepare "$dir"
  python3 - "$dir/json/records/$(challenger_of 1).json" <<'PY'
import json, sys
p = sys.argv[1]
d = json.load(open(p))
d["ab"]["challenger_source"] = "repo"
json.dump(d, open(p, "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a challenger from another source"
  expect_contains "$out" "FACT challenger: FAIL" "a challenger from another source: the row"
  expect_contains "$out" "carries ab.challenger_source 'repo'" "a challenger from another source: what it found"
}

test_a_challenger_record_with_a_branch_is_a_finding() {
  local dir=$work/challengerbranch
  prepare "$dir"
  python3 - "$dir/json/records/$(challenger_of 1).json" <<'PY'
import json, sys
p = sys.argv[1]
d = json.load(open(p))
d["branch"] = "fugaro/chk210-run-1b"
json.dump(d, open(p, "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a challenger record with a branch"
  expect_contains "$out" "FACT challenger: FAIL" "a challenger record with a branch: the row"
  expect_contains "$out" "carries branch 'fugaro/chk210-run-1b' and pr 0" "a challenger record with a branch: what it found"
}

test_a_challenger_record_with_an_outcome_is_a_finding() {
  local dir=$work/challengeroutcome
  prepare "$dir"
  python3 - "$dir/json/records/$(challenger_of 1).json" <<'PY'
import json, sys
p = sys.argv[1]
d = json.load(open(p))
d["outcome"] = "pr"
json.dump(d, open(p, "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a challenger record with an outcome"
  expect_contains "$out" "FACT challenger: FAIL" "a challenger record with an outcome: the row"
  expect_contains "$out" "carries outcome 'pr'" "a challenger record with an outcome: what it found"
}

test_a_challenger_that_really_pushed_is_a_finding() {
  # The record's own branch field is one thing and the remote is another: the
  # check reads the branch list itself, so a shadow whose record lies is caught
  # by the forge rather than by the record.
  local dir=$work/pushed
  prepare "$dir"
  write_remote "$dir" "fugaro/$(run_of 1)" "fugaro/$(challenger_of 1)"
  run_check "$dir"
  expect_status "$status" 1 "a challenger that really pushed"
  expect_contains "$out" "FACT branch: FAIL" "a challenger that really pushed: the row"
  expect_contains "$out" "holds refs/heads/fugaro/$(challenger_of 1): the challenger PUSHED" "a challenger that really pushed: what it found"
  # The record row above still holds: the finding is the remote, and the check
  # does not report the same fact twice.
  expect_contains "$out" "FACT challenger: PASS" "a challenger that really pushed: the record's own row is not the finding"
}

test_a_champion_that_left_no_branch_is_a_finding() {
  local dir=$work/nobranch
  prepare "$dir"
  write_remote "$dir"
  run_check "$dir"
  expect_status "$status" 1 "a champion that left no branch"
  expect_contains "$out" "FACT branch: FAIL" "a champion that left no branch: the row"
  expect_contains "$out" "holds no refs/heads/fugaro/$(run_of 1)" "a champion that left no branch: what it found"
}

test_a_remote_that_cannot_be_read_is_not_measured() {
  local dir=$work/noremote
  prepare "$dir"
  run_check "$dir" FAKE_MODE=remote-fails
  expect_status "$status" 2 "a remote that cannot be read"
  expect_contains "$out" "FACT branch: FAIL" "a remote that cannot be read: the row"
  expect_contains "$err" "could NOT measure branch" "a remote that cannot be read: the exit names the row"
}

test_a_champion_without_a_pull_request_is_a_finding() {
  local dir=$work/nopr
  prepare "$dir"
  python3 - "$dir/json/records/$(run_of 1).json" <<'PY'
import json, sys
p = sys.argv[1]
d = json.load(open(p))
d.pop("pr", None)
json.dump(d, open(p, "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a champion without a pull request"
  expect_contains "$out" "FACT pr: FAIL" "a champion without a pull request: the row"
  expect_contains "$out" "the champion's record carries no pull request number" "a champion without a pull request: what it found"
}

test_a_challenger_that_opened_a_pull_request_is_a_finding() {
  local dir=$work/challengerpr
  prepare "$dir"
  python3 - "$dir/json/records/$(challenger_of 1).json" <<'PY'
import json, sys
p = sys.argv[1]
d = json.load(open(p))
d["pr"] = {"number": 42, "url": "https://example.invalid/acme/sandbox/pull/42"}
json.dump(d, open(p, "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a challenger that opened a pull request"
  expect_contains "$out" "FACT pr: FAIL" "a challenger that opened a pull request: the row"
  expect_contains "$out" "carries pull request 42" "a challenger that opened a pull request: what it found"
}

test_a_missing_patch_object_is_a_finding() {
  local dir=$work/nopatch
  prepare "$dir"
  : >"$dir/json/missing-patch-$(challenger_of 1)"
  run_check "$dir"
  expect_status "$status" 1 "a missing patch object"
  expect_contains "$out" "FACT patch-objects: FAIL" "a missing patch object: the row"
  expect_contains "$out" "does not hold a patch object for challenger" "a missing patch object: what it found"
}

test_a_report_without_the_pair_is_a_finding() {
  local dir=$work/noreport
  prepare "$dir"
  write_ab "$dir" "$champion_model" 4
  run_check "$dir"
  expect_status "$status" 1 "a report without the pair"
  expect_contains "$out" "FACT ab-report: FAIL" "a report without the pair: the row"
  expect_contains "$out" "carries no row for the challenger model ${challenger_model} with a pair in it" "a report without the pair: what it found"
}

test_a_report_at_no_pairs_is_a_finding() {
  local dir=$work/zeropairs
  prepare "$dir"
  write_ab "$dir" "$challenger_model" 0
  run_check "$dir"
  expect_status "$status" 1 "a report at no pairs"
  expect_contains "$out" "FACT ab-report: FAIL" "a report at no pairs: the row"
}

test_a_report_that_cannot_be_read_is_not_measured() {
  local dir=$work/noreportread
  prepare "$dir"
  run_check "$dir" FAKE_MODE=ab-fails
  expect_status "$status" 2 "a report that cannot be read"
  expect_contains "$out" "FACT ab-report: FAIL" "a report that cannot be read: the row"
  expect_contains "$err" "could NOT measure ab-report" "a report that cannot be read: the exit names the row"
}

test_a_teardown_that_cannot_close_is_a_finding() {
  local dir=$work/teardownfails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=push-fails
  expect_status "$status" 1 "a teardown that cannot delete a branch"
  expect_contains "$out" "FACT restore: FAIL" "a teardown that cannot delete a branch: the row"
  expect_contains "$out" "could NOT be closed or deleted" "a teardown that cannot delete a branch: what it found"
  expect_contains "$out" "closing a closed pull request and deleting a deleted branch are the state this wants" "a teardown that cannot delete a branch: the remedy is the commands, not a rerun"
}

test_a_pull_request_that_cannot_be_closed_is_a_finding() {
  local dir=$work/prfails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=pr-close-fails
  expect_status "$status" 1 "a pull request that cannot be closed"
  expect_contains "$out" "FACT restore: FAIL" "a pull request that cannot be closed: the row"
  expect_contains "$out" "pull request 41" "a pull request that cannot be closed: which one"
}

# The Restore's hardest shape: the check gives up before any pair — so no record
# is read and nothing below the launches ran — with its launches still IN FLIGHT.
# A teardown that read only what each run had left so far would report PASS over
# a branch and a pull request the run was about to push and open, so it stops
# each one with `fugaro cancel <run> --now`, reads it back as ended, and tears
# down what that run did leave.
test_a_launch_still_going_is_cancelled_and_read_back() {
  local dir=$work/inflight
  prepare "$dir" 99
  run_check "$dir" CHECK210_MAX_LAUNCHES=2 FAKE_MODE=rows-running
  expect_status "$status" 1 "a launch still going"
  expect_contains "$out" "FACT pair: FAIL" "a launch still going: no pair in the bound"
  expect_contains "$calls" "cancel $(run_of 1) --now --json" "a launch still going: the first launch is stopped"
  expect_contains "$calls" "cancel $(run_of 2) --now --json" "a launch still going: the second launch is stopped"
  expect_contains "$out" "FACT restore: PASS" "a launch still going: the teardown row"
  expect_contains "$out" "the launches that had not ended ($(run_of 1), $(run_of 2)) were cancelled with 'fugaro cancel <run> --now' and read back as ended" "a launch still going: what the row says it had to stop"
  # And the teardown is not skipped for a run that was going: the branch and the
  # pull request it had left are dealt with after the cancel, and the read-backs
  # are readings of state that changed.
  expect_contains "$gitcalls" "push origin --delete fugaro/$(run_of 1)" "a launch still going: the branch it had left is deleted"
  # By line, not by substring: the fixture's remote also holds the hundredth
  # run's branch, which starts with the first run's name.
  if grep -qx "fugaro/$(run_of 1)" "$dir/json/branches"; then
    fail "a launch still going: the branch is still on the remote"
  fi
  expect_contains "$ghcalls" "pr close 41 --repo acme/sandbox" "a launch still going: the pull request it had left is closed"
}

test_a_launch_the_cancel_does_not_end_is_a_finding() {
  # A cancel that answers while the run keeps going is the state the Restore row
  # may NOT read PASS over: the run can still push and open after the check is
  # over, and nothing else here will be looking.
  local dir=$work/stillgoing
  prepare "$dir" 99
  run_check "$dir" CHECK210_MAX_LAUNCHES=1 FAKE_MODE=rows-running-stuck
  expect_status "$status" 1 "a launch the cancel does not end"
  expect_contains "$out" "FACT restore: FAIL" "a launch the cancel does not end: the row"
  expect_contains "$out" "the runs list still reads them as going" "a launch the cancel does not end: what it found"
  expect_contains "$out" "$(run_of 1) (still going after 'fugaro cancel $(run_of 1) --now' answered 'running')" "a launch the cancel does not end: the run it names"
  expect_contains "$out" "stop it with 'fugaro cancel <run> --now'" "a launch the cancel does not end: the remedy names the command"
  expect_contains "$out" "Running this check again does not settle them" "a launch the cancel does not end: a rerun is refused as a remedy"
}

test_a_launch_the_runs_list_does_not_carry_is_a_finding() {
  # A launched run the runs list does not answer for is a teardown that cannot
  # read what the run left, so the row says so and names it rather than skipping
  # it: a run nobody can read is not a run that left nothing.
  local dir=$work/unlisted
  prepare "$dir" 99
  printf '%s\n' "$(run_of 1)" >"$dir/json/omit"
  run_check "$dir" CHECK210_MAX_LAUNCHES=1 FAKE_MODE=pair-skipped
  expect_status "$status" 1 "a launch the runs list does not carry"
  expect_contains "$out" "FACT restore: FAIL" "a launch the runs list does not carry: the row"
  expect_contains "$out" "the runs list does not carry these launches at all: $(run_of 1)" "a launch the runs list does not carry: the run it names"
}

test_a_launch_that_hangs_is_not_measured() {
  local dir=$work/hang
  prepare "$dir"
  run_check "$dir" FAKE_MODE=launch-hangs CHECK210_LAUNCH_TIMEOUT_SECONDS=2
  expect_status "$status" 2 "a launch that hangs"
  expect_contains "$out" "FACT pair: FAIL" "a launch that hangs: the row"
  expect_contains "$err" "could NOT measure pair" "a launch that hangs: the exit names the row"
}

test_a_kept_scratch_directory_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK210_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$err" "CHECK210_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
  [ -d "$scratch" ] || fail "a kept scratch directory: $scratch was removed anyway"
  rm -rf "$scratch" 2>/dev/null || true
}

test_an_unknown_argument_is_refused() {
  local dir=$work/arg
  prepare "$dir"
  run_check "$dir" -- --wat
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --wat" "an unknown argument: the message"
}

test_a_max_launches_that_is_not_a_number_is_refused() {
  local dir=$work/badmax
  prepare "$dir"
  run_check "$dir" -- --max-launches lots
  expect_status "$status" 2 "a --max-launches that is not a number"
  expect_contains "$err" "needs a whole number" "a --max-launches that is not a number: the message"
  assert_no_run "$dir" "a --max-launches that is not a number"
}

# THE MUTATION. The row that grades the challenger's own arm is the plan's first
# reading, and it is only worth anything if it really fails an arm that is not
# the shadow. The case is a COPY of the script with that one comparison replaced
# by `false`, run against the same fixture the case above fails: if the row were
# not load-bearing, the copy would read the same and this test would report
# nothing.
test_the_challengers_arm_row_is_load_bearing() {
  local dir=$work/mutate
  prepare "$dir"
  python3 - "$dir/json/records/$(challenger_of 1).json" <<'PY'
import json, sys
p = sys.argv[1]
d = json.load(open(p))
d["ab"]["arm"] = "champion"
json.dump(d, open(p, "w"))
PY
  mkdir -p "$dir/copy"
  sed 's/if \[ "\$arm" != challenger \]; then/if false; then/' "$script" >"$dir/copy/check210.sh"
  grep -q "if false; then" "$dir/copy/check210.sh" || {
    fail "the mutation: the copy was not mutated (the pattern no longer matches)"
    return 0
  }
  run_copy "$dir" "$dir/copy/check210.sh"
  expect_contains "$out" "FACT challenger: PASS" "the mutation: the broken copy passes the row"
  expect_absent "$out" "FACT challenger: FAIL" "the mutation: the broken copy does not fail the row"
  expect_status "$status" 0 "the mutation: the broken copy ends clean"
  # And the real script, on the same fixture, fails it — which is the assertion
  # the copy above shows would have been missed.
  run_check "$dir"
  expect_status "$status" 1 "the real script: the row fails"
  expect_contains "$out" "FACT challenger: FAIL" "the real script: the row"
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
t test_a_launch_loop_reaches_the_pair_after_its_first_samples
t test_the_teardown_closes_the_pull_request_and_deletes_the_branch
t test_a_missing_sandbox_is_refused
t test_a_path_that_is_not_a_checkout_stops_the_check
t test_xtrace_refused
t test_a_coding_agents_session_is_refused
t test_a_project_naming_belong_is_refused
t test_a_checkout_of_a_belong_project_is_refused
t test_yes_is_required
t test_a_build_older_than_the_release_is_refused
t test_a_build_without_the_ab_command_is_refused
t test_a_dev_build_is_decided_by_the_feature_probe
t test_a_layer_without_an_ab_block_is_a_finding
t test_a_layer_that_switched_ab_off_is_a_finding
t test_a_layer_rate_that_is_not_the_premise_is_a_finding
t test_a_layer_whose_rate_is_written_as_0_10_holds
t test_a_flow_style_ab_block_is_read
t test_a_layer_with_no_challenger_is_a_finding
t test_a_repository_that_opted_out_is_a_finding
t test_a_layer_that_cannot_be_read_is_not_measured
t test_a_layer_document_that_cannot_be_read_is_not_measured
t test_no_pair_in_the_bound_is_a_finding
t test_a_launch_that_fails_is_a_finding
t test_a_launch_that_names_no_run_is_a_finding
t test_a_challenger_that_did_not_start_is_a_finding
t test_an_arm_that_never_ends_is_not_measured
t test_a_batch_with_a_third_row_is_a_finding
t test_rows_keyed_to_another_run_are_a_finding
t test_a_champion_record_that_cannot_be_read_is_not_measured
t test_a_champion_record_of_another_run_is_a_finding
t test_a_champion_whose_arm_is_not_the_champion_is_a_finding
t test_a_challenger_document_that_cannot_be_read_is_not_measured
t test_a_challenger_that_published_is_a_finding
t test_a_challenger_from_another_source_is_a_finding
t test_a_challenger_record_with_a_branch_is_a_finding
t test_a_challenger_record_with_an_outcome_is_a_finding
t test_a_challenger_that_really_pushed_is_a_finding
t test_a_champion_that_left_no_branch_is_a_finding
t test_a_remote_that_cannot_be_read_is_not_measured
t test_a_champion_without_a_pull_request_is_a_finding
t test_a_challenger_that_opened_a_pull_request_is_a_finding
t test_a_missing_patch_object_is_a_finding
t test_a_report_without_the_pair_is_a_finding
t test_a_report_at_no_pairs_is_a_finding
t test_a_report_that_cannot_be_read_is_not_measured
t test_a_teardown_that_cannot_close_is_a_finding
t test_a_pull_request_that_cannot_be_closed_is_a_finding
t test_a_launch_still_going_is_cancelled_and_read_back
t test_a_launch_the_cancel_does_not_end_is_a_finding
t test_a_launch_the_runs_list_does_not_carry_is_a_finding
t test_a_launch_that_hangs_is_not_measured
t test_a_kept_scratch_directory_is_reported
t test_an_unknown_argument_is_refused
t test_a_max_launches_that_is_not_a_number_is_refused
t test_the_challengers_arm_row_is_load_bearing

if [ "$failures" -ne 0 ]; then
  echo "check210_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check210_test.sh: ok"
