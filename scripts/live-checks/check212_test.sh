#!/usr/bin/env bash
# Tests for scripts/live-checks/check212.sh, with fake `gcloud`, `fugaro` and
# `git` on PATH: NOTHING here reaches a cloud, a project, a price table or a run,
# no agent runs a live command, and the fakes record every call they are given —
# in which directory it was made — so a case can assert what the check did and
# did not do.
#
# The fakes are a small model of one machine: the fake `fugaro` answers the calls
# the check makes (version, the report's own `--help`, doctor, the layer, the
# model table, the launches, the runs listing and the cancel), the fake `gcloud`
# serves the two arms' records and the judge run's verdicts object, and the fake
# `git` answers the checkout's origin. The cases that matter are the ones where
# that model is bent: a build older than the release, a layer with no ab block or
# a rate of zero, a challenger that is a Claude ID or on the direct route or
# whose route refuses this repository, a sampler that pairs nothing, a pair whose
# shadow never started, arms that never end (which the check has to cancel — this
# check may leave nothing running it was following — and which leave the pair
# below unfit for the judge run it would otherwise launch), a record that is another
# run's, a shadow that published, a shadow that ran another model, a pair whose
# costs went past the cap or whose records carry none, a judge run that will not
# launch, a verdict the judge skipped or wrote for another model or decided
# against its own passes, a pair missing from the report, and a row that concluded below
# the minimum.
#
# Every command is bounded (see `bounded` below): a check that hangs fails here
# with the case's own name rather than hanging the suite that runs this file.
#
# Usage: bash scripts/live-checks/check212_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check212.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK212_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK212_TEST_HEAD:-900}")" ;;
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
CASE_TIMEOUT=${CHECK212_TEST_TIMEOUT:-90}

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

project=fugaro-chk212-test
repo=acme/sandbox
# The repository's slug, as `fugaro runs ls --json` carries it in a row's `run`
# ref: ONE path segment, where the launch names the repository as `owner/name`.
# The records' and the verdicts object's keys are built from this one, so a case
# that reads a key back is reading the check's own choice.
slug=acme-sandbox-9f8e7d6c
challenger=bigmodel/x-1
judge_model=claude-opus-4-1
coder=claude-opus-4-1
pair_id=pair-20261114130000-aaaa
champion_id=run-20261114130000-champ
challenger_id=run-20261114130001-shad
judge_id=run-20261114130002-judg
# The per-run cap every fixture record carries, and the spend the two arms stay
# under; the case that measures a pair with no cap writes its records with it at
# zero, which is what a project that never set one reads as.
cap_usd=5

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check212.sh's test only. It
# answers the account read the check makes and serves the two arms' records and
# the judge run's verdicts object, at the keys the check builds from the slug and
# the run id. Nothing here reaches a real project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    case "$mode" in
    belong-account) printf 'chk212-owner@belong.example\n' ;;
    *) printf 'chk212-owner@example.com\n' ;;
    esac
  fi
  ;;
storage)
  if [ "${2:-}" = "cat" ]; then
    url=${3:-}
    file=""
    case "$url" in
    */ab/verdicts.json) file="$FAKE_JSON_DIR/verdicts-$(basename "$(dirname "$(dirname "$url")")").json" ;;
    */result.json) file="$FAKE_JSON_DIR/record-$(basename "$(dirname "$url")").json" ;;
    esac
    if [ "$mode" = "record-missing" ] || [ -z "$file" ] || [ ! -f "$file" ]; then
      echo "ERROR: (gcloud.storage.cat) NotFound: No such object: $url" >&2
      exit 1
    fi
    cat "$file"
  fi
  ;;
*)
  echo "gcloud: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_git() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/git" <<'GIT_EOF'
#!/usr/bin/env bash
# A stand-in for `git`, for scripts/live-checks/check212.sh's test only: the
# check reads the sandbox checkout's origin to name the repository as
# owner/name, and that is the one call this answers.
set -euo pipefail
printf 'git %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/git.calls"
case "${1:-}" in
-C)
  shift 2
  ;;
esac
case "${1:-}" in
remote)
  case "${2:-}" in
  get-url)
    case "${FAKE_MODE:-default}" in
    no-remote)
      echo "error: No such remote 'origin'" >&2
      exit 2
      ;;
    *) printf 'git@github.com:%s.git\n' "$FAKE_REPO" ;;
    esac
    ;;
  esac
  ;;
*)
  echo "git: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
GIT_EOF
  chmod +x "$1/bin/git"
}

write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check212.sh's test only. The
# `--json` reads come from files the case wrote, `ab --help` prints the usage
# line the real command prints (the check's offline feature probe), and every
# call is logged with the directory it was made in.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}

case "${1:-}" in
version)
  case "$mode" in
  old-version) printf '0.23.0\n' ;;
  dev-build) printf 'dev-9f8e7d6\n' ;;
  *) printf '0.24.0\n' ;;
  esac
  ;;
ab)
  case "${2:-}" in
  --help)
    case "$mode" in
    no-feature) printf 'Usage:\n  fugaro ab [flags]\n' ;;
    *) printf 'Usage:\n  fugaro ab [flags]\n\nFlags:\n      --json   print machine-readable output\n      --repo string   only this repository (owner/name)\n      --since string   how far back to read: Nd, a Go duration, or 0 for no bound\n' ;;
    esac
    ;;
  *)
    cat "$FAKE_JSON_DIR/ab.json"
    ;;
  esac
  ;;
doctor)
  cat "$FAKE_JSON_DIR/doctor.json"
  ;;
config)
  case "${2:-}" in
  layer)
    cat "$FAKE_JSON_DIR/layer.json"
    ;;
  *)
    echo "fugaro: unexpected config subcommand ${2:-}" >&2
    exit 1
    ;;
  esac
  ;;
models)
  case "${2:-}" in
  ls)
    cat "$FAKE_JSON_DIR/models.json"
    ;;
  *)
    echo "fugaro: unexpected models subcommand ${2:-}" >&2
    exit 1
    ;;
  esac
  ;;
run)
  case " $* " in
  *" --recipe ab-judge "*)
    printf 'fugaro run %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/judge.calls"
    case "$mode" in
    judge-fails)
      printf 'fugaro: a judge run for this repository has not finished; one judge run per repository at a time\n' >&2
      exit 1
      ;;
    esac
    cat "$FAKE_JSON_DIR/judge.json"
    ;;
  *)
    printf 'fugaro run %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/launch.calls"
    case "$mode" in
    run-fails)
      printf 'fugaro: the launch was refused\n' >&2
      exit 1
      ;;
    launch-hangs)
      sleep 5
      ;;
    esac
    cat "$FAKE_JSON_DIR/launch.json"
    ;;
  esac
  ;;
runs)
  cat "$FAKE_JSON_DIR/rows.json"
  ;;
cancel)
  printf 'cancel %s\n' "$*" >>"$FAKE_LOG_DIR/cancel.calls"
  case "$mode" in
  cancel-fails)
    printf 'fugaro: the run could not be cancelled\n' >&2
    exit 1
    ;;
  cancel-hangs)
    sleep 5
    ;;
  esac
  cat "$FAKE_JSON_DIR/cancel.json"
  ;;
*)
  echo "fugaro: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# ------------------------------------------------------------------ documents
write_docs() { # dir
  local dir=$1 json=$1/json
  mkdir -p "$json"
  write_doctor "$dir" "project"
  write_layer "$dir" "good"
  write_models "$dir" "good"
  write_ab "$dir" "below-minimum"
  cat >"$json/launch.json" <<JSON
{"project":"$project","run":"$slug/$champion_id","repo":"$repo","run_id":"$champion_id","branch":"fugaro/20261114-130000-aaaa","execution":"exec-1","status":"launched","pair":{"id":"$pair_id","batch":"ab-$pair_id","challenger":"$challenger_id"}}
JSON
  cat >"$json/judge.json" <<JSON
{"project":"$project","run":"$slug/$judge_id","repo":"$repo","run_id":"$judge_id","branch":"fugaro/20261114-130002-bbbb","execution":"exec-2","status":"launched","recipe":{"name":"ab-judge","source":"catalog"}}
JSON
  write_rows "$dir" "done"
  write_cancel "$dir"
  write_records "$dir" "good"
  write_verdicts "$dir" "good"
}

write_doctor() { # write_doctor DIR STATE
  local name=$project
  [ "$2" = "belong-project" ] && name="${project}-belong"
  cat >"$1/json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[],"project":{"name":"chk212","gcp_project":"$name","region":"us-east5"}}
JSON
}

# write_layer DIR STATE: the layer document, whose `yaml` carries the ab: block.
# "absent" is a layer with no block at all, which is what a project that never
# published one reads as.
write_layer() { # write_layer DIR STATE
  local ab=""
  case "$2" in
  good)
    ab='ab:\n  rate: 0.05\n  max_pairs_per_day: 3\n  challengers:\n    - '"$challenger"'\n    - claude-sonnet-5\n  judge_model: '"$judge_model"'\n'
    ;;
  claude-first)
    ab='ab:\n  rate: 0.05\n  challengers:\n    - claude-opus-4-1\n    - '"$challenger"'\n  judge_model: '"$judge_model"'\n'
    ;;
  rate-zero)
    ab='ab:\n  rate: 0\n  challengers:\n    - '"$challenger"'\n  judge_model: '"$judge_model"'\n'
    ;;
  rate-too-high)
    ab='ab:\n  rate: 0.5\n  challengers:\n    - '"$challenger"'\n  judge_model: '"$judge_model"'\n'
    ;;
  no-challengers)
    ab='ab:\n  rate: 0.05\n  challengers: []\n  judge_model: '"$judge_model"'\n'
    ;;
  no-judge)
    ab='ab:\n  rate: 0.05\n  challengers:\n    - '"$challenger"'\n'
    ;;
  absent)
    ab=''
    ;;
  esac
  {
    printf '{"where":"project","generation":7,"sha256":"9f8e7d6c","yaml":"'
    if [ -n "$ab" ]; then
      printf 'governance:\\n  mode: standard\\n%s' "$ab"
    else
      printf 'governance:\\n  mode: standard\\n'
    fi
    printf '"}\n'
  } >"$1/json/layer.json"
}

# write_models DIR STATE: the model table, whose routes carry the data terms and
# whose models carry the price. The direct route is always there, as it is in the
# real document.
write_models() { # write_models DIR STATE
  local route=bigmodel kind=openai allow="[\"$repo\"]" open=false
  local model_route=$route refused="" chally=$challenger pin=$coder
  case "$2" in
  direct-route) model_route=direct ;;
  route-refuses-repo) allow='["other/repo"]' ;;
  route-open) allow='["*"]'; open=true ;;
  unpriced) refused="no price for this model" ;;
  coder-pin) pin=$challenger ;;
  no-coder) pin="" ;;
  esac
  cat >"$1/json/models.json" <<JSON
{"version":1,"project":"chk212","routes":[{"name":"direct","kind":"anthropic","host":"api.anthropic.com","models":["$coder"],"allow_data_to":["*"],"open_to_every_repo":true},{"name":"$route","kind":"$kind","host":"providers.example","models":["$chally"],"allow_data_to":$allow,"open_to_every_repo":$open}],"models":[{"id":"$chally","route":"$model_route","source":"catalog","input_per_m":1.5,"output_per_m":6,"allowed_to":["$repo"],"refused":"$refused"},{"id":"$coder","route":"direct","source":"embedded","input_per_m":3,"output_per_m":15,"allowed_to":["$repo"]}],"repositories":[{"name":"$repo","state":"active","coder":"$pin"}]}
JSON
}

# write_rows DIR STATE: the runs listing the check follows its arms and the judge
# run with.
write_rows() { # write_rows DIR STATE
  local champ_term=true chal_term=true judge_term=true judge_recipe=ab-judge
  case "$2" in
  arms-running) champ_term=false; chal_term=false ;;
  judge-running) judge_term=false ;;
  all-running) champ_term=false; chal_term=false; judge_term=false ;;
  esac
  cat >"$1/json/rows.json" <<JSON
{"project":"$project","runs":[{"run":"$slug/$champion_id","repo":"$repo","run_id":"$champion_id","status":"succeeded","stage":"finalize","terminal":$champ_term,"settled":$champ_term,"batch":"ab-$pair_id","branch":"fugaro/20261114-130000-aaaa"},{"run":"$slug/$challenger_id","repo":"$repo","run_id":"$challenger_id","status":"succeeded","stage":"finalize","terminal":$chal_term,"settled":$chal_term,"batch":"ab-$pair_id","branch":"fugaro/20261114-130001-aaab"},{"run":"$slug/$judge_id","repo":"$repo","run_id":"$judge_id","status":"succeeded","stage":"finalize","terminal":$judge_term,"settled":$judge_term,"recipe":"$judge_recipe","branch":"fugaro/20261114-130002-bbbb"}],"totals":{"runs":3},"warnings":[]}
JSON
}

write_cancel() { # dir
  cat >"$1/json/cancel.json" <<JSON
{"project":"$project","run":"$slug/$champion_id","status":"cancelled","marker":true,"hard":true}
JSON
}

# write_record DIR RUN_ID ARM PAIR_ID MODEL PUBLISH USD STARTED FINISHED [SPLIT_ID]:
# one arm's record. MODEL is the ab block's challenger ("" on the champion), and
# FINISHED "" writes no finished_at at all, which is the state the wall-times row
# exists to catch.
write_record() { # write_record DIR RUN_ID ARM PAIR_ID MODEL PUBLISH USD STARTED FINISHED [SPLIT_ID]
  local dir=$1 id=$2 arm=$3 pair=$4 model=$5 publish=$6 usd=$7 started=$8 finished=$9 split=${10:-}
  [ -n "$split" ] || { if [ "$arm" = champion ]; then split=$coder; else split=$model; fi; }
  local ab finished_json
  ab='{"pair_id":"'"$pair"'","arm":"'"$arm"'"'
  if [ "$arm" = challenger ]; then
    ab="$ab"',"publish":"'"$publish"'","challenger":"'"$model"'","challenger_source":"owner"'
  fi
  ab="$ab"'}'
  finished_json=""
  if [ -n "$finished" ]; then finished_json=',"finished_at":"'"$finished"'"'; fi
  local split_json="{}"
  [ -n "$split" ] && split_json='{"'"$split"'":'"$usd"'}'
  cat >"$dir/json/record-$id.json" <<JSON
{"version":1,"run_id":"$id","repo":"$repo","workflow":"agent","status":"succeeded","stage":"finalize","outcome":"ready","started_at":"$started"$finished_json,"ab":$ab,"pr":{"number":2000,"url":"https://example.invalid/pull/2000"},"policy":{"effective":{"per_run_usd":$cap_usd,"mode":"enforce"}},"stages":[{"name":"implement","model":"$split","model_usd":$usd}],"cost":{"model_usd":$usd,"compute_usd":0,"total_usd":$usd,"model_by":$split_json}}
JSON
}

write_records() { # write_records DIR STATE
  local champ_usd=0.42 chal_usd=0.51 champ_start=2026-11-14T13:00:00Z champ_end=2026-11-14T13:10:00Z
  local chal_start=2026-11-14T13:00:00Z chal_end=2026-11-14T13:11:00Z
  local champ_pair=$pair_id chal_pair=$pair_id chal_publish=none chal_model=$challenger
  local champ_arm=champion chal_arm=challenger chal_split="" champ_split=""
  local champ_finish=$champ_end
  case "$2" in
  no-wall) champ_finish="" ;;
  another-pair) chal_pair=pair-other ;;
  swapped-arms) champ_arm=challenger; chal_arm=champion ;;
  publishes) chal_publish="" ;;
  another-model) chal_model=claude-sonnet-5 ;;
  no-challenger-spend) chal_split=none ;;
  over-cap) chal_usd=9.99 ;;
  slow-shadow) chal_end=2026-11-14T13:40:00Z ;;
  esac
  write_record "$1" "$champion_id" "$champ_arm" "$champ_pair" "" "" "$champ_usd" "$champ_start" "$champ_finish" "$champ_split"
  write_record "$1" "$challenger_id" "$chal_arm" "$chal_pair" "$chal_model" "$chal_publish" "$chal_usd" "$chal_start" "$chal_end" "$chal_split"
}

# pass_arm is the judge's own mapping of one pass's word to an arm
# (ee/ab/judge.go's labelArm): the same word in the two passes means the two
# different arms, because the second pass swapped the order, and a word outside
# the vocabulary is a tie. Every fixture below is written through it, so a pass
# in the object is one the judge really produces.
pass_arm() { # pass_arm SWAP LABEL
  case "$2" in
  a)
    if [ "$1" = true ]; then printf 'challenger'; else printf 'champion'; fi
    ;;
  b)
    if [ "$1" = true ]; then printf 'champion'; else printf 'challenger'; fi
    ;;
  *) printf 'tie' ;;
  esac
}

# pass_json is one pass as the judge writes it: the word the model answered, the
# arm that word meant, and the reason beside it — a tie has no winner to give a
# reason for, which is why the field is absent there.
pass_json() { # pass_json SWAP LABEL ARM
  local reason=',"reason":"clearer"'
  [ "$3" = tie ] && reason=""
  printf '{"swap":%s,"label":"%s","arm":"%s"%s}' "$1" "$2" "$3" "$reason"
}

# write_verdicts DIR STATE: the judge run's own object. Its `judge_model` is the
# layer's, and the pair entry is THIS pair unless a case says otherwise. The
# labels are what the model answered on each labelling; the arms beside them are
# pass_arm's reading, and the winner is what decide makes of the two arms — a
# case that wants a verdict no judge writes bends the winner alone.
write_verdicts() { # write_verdicts DIR STATE
  local judge=$judge_model entry_pair=$pair_id winner=challenger skipped=""
  local label1=b label2=a two=yes
  case "$2" in
  another-judge) judge=claude-haiku-4-5 ;;
  skipped) skipped=identified ;;
  another-pair) entry_pair=pair-other ;;
  no-pass) two=no ;;
  # The two passes answered differently, so decide reads the pair as a tie: a
  # judge that disagrees with itself is not a verdict the check may fail.
  disagree) winner=tie; label2=b ;;
  # A word outside the vocabulary carries the tie arm decide reads as well.
  no-vocabulary) winner=tie; label1=""; label2="" ;;
  # Both passes named the champion, so the winner decide makes of them is the
  # champion: a tie written beside them is an object no judge produces.
  decided-tie) winner=tie; label1=a; label2=b ;;
  tie) winner=tie; label1=tie; label2=tie ;;
  esac
  local passes
  passes=$(pass_json false "$label1" "$(pass_arm false "$label1")")
  [ "$two" = yes ] && passes="$passes,$(pass_json true "$label2" "$(pass_arm true "$label2")")"
  local reason_json=',"reason":"clearer"'
  [ "$winner" = tie ] && reason_json=""
  local skipped_json=""
  [ -n "$skipped" ] && skipped_json=',"skipped":"'"$skipped"'"'
  cat >"$1/json/verdicts-$judge_id.json" <<JSON
{"version":1,"judge_model":"$judge","pairs":[{"pair_id":"$entry_pair","winner":"$winner"$reason_json$skipped_json,"passes":[$passes]}]}
JSON
}

# write_ab DIR STATE: the report. "no-row" is a report that lists another pair;
# "concludes" is a row that draws a conclusion from three decisive pairs, under
# the minimum of twenty; "inconsistent" is one whose own wins and losses do not
# add up to the decisive count it carries.
write_ab() { # write_ab DIR STATE
  local pairs=1 wins=1 losses=0 ties=0 decisive=1 decision=no-conclusion rule=below-minimum listed=$challenger
  case "$2" in
  no-row) listed=bigmodel/x-9 ;;
  concludes) decisive=3; wins=3; decision=adopt; rule=significant ;;
  inconsistent) decisive=3 ;;
  reached-minimum) pairs=25; wins=12; losses=8; decisive=20; decision=adopt; rule=significant ;;
  esac
  cat >"$1/json/ab.json" <<JSON
{"project":"$project","source":"run-records","since":"","until":"","rows":[{"mode":"pro","tier":"untiered","champion":"$coder","challenger":"$listed","pairs":$pairs,"wins":$wins,"losses":$losses,"ties":$ties,"champion_halts":0,"challenger_halts":0,"champion_follow_ups":0,"judge_families":["anthropic"],"judge_same_family":false,"unjudged":0,"truncated":0,"recommendation":{"decision":"$decision","rule":"$rule","decisive":$decisive}}],"warnings":[]}
JSON
}

# A repository checkout: the check refuses a path that is not one, and every
# command is run from the checkout it belongs to.
write_checkout() { # dir
  mkdir -p "$1"
  printf 'project: chk212\n' >"$1/fugaro.yaml"
}

prepare() { # dir
  mkdir -p "$1/home" "$1/state" "$1/logs" "$1/tmp"
  write_fake_gcloud "$1"
  write_fake_git "$1"
  write_fake_fugaro "$1"
  write_docs "$1"
  write_checkout "$1/sandbox"
  write_checkout "$1/other"
}

run_check() { # run_check DIR ENV... [-- ARGS...]
  local dir=$1
  shift
  run_one "$dir" "$script" "$@"
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
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/cancel.calls"
  : >"$dir/logs/git.calls"
  : >"$dir/logs/launch.calls"
  : >"$dir/logs/judge.calls"
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
    FAKE_JSON_DIR="$dir/json" FAKE_REPO="$repo" \
    CHECK212_SANDBOX="$dir/sandbox" CHECK212_YES=1 \
    CHECK212_RUN_TIMEOUT_SECONDS=4 CHECK212_JUDGE_TIMEOUT_SECONDS=4 \
    CHECK212_POLL_SECONDS=1 CHECK212_CLI_TIMEOUT_SECONDS=20 \
    CHECK212_LAUNCH_TIMEOUT_SECONDS=20 CHECK212_CANCEL_TIMEOUT_SECONDS=20 \
    CHECK212_DOCTOR_TIMEOUT_SECONDS=20 CHECK212_AB_TIMEOUT_SECONDS=20 \
    CHECK212_MAX_LAUNCHES=2 \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$target" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
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

assert_no_launch() { # dir what
  expect_absent "$(cat "$1/logs/launch.calls")" "run --repo" "$2: no run is launched"
}

assert_no_cancel() { # dir what
  expect_absent "$(cat "$1/logs/fugaro.calls")" "cancel " "$2: nothing is cancelled"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check212.sh --sandbox <a sandbox repository checkout> --yes" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, that the check spends the owner's money, who runs it, and
  # what it does to a run that will not end.
  expect_contains "$out" "BILLABLE, and the reason \`--yes\` is required" "--help: the cost is stated"
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  expect_contains "$out" "cancelled from that same trap if they have not ended" "--help: what it does to a run that will not end"
  assert_no_launch "$dir" "--help"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK212_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 212 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version row"
  expect_contains "$out" "FACT agent-session: PASS" "dry run: the session guard"
  expect_contains "$out" "FACT belong: PASS" "dry run: the belong guard"
  expect_contains "$out" "FACT bound: PASS" "dry run: the cost is stated"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore row"
  expect_contains "$out" "CHECK 212 RESULT END (exit 0)" "dry run: the end line"
  # A row the plan did not measure reads INFO, never PASS: a plan is not a
  # reading, and a block that said PASS would be one nobody took.
  expect_contains "$out" "FACT layer: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$out" "FACT ab-listed: INFO — planned" "dry run: the report row is planned too"
  expect_contains "$err" "BILLABLE" "dry run: the plan says the runs cost money"
  expect_contains "$err" "fugaro cancel <run> --now" "dry run: the plan says what it does to a run that will not end"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "dry run: the installation is not read"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage" "dry run: the bucket is not read"
  assert_no_launch "$dir" "dry run"
  assert_no_cancel "$dir" "dry run"
  assert_scratch_removed "$dir" "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "FACT sandbox: PASS" "happy path: the checkout and its project"
  expect_contains "$out" "belongs to ${project}" "happy path: the project it read"
  expect_contains "$out" "FACT layer: PASS" "happy path: the layer"
  expect_contains "$out" "samples 0.05 of eligible runs" "happy path: the rate it read"
  expect_contains "$out" "FACT challenger: PASS" "happy path: the challenger"
  expect_contains "$out" "route bigmodel" "happy path: the provider route"
  expect_contains "$out" "FACT pair-launched: PASS" "happy path: the pair"
  expect_contains "$out" "was paired with the shadow ${challenger_id}" "happy path: the two arms it follows"
  expect_contains "$out" "FACT arms-terminal: PASS" "happy path: both arms ended"
  expect_contains "$out" "FACT records: PASS" "happy path: the two records"
  expect_contains "$out" "FACT wall-times: PASS" "happy path: U4's two wall times"
  expect_contains "$out" "the champion run-20261114130000-champ ran 600.0s" "happy path: the champion's wall time"
  expect_contains "$out" "the shadow run-20261114130001-shad ran 660.0s" "happy path: the shadow's wall time"
  expect_contains "$out" "FACT latency-profile: PASS" "happy path: the two profiles"
  expect_contains "$out" "FACT costs: PASS" "happy path: both costs inside the cap"
  expect_contains "$out" "FACT challenger-ran: PASS" "happy path: the provider model's own calls"
  expect_contains "$out" "FACT judge-run: PASS" "happy path: the judge run"
  expect_contains "$out" "FACT verdict: PASS" "happy path: the verdict object"
  expect_contains "$out" "the winner is 'challenger'" "happy path: the winner"
  expect_contains "$out" "FACT ab-listed: PASS" "happy path: the report"
  expect_contains "$out" "below the minimum" "happy path: what the row says"
  expect_contains "$out" "FACT restore: PASS" "happy path: the restore"
  expect_contains "$out" "CHECK 212 RESULT END (exit 0)" "happy path: the end line"
  # Every command ran in the checkout it belongs to, the records and the verdicts
  # object were read at the keys the slug builds, the judge run was launched by
  # its recipe, and nothing was cancelled, because both arms ended on their own.
  local calls
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_contains "$calls" "doctor --json (in $dir/sandbox)" "happy path: doctor runs in the sandbox checkout"
  expect_contains "$calls" "config layer --json (in $dir/sandbox)" "happy path: the layer is read in the sandbox checkout"
  expect_contains "$calls" "models ls --json (in $dir/sandbox)" "happy path: the model table is read in the sandbox checkout"
  expect_contains "$calls" "run --repo $dir/sandbox --json" "happy path: the pair is launched from the sandbox checkout"
  expect_contains "$(cat "$dir/logs/judge.calls")" "--recipe ab-judge" "happy path: the judge run is launched by its recipe"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "gs://fugaro-runs-${project}/runs/${slug}/${champion_id}/result.json" "happy path: the champion's record key"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "gs://fugaro-runs-${project}/runs/${slug}/${challenger_id}/result.json" "happy path: the shadow's record key"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "gs://fugaro-runs-${project}/runs/${slug}/${judge_id}/ab/verdicts.json" "happy path: the verdicts object's key"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "runs/${repo}/" "happy path: the key is the one-segment slug, not the owner/name the launch names"
  assert_no_cancel "$dir" "happy path"
  assert_scratch_removed "$dir" "happy path"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/nosandbox
  prepare "$dir"
  run_check "$dir" CHECK212_SANDBOX= -- --dry-run
  expect_status "$status" 2 "no --sandbox"
  expect_contains "$err" "needs --sandbox" "no --sandbox: the refusal"
  expect_contains "$err" "Usage: bash scripts/live-checks/check212.sh" "no --sandbox: what to type"
}

test_a_path_that_is_not_a_checkout_stops_the_check() {
  local dir=$work/notcheckout
  prepare "$dir"
  run_check "$dir" CHECK212_SANDBOX="$dir/state"
  expect_status "$status" 2 "not a checkout"
  expect_contains "$err" "carries no fugaro.yaml" "not a checkout: the refusal"
  assert_no_launch "$dir" "not a checkout"
}

test_a_checkout_with_no_origin_is_a_finding() {
  local dir=$work/noorigin
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-remote
  expect_status "$status" 1 "no origin"
  expect_contains "$out" "FACT sandbox: FAIL" "no origin: the row"
  expect_contains "$out" "names no repository" "no origin: what is missing"
  assert_no_launch "$dir" "no origin"
}

# The billable gate: without --yes the check stops before it spends anything,
# and the block still says what it would have done.
test_no_yes_is_refused() {
  local dir=$work/noyes
  prepare "$dir"
  run_check "$dir" CHECK212_YES=
  expect_status "$status" 2 "no --yes"
  expect_contains "$err" "it needs your word" "no --yes: the refusal"
  expect_contains "$err" "bash scripts/live-checks/check212.sh --sandbox $dir/sandbox --yes" "no --yes: what to type"
  expect_contains "$out" "FACT bound: PASS" "no --yes: the cost is still stated"
  expect_contains "$out" "BILLABLE" "no --yes: the row says why"
  assert_no_launch "$dir" "no --yes"
  assert_scratch_removed "$dir" "no --yes"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- --xtrace
  expect_status "$status" 1 "xtrace"
  expect_contains "$err" "this shell is tracing" "xtrace: the refusal"
  assert_no_launch "$dir" "xtrace"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "agent session"
  expect_contains "$err" "CLAUDECODE is set" "agent session: the refusal"
  assert_no_launch "$dir" "agent session"
}

test_an_account_naming_belong_is_refused() {
  local dir=$work/belongacct
  prepare "$dir"
  run_check "$dir" FAKE_MODE=belong-account
  expect_status "$status" 1 "belong account"
  expect_contains "$err" "names belong" "belong account: the refusal"
  assert_no_launch "$dir" "belong account"
}

test_a_project_naming_belong_is_refused() {
  local dir=$work/belongproj
  prepare "$dir"
  write_doctor "$dir" "belong-project"
  run_check "$dir"
  expect_status "$status" 1 "belong project"
  expect_contains "$err" "belongs to ${project}-belong" "belong project: the refusal"
  assert_no_launch "$dir" "belong project"
}

test_a_build_older_than_the_release_is_refused() {
  local dir=$work/old
  prepare "$dir"
  run_check "$dir" FAKE_MODE=old-version
  expect_status "$status" 1 "old build"
  expect_contains "$out" "FACT release: FAIL" "old build: the release row"
  expect_contains "$out" "predates release 0.24.0" "old build: the release it needs"
  assert_no_launch "$dir" "old build"
}

test_a_build_without_the_report_is_refused() {
  local dir=$work/nofeature
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-feature
  expect_status "$status" 1 "no report"
  expect_contains "$out" "FACT release: FAIL" "no report: the release row"
  expect_contains "$out" "has no 'ab' report" "no report: what is missing"
  assert_no_launch "$dir" "no report"
}

test_a_dev_build_is_decided_by_the_feature_probe() {
  local dir=$work/dev
  prepare "$dir"
  run_check "$dir" FAKE_MODE=dev-build
  expect_status "$status" 0 "dev build"
  expect_contains "$out" "not a release number" "dev build: the reading"
  expect_contains "$out" "FACT layer: PASS" "dev build: the check went on"
  assert_no_cancel "$dir" "dev build"
}

test_a_layer_without_the_ab_block_is_a_finding() {
  local dir=$work/noblock
  prepare "$dir"
  write_layer "$dir" "absent"
  run_check "$dir"
  expect_status "$status" 1 "no ab block"
  expect_contains "$out" "FACT layer: FAIL" "no ab block: the row"
  expect_contains "$out" "carries no ab: block" "no ab block: what is missing"
  assert_no_launch "$dir" "no ab block"
}

test_a_layer_with_rate_zero_is_a_finding() {
  local dir=$work/ratezero
  prepare "$dir"
  write_layer "$dir" "rate-zero"
  run_check "$dir"
  expect_status "$status" 1 "rate zero"
  expect_contains "$out" "the project's opt-out" "rate zero: what it means"
  assert_no_launch "$dir" "rate zero"
}

test_a_rate_above_the_ceiling_is_a_finding() {
  local dir=$work/ratehigh
  prepare "$dir"
  write_layer "$dir" "rate-too-high"
  run_check "$dir"
  expect_status "$status" 1 "rate too high"
  expect_contains "$out" "above the 0.10 ceiling" "rate too high: the ceiling"
  assert_no_launch "$dir" "rate too high"
}

test_a_layer_with_no_challengers_is_a_finding() {
  local dir=$work/nochallengers
  prepare "$dir"
  write_layer "$dir" "no-challengers"
  run_check "$dir"
  expect_status "$status" 1 "no challengers"
  expect_contains "$out" "names no ab.challengers" "no challengers: what is missing"
  assert_no_launch "$dir" "no challengers"
}

test_a_layer_with_no_judge_model_is_a_finding() {
  local dir=$work/nojudge
  prepare "$dir"
  write_layer "$dir" "no-judge"
  run_check "$dir"
  expect_status "$status" 1 "no judge model"
  expect_contains "$out" "names no ab.judge_model" "no judge model: what is missing"
  assert_no_launch "$dir" "no judge model"
}

test_a_challenger_that_is_a_claude_id_is_a_finding() {
  local dir=$work/claudefirst
  prepare "$dir"
  write_layer "$dir" "claude-first"
  run_check "$dir"
  expect_status "$status" 1 "Claude challenger"
  expect_contains "$out" "FACT challenger: FAIL" "Claude challenger: the row"
  expect_contains "$out" "is a Claude ID" "Claude challenger: what it is"
  assert_no_launch "$dir" "Claude challenger"
}

test_a_challenger_on_the_direct_route_is_a_finding() {
  local dir=$work/directroute
  prepare "$dir"
  write_models "$dir" "direct-route"
  run_check "$dir"
  expect_status "$status" 1 "direct route"
  expect_contains "$out" "served by the direct Anthropic route" "direct route: the route it read"
  assert_no_launch "$dir" "direct route"
}

test_a_challenger_whose_route_refuses_the_repository_is_a_finding() {
  local dir=$work/routerefuses
  prepare "$dir"
  write_models "$dir" "route-refuses-repo"
  run_check "$dir"
  expect_status "$status" 1 "route refuses the repo"
  expect_contains "$out" "does not admit this repository" "route refuses the repo: what is missing"
  assert_no_launch "$dir" "route refuses the repo"
}

test_a_challenger_that_is_the_coder_pin_is_a_finding() {
  local dir=$work/coderpin
  prepare "$dir"
  write_models "$dir" "coder-pin"
  run_check "$dir"
  expect_status "$status" 1 "coder pin"
  expect_contains "$out" "is also this repository's coder pin" "coder pin: the row"
  assert_no_launch "$dir" "coder pin"
}

test_a_challenger_the_layer_does_not_list_is_a_finding() {
  local dir=$work/notlisted
  prepare "$dir"
  run_check "$dir" CHECK212_CHALLENGER=bigmodel/x-9
  expect_status "$status" 1 "challenger not listed"
  expect_contains "$out" "--challenger names bigmodel/x-9" "challenger not listed: the row"
  expect_contains "$out" "ab.challengers is [bigmodel/x-1 claude-sonnet-5]" "challenger not listed: the list it read"
  assert_no_launch "$dir" "challenger not listed"
}

test_a_sampler_that_pairs_nothing_is_a_finding() {
  local dir=$work/nopair
  prepare "$dir"
  cat >"$dir/json/launch.json" <<JSON
{"project":"$project","run":"$slug/$champion_id","repo":"$repo","run_id":"$champion_id","branch":"fugaro/20261114-130000-aaaa","execution":"exec-1","status":"launched"}
JSON
  run_check "$dir"
  expect_status "$status" 1 "no pair"
  expect_contains "$out" "FACT pair-launched: FAIL" "no pair: the row"
  expect_contains "$out" "paired none of the 2 sandbox runs" "no pair: the cap it reached"
  expect_contains "$out" "the chance of 2 rolls all missing" "no pair: what the odds mean"
  # The launches that paired nothing are ordinary champion runs: the check does
  # not chase them and it does not cancel them.
  expect_contains "$(cat "$dir/logs/launch.calls")" "run --repo $dir/sandbox --json" "no pair: a run was launched"
  assert_no_cancel "$dir" "no pair"
}

test_a_launch_that_names_a_reason_is_a_finding() {
  local dir=$work/reason
  prepare "$dir"
  cat >"$dir/json/launch.json" <<JSON
{"project":"$project","run":"$slug/$champion_id","repo":"$repo","run_id":"$champion_id","branch":"fugaro/20261114-130000-aaaa","execution":"exec-1","status":"launched","pair":{"reason":"old-image"}}
JSON
  run_check "$dir"
  expect_status "$status" 1 "sampler reason"
  expect_contains "$out" "sampled no pair and the launcher said why: 'old-image'" "sampler reason: the launcher's own word"
  assert_no_cancel "$dir" "sampler reason"
}

test_a_pair_whose_shadow_never_started_is_a_finding() {
  local dir=$work/halfpair
  prepare "$dir"
  cat >"$dir/json/launch.json" <<JSON
{"project":"$project","run":"$slug/$champion_id","repo":"$repo","run_id":"$champion_id","branch":"fugaro/20261114-130000-aaaa","execution":"exec-1","status":"launched","pair":{"id":"$pair_id","batch":"ab-$pair_id"}}
JSON
  run_check "$dir"
  expect_status "$status" 1 "half pair"
  expect_contains "$out" "carries the pair id ${pair_id} and no challenger" "half pair: the row"
  assert_no_cancel "$dir" "half pair"
}

test_a_launch_that_fails_is_a_finding() {
  local dir=$work/launchfails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=run-fails
  expect_status "$status" 1 "launch fails"
  expect_contains "$out" "fugaro run did not answer for attempt 1" "launch fails: the row"
}

test_arms_that_never_end_are_cancelled() {
  local dir=$work/neverends
  prepare "$dir"
  write_rows "$dir" "arms-running"
  run_check "$dir"
  expect_status "$status" 2 "arms never end"
  expect_contains "$out" "FACT arms-terminal: FAIL" "arms never end: the row"
  expect_contains "$out" "FACT cancelled: PASS" "arms never end: the cancel"
  expect_contains "$out" "nothing this check was still following outlives it" "arms never end: what the cancel means"
  # A pair whose arms never ended has nothing for a judge to read, so no billable
  # judge run is launched for it — the report and the records below it are not
  # reached either.
  expect_absent "$(cat "$dir/logs/judge.calls")" "ab-judge" "arms never end: no judge run is launched"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "ab --repo" "arms never end: the report is not read"
  expect_contains "$out" "FACT judge-run: FAIL — there is no complete pair" "arms never end: the judge row says why"
  # Both arms were cancelled, once each: the check may leave nothing running it
  # was following.
  local cancels
  cancels=$(cat "$dir/logs/cancel.calls")
  expect_contains "$cancels" "$champion_id" "arms never end: the champion is cancelled"
  expect_contains "$cancels" "$challenger_id" "arms never end: the shadow is cancelled"
  expect_contains "$cancels" "--now" "arms never end: the cancel is the hard one"
  assert_scratch_removed "$dir" "arms never end"
}

# The pair's arms and the judge run of the same rows: neither ends here, and the
# one judge run the check would otherwise launch for a pair nothing can judge
# must not be launched — nor cancelled later, because it was never started. The
# cancels name the two arms and nothing else.
test_arms_and_the_judge_run_that_never_end_launch_no_judge() {
  local dir=$work/allneverends
  prepare "$dir"
  write_rows "$dir" "all-running"
  run_check "$dir"
  expect_status "$status" 2 "arms and judge never end"
  expect_contains "$out" "FACT arms-terminal: FAIL" "arms and judge never end: the row"
  expect_contains "$out" "FACT cancelled: PASS" "arms and judge never end: the cancel"
  expect_absent "$(cat "$dir/logs/judge.calls")" "ab-judge" "arms and judge never end: no judge run is launched"
  local cancels
  cancels=$(cat "$dir/logs/cancel.calls")
  expect_contains "$cancels" "$champion_id" "arms and judge never end: the champion is cancelled"
  expect_contains "$cancels" "$challenger_id" "arms and judge never end: the shadow is cancelled"
  expect_absent "$cancels" "$judge_id" "arms and judge never end: the judge run was never started, so it is not cancelled"
  assert_scratch_removed "$dir" "arms and judge never end"
}

test_a_cancel_that_does_not_answer_is_a_finding() {
  local dir=$work/cancelfails
  prepare "$dir"
  write_rows "$dir" "arms-running"
  run_check "$dir" FAKE_MODE=cancel-fails
  expect_status "$status" 1 "cancel fails"
  expect_contains "$out" "FACT cancelled: FAIL" "cancel fails: the row"
  expect_contains "$out" "cancel it yourself" "cancel fails: what the owner must do"
}

test_a_record_that_cannot_be_read_is_a_finding() {
  local dir=$work/norecord
  prepare "$dir"
  run_check "$dir" FAKE_MODE=record-missing
  expect_status "$status" 1 "no record"
  expect_contains "$out" "FACT records: FAIL" "no record: the row"
  expect_contains "$out" "could not be read" "no record: what failed"
}

# The two records have to be one pair: a reader joins the arms by the pair id,
# and two records that disagree are a pair nobody can read.
test_records_of_two_pairs_are_a_finding() {
  local dir=$work/twopairs
  prepare "$dir"
  write_records "$dir" "another-pair"
  run_check "$dir"
  expect_status "$status" 1 "two pairs"
  expect_contains "$out" "the two records carry the pair ids '${pair_id}' and 'pair-other'" "two pairs: the row"
}

test_a_shadow_that_publishes_is_a_finding() {
  local dir=$work/publishes
  prepare "$dir"
  write_records "$dir" "publishes"
  run_check "$dir"
  expect_status "$status" 1 "shadow publishes"
  expect_contains "$out" "carries publish 'none' where a challenger publishes nothing" "shadow publishes: the row"
  expect_contains "$out" "a shadow that opened a pull request" "shadow publishes: what it means"
}

test_a_shadow_that_ran_another_model_is_a_finding() {
  local dir=$work/anothermodel
  prepare "$dir"
  write_records "$dir" "another-model"
  run_check "$dir"
  expect_status "$status" 1 "another model"
  expect_contains "$out" "names the challenger model 'claude-sonnet-5'" "another model: the row"
  expect_contains "$out" "the layer's list led with ${challenger}" "another model: what the layer promised"
}

test_a_record_without_a_wall_time_is_a_finding() {
  local dir=$work/nowall
  prepare "$dir"
  write_records "$dir" "no-wall"
  run_check "$dir"
  expect_status "$status" 1 "no wall time"
  expect_contains "$out" "FACT wall-times: FAIL" "no wall time: the row"
  expect_contains "$out" "carries no wall time" "no wall time: what is missing"
}

test_a_shadow_far_slower_than_the_champion_is_a_finding() {
  local dir=$work/slow
  prepare "$dir"
  write_records "$dir" "slow-shadow"
  run_check "$dir"
  expect_status "$status" 1 "slow shadow"
  expect_contains "$out" "FACT latency-profile: FAIL" "slow shadow: the row"
  expect_contains "$out" "differ by a factor of 4.00" "slow shadow: the factor it read"
  expect_contains "$out" "CHECK212_LATENCY_MAX_RATIO" "slow shadow: the knob that bounds it"
}

test_a_cost_over_the_cap_is_a_finding() {
  local dir=$work/overcap
  prepare "$dir"
  write_records "$dir" "over-cap"
  run_check "$dir"
  expect_status "$status" 1 "over the cap"
  expect_contains "$out" "FACT costs: FAIL" "over the cap: the row"
  expect_contains "$out" "spent past its cap" "over the cap: what it means"
}

test_a_pair_with_no_cap_is_a_finding() {
  local dir=$work/nocap
  prepare "$dir"
  cap_usd=0
  write_records "$dir" "good"
  cap_usd=5
  run_check "$dir"
  expect_status "$status" 1 "no cap"
  expect_contains "$out" "carry no per-run cap" "no cap: the row"
  expect_contains "$out" "budget.mode: enforce" "no cap: the remedy"
}

test_a_shadow_with_no_challenger_spend_is_a_finding() {
  local dir=$work/nospend
  prepare "$dir"
  write_records "$dir" "no-challenger-spend"
  run_check "$dir"
  expect_status "$status" 1 "no challenger spend"
  expect_contains "$out" "FACT challenger-ran: FAIL" "no challenger spend: the row"
  expect_contains "$out" "carries no model spend for ${challenger}" "no challenger spend: what is missing"
}

test_a_judge_that_will_not_launch_is_unmeasured() {
  local dir=$work/judgefails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=judge-fails
  expect_status "$status" 2 "judge launch refused"
  expect_contains "$out" "FACT judge-run: FAIL" "judge launch refused: the row"
  expect_contains "$out" "one judge run at a time" "judge launch refused: the command's own sentence"
  assert_no_cancel "$dir" "judge launch refused"
}

test_a_judge_run_that_never_ends_is_cancelled() {
  local dir=$work/judgeruns
  prepare "$dir"
  write_rows "$dir" "judge-running"
  run_check "$dir"
  expect_status "$status" 2 "judge never ends"
  expect_contains "$out" "FACT judge-run: FAIL" "judge never ends: the row"
  expect_contains "$out" "FACT cancelled: PASS" "judge never ends: the cancel"
  expect_contains "$(cat "$dir/logs/cancel.calls")" "$judge_id" "judge never ends: the judge run is the one cancelled"
  expect_absent "$(cat "$dir/logs/cancel.calls")" "$champion_id" "judge never ends: the arms had ended and are not cancelled"
}

test_a_verdict_that_skipped_the_pair_is_a_finding() {
  local dir=$work/skipped
  prepare "$dir"
  write_verdicts "$dir" "skipped"
  run_check "$dir"
  expect_status "$status" 1 "skipped pair"
  expect_contains "$out" "FACT verdict: FAIL" "skipped pair: the row"
  expect_contains "$out" "was SKIPPED by the judge run: 'identified'" "skipped pair: the skip word"
}

test_a_verdict_from_another_judge_model_is_a_finding() {
  local dir=$work/otherjudge
  prepare "$dir"
  write_verdicts "$dir" "another-judge"
  run_check "$dir"
  expect_status "$status" 1 "another judge"
  expect_contains "$out" "written by the judge model 'claude-haiku-4-5'" "another judge: the row"
}

test_a_verdict_missing_this_pair_is_a_finding() {
  local dir=$work/otherpair
  prepare "$dir"
  write_verdicts "$dir" "another-pair"
  run_check "$dir"
  expect_status "$status" 1 "pair not in the verdicts"
  expect_contains "$out" "none of them is ${pair_id}" "pair not in the verdicts: the row"
}

test_a_verdict_with_one_pass_is_a_finding() {
  local dir=$work/onepass
  prepare "$dir"
  write_verdicts "$dir" "no-pass"
  run_check "$dir"
  expect_status "$status" 1 "one pass"
  expect_contains "$out" "carries 1 pass(es) where the judge makes exactly two" "one pass: the row"
}

# A judge that answers differently on the two labellings has not decided the
# pair, and D552's own reading of that is a tie — the object a real judge writes
# for it, which this check may not call a coin toss counted as a decision.
test_a_judge_that_disagrees_with_itself_is_a_tie() {
  local dir=$work/disagree
  prepare "$dir"
  write_verdicts "$dir" "disagree"
  run_check "$dir"
  expect_status "$status" 0 "passes disagree"
  expect_contains "$out" "the winner is 'tie', its two passes naming 'challenger' and 'champion'" "passes disagree: the row"
}

# A word outside the vocabulary has no arm either, and two passes that named
# none are the same tie.
test_an_answer_outside_the_vocabulary_is_a_tie() {
  local dir=$work/novocab
  prepare "$dir"
  write_verdicts "$dir" "no-vocabulary"
  run_check "$dir"
  expect_status "$status" 0 "no vocabulary"
  expect_contains "$out" "the winner is 'tie', its two passes naming 'tie' and 'tie'" "no vocabulary: the row"
}

# The other way round is the finding: decide makes the arm both passes named the
# winner, so a tie beside two agreed passes is a decision the judge did not make.
test_a_winner_its_own_passes_do_not_decide_is_a_finding() {
  local dir=$work/decidedtie
  prepare "$dir"
  write_verdicts "$dir" "decided-tie"
  run_check "$dir"
  expect_status "$status" 1 "winner the passes do not decide"
  expect_contains "$out" "carries the winner 'tie' where both of its passes name 'champion'" "winner the passes do not decide: the row"
}

test_a_tie_is_a_verdict() {
  local dir=$work/tie
  prepare "$dir"
  write_verdicts "$dir" "tie"
  run_check "$dir"
  expect_status "$status" 0 "a tie"
  expect_contains "$out" "the winner is 'tie'" "a tie: the winner"
  expect_contains "$out" "its two passes naming 'tie' and 'tie'" "a tie: what the passes name"
}

test_a_pair_missing_from_the_report_is_a_finding() {
  local dir=$work/notreported
  prepare "$dir"
  write_ab "$dir" "no-row"
  run_check "$dir"
  expect_status "$status" 1 "pair not in the report"
  expect_contains "$out" "FACT ab-listed: FAIL" "pair not in the report: the row"
  expect_contains "$out" "none of them is a pair of a champion against ${challenger}" "pair not in the report: what is missing"
}

test_a_row_that_concluded_below_the_minimum_is_a_finding() {
  local dir=$work/concludes
  prepare "$dir"
  write_ab "$dir" "concludes"
  run_check "$dir"
  expect_status "$status" 1 "a conclusion below the minimum"
  expect_contains "$out" "FACT ab-listed: FAIL" "a conclusion below the minimum: the row"
  expect_contains "$out" "under the minimum of 20" "a conclusion below the minimum: the minimum"
}

test_a_row_whose_own_figures_disagree_is_a_finding() {
  local dir=$work/figures
  prepare "$dir"
  write_ab "$dir" "inconsistent"
  run_check "$dir"
  expect_status "$status" 1 "figures that disagree"
  expect_contains "$out" "FACT ab-listed: FAIL" "figures that disagree: the row"
  expect_contains "$out" "carries wins 1 and losses 0 with decisive 3" "figures that disagree: what disagrees"
}

# A project whose window has already reached the minimum cannot show a pair
# "below the minimum": the row advises instead, and the check says so rather
# than failing a sandbox whose report is older than this check.
test_a_window_that_reached_the_minimum_passes_with_a_note() {
  local dir=$work/reached
  prepare "$dir"
  write_ab "$dir" "reached-minimum"
  run_check "$dir"
  expect_status "$status" 0 "the window reached the minimum"
  expect_contains "$out" "FACT ab-listed: PASS" "the window reached the minimum: the row"
  expect_contains "$out" "already holds 20 decisive pairs" "the window reached the minimum: what it read"
  expect_contains "$out" "is not available on an installation whose window has already reached it" "the window reached the minimum: why it is not below-minimum"
}

test_a_kept_scratch_directory_is_reported() {
  local dir=$work/kept
  prepare "$dir"
  run_check "$dir" CHECK212_KEEP_SCRATCH=1
  expect_status "$status" 1 "kept scratch"
  expect_contains "$out" "FACT scratch-removed: FAIL" "kept scratch: the row"
  expect_contains "$err" "CHECK212_KEEP_SCRATCH is set" "kept scratch: the reason"
  [ -e "$scratch" ] || fail "kept scratch: the directory was removed anyway"
  rm -rf "$scratch"
}

test_an_unknown_argument_is_refused() {
  local dir=$work/unknown
  prepare "$dir"
  run_check "$dir" -- --wat
  expect_status "$status" 2 "unknown argument"
  expect_contains "$err" "unknown argument: --wat" "unknown argument: the refusal"
  assert_no_launch "$dir" "unknown argument"
}

# The below-minimum row is load-bearing: the plan's last criterion is that the
# report lists the pair as below the minimum, and a copy of the script with that
# one comparison replaced by `false` would read the same fixture as a pass. The
# copy is run against the fixture the case above fails.
test_the_below_minimum_row_is_load_bearing() {
  local dir=$work/mutate
  prepare "$dir"
  write_ab "$dir" "concludes"
  mkdir -p "$dir/copy"
  sed 's/if \[ "\$decision" != no-conclusion \] || \[ "\$rule" != below-minimum \]; then/if false; then/' "$script" >"$dir/copy/check212.sh"
  grep -q "if false; then" "$dir/copy/check212.sh" || {
    fail "the mutation: the copy was not mutated (the pattern no longer matches)"
    return 0
  }
  run_one "$dir" "$dir/copy/check212.sh"
  expect_contains "$out" "FACT ab-listed: PASS" "the mutation: the broken copy passes the row"
  expect_status "$status" 0 "the mutation: the broken copy ends clean"
  # And the real script, on the same fixture, fails it — which is the assertion
  # the copy above shows would have been missed.
  run_check "$dir"
  expect_status "$status" 1 "the real script: the row fails"
  expect_contains "$out" "FACT ab-listed: FAIL" "the real script: the row"
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
t test_a_missing_sandbox_is_refused
t test_a_path_that_is_not_a_checkout_stops_the_check
t test_a_checkout_with_no_origin_is_a_finding
t test_no_yes_is_refused
t test_xtrace_refused
t test_a_coding_agents_session_is_refused
t test_an_account_naming_belong_is_refused
t test_a_project_naming_belong_is_refused
t test_a_build_older_than_the_release_is_refused
t test_a_build_without_the_report_is_refused
t test_a_dev_build_is_decided_by_the_feature_probe
t test_a_layer_without_the_ab_block_is_a_finding
t test_a_layer_with_rate_zero_is_a_finding
t test_a_rate_above_the_ceiling_is_a_finding
t test_a_layer_with_no_challengers_is_a_finding
t test_a_layer_with_no_judge_model_is_a_finding
t test_a_challenger_that_is_a_claude_id_is_a_finding
t test_a_challenger_on_the_direct_route_is_a_finding
t test_a_challenger_whose_route_refuses_the_repository_is_a_finding
t test_a_challenger_that_is_the_coder_pin_is_a_finding
t test_a_challenger_the_layer_does_not_list_is_a_finding
t test_a_sampler_that_pairs_nothing_is_a_finding
t test_a_launch_that_names_a_reason_is_a_finding
t test_a_pair_whose_shadow_never_started_is_a_finding
t test_a_launch_that_fails_is_a_finding
t test_arms_that_never_end_are_cancelled
t test_arms_and_the_judge_run_that_never_end_launch_no_judge
t test_a_cancel_that_does_not_answer_is_a_finding
t test_a_record_that_cannot_be_read_is_a_finding
t test_records_of_two_pairs_are_a_finding
t test_a_shadow_that_publishes_is_a_finding
t test_a_shadow_that_ran_another_model_is_a_finding
t test_a_record_without_a_wall_time_is_a_finding
t test_a_shadow_far_slower_than_the_champion_is_a_finding
t test_a_cost_over_the_cap_is_a_finding
t test_a_pair_with_no_cap_is_a_finding
t test_a_shadow_with_no_challenger_spend_is_a_finding
t test_a_judge_that_will_not_launch_is_unmeasured
t test_a_judge_run_that_never_ends_is_cancelled
t test_a_verdict_that_skipped_the_pair_is_a_finding
t test_a_verdict_from_another_judge_model_is_a_finding
t test_a_verdict_missing_this_pair_is_a_finding
t test_a_verdict_with_one_pass_is_a_finding
t test_a_judge_that_disagrees_with_itself_is_a_tie
t test_an_answer_outside_the_vocabulary_is_a_tie
t test_a_winner_its_own_passes_do_not_decide_is_a_finding
t test_a_tie_is_a_verdict
t test_a_pair_missing_from_the_report_is_a_finding
t test_a_row_that_concluded_below_the_minimum_is_a_finding
t test_a_row_whose_own_figures_disagree_is_a_finding
t test_a_window_that_reached_the_minimum_passes_with_a_note
t test_a_kept_scratch_directory_is_reported
t test_an_unknown_argument_is_refused
t test_the_below_minimum_row_is_load_bearing

if [ "$failures" -ne 0 ]; then
  echo "check212_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check212_test.sh: ok"
