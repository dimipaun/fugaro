#!/usr/bin/env bash
# Tests for scripts/live-checks/check211.sh, with a fake `fugaro` and a fake
# `gcloud` on PATH: NOTHING here reaches a cloud, a project, a bucket or a run,
# no agent launches anything, and the fakes record every call they are given --
# in which directory it was made -- so a case can assert what the check did and
# did not do.
#
# The fakes are a small model of one sandbox machine: the fake `fugaro` answers
# version, the `run --help` usage line (the offline probe for the ab-judge
# recipe), `doctor --json`, `recipes ls --json`, `config layer --json`, the ONE
# launch the check makes and the runs list, and its `cancel` really ends the run
# it names; the fake `gcloud` serves the judge run's own objects out of the
# case's own directory, keyed by the path BELOW the run's prefix, and refuses an
# object under another prefix or another project's bucket -- so the check's own
# prefix is a reading rather than a claim. A fake `git`, `gh` and `belong` log
# any call and fail: a judge run publishes nothing, and the happy path says so by
# those logs staying empty.
#
# The cases that matter are the ones where that model is bent: a build older than
# the release, a build whose usage line carries no ab-judge recipe, a catalog
# without it and a catalog whose ab-judge is shadowed, a layer with no `ab:`
# block, with a block that names no judge_model and in flow style, a refused
# launch, a launch that names no run, a run that never ends, a run whose cancel
# does not end it, a run the runs list does not carry, a row with no ref, a run
# that published a branch or a pull request, a prefix missing its manifest or its
# verdicts, a manifest that cannot be read, that names no judge model, with fewer
# pairs than the premise, with an arm that names no patch object, verdicts whose
# count does not match the manifest, verdicts naming another judge model, a
# verdict for a pair the manifest does not hold, a pair neither judged nor
# skipped, a run that skipped every pair, stored inputs that cannot be read, and
# -- of U1 itself -- a run ID, a branch, a pair ID, a model word (an arm's, and
# never the judge's: A5.1 is handed the two arms' models alone), a run-ID-shaped
# string, one bend each.
#
# Every command is bounded (see `bounded` below): a check that hangs fails here
# with the case's own name rather than hanging the suite that runs this file.
#
# Usage: bash scripts/live-checks/check211_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check211.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK211_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK211_TEST_HEAD:-900}")" ;;
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
CASE_TIMEOUT=${CHECK211_TEST_TIMEOUT:-60}

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

project=fugaro-chk211-test
# The repository's slug, as `fugaro runs ls --json` carries it in a row's `run`
# ref: ONE path segment, where the launch names the repository as `owner/name`.
# The judge run's own prefix is built from this one, so a case that reads a key
# back is reading the check's own choice.
slug=acme-sandbox-9f8e7d6c
# The one run this check launches, and the ref its objects live under. The fake
# launcher names it and the fake runs list answers for it, so a check that read
# another run's row would find no row at all.
judge_run=chk211-run-0001
ref=$slug/$judge_run
# The three models the fixtures name. The arms differ in family and the judge is
# a third ID: A5.1's redaction vocabulary is the ARMS' models with the words
# their names are built from -- Bundle is handed those two and never the judge's
# -- so the cases below can bend the input with one arm's own word ('opus') and
# read exactly one hit, and a case that bends it with the judge's ('haiku') reads
# none.
champion_model=anthropic/claude-opus-5.5
challenger_model=openai/gpt-4o
judge_model=anthropic/claude-haiku-4.5
# The branch the fake runs list answers with for the mode a case bends
# (rows-branch, rows-branch-and-pr): a name the release would only carry if the
# run pushed a branch of its own, since a healthy evaluate-mode run's row carries
# the branch lock's key 'fugaro/<its own run id>' and nothing else. The fake
# spells it itself -- it sees only its own environment -- so the two are kept the
# same by hand.
other_branch=fugaro/chk211-run-0002
# What the judge was handed, as A5.2 stores it: the fixed system text and the
# two swapped prompts, none of which names a run, a branch, a pair or a model.
JUDGE_SYSTEM='You compare two changes and answer with one line: the better one, then one word for why.'
PROMPT_A='--- BEGIN 1a2b3c4d CHANGE A ---\n... a diff ...\n--- END 1a2b3c4d CHANGE A ---\n--- BEGIN 1a2b3c4d CHANGE B ---\n... a diff ...\n--- END 1a2b3c4d CHANGE B ---\nWhich change is better? Answer with one line and nothing else: A, B or tie, then one word for why (correctness, tests, scope, clarity, safety, other).'
PROMPT_B='--- BEGIN 1a2b3c4d CHANGE A ---\n... a diff ...\n--- END 1a2b3c4d CHANGE A ---\n--- BEGIN 1a2b3c4d CHANGE B ---\n... a diff ...\n--- END 1a2b3c4d CHANGE B ---\nWhich change is better? Answer with one line and nothing else: A, B or tie, then one word for why (correctness, tests, scope, clarity, safety, other).'
# The task text the one launch carries: `fugaro run` refuses a launch with none,
# so the check passes its own sentence and the fake refuses a textless launch the
# way the real command does. Kept the same as the check's own JUDGE_TASK_TEXT by
# hand, because the fake sees only the call it is given.
JUDGE_TASK_TEXT='Judge the unjudged A/B pairs of this repository; the ab-judge recipe reads its own manifest and this text only names the launch.'

pair_id() { printf 'chk211-pair-%s' "$1"; }
arm_run() { printf 'chk211-arm-%s-%s' "$1" "$2"; }        # arm_run I champ|chall
arm_branch() { printf 'fugaro/%s' "$(arm_run "$1" "$2")"; }
input_name() { printf '%016x' "$1"; }                    # the hex A5.3 names a stored input by
input_key() { printf 'ab/inputs/%s.json' "$(input_name "$1")"; }

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check211.sh's test only. It
# answers the account read the check makes at startup and serves one object out
# of the case's own directory, keyed by the path BELOW the judge run's own
# prefix. An object under another prefix, or under another project's bucket, is
# refused rather than answered: that is what makes the check's own prefix a
# reading of the row it read rather than a claim about it. Nothing here reaches
# a real project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}

case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    case "$mode" in
    belong-account) printf 'chk211-owner@belong.example\n' ;;
    *) printf 'chk211-owner@example.com\n' ;;
    esac
  fi
  ;;
storage)
  case "${2:-}" in
  cat)
    url=${3:-}
    case "$url" in
    "gs://fugaro-runs-$FAKE_PROJECT/runs/$FAKE_REF/"*) rel=${url#"gs://fugaro-runs-$FAKE_PROJECT/runs/$FAKE_REF/"} ;;
    *)
      printf 'ERROR: (gcloud.storage.cat) NotFound: No such object: %s\n' "$url" >&2
      exit 1
      ;;
    esac
    file="$FAKE_JSON_DIR/bucket/$rel"
    if [ ! -f "$file" ]; then
      printf 'ERROR: (gcloud.storage.cat) NotFound: No such object: %s\n' "$url" >&2
      exit 1
    fi
    cat "$file"
    ;;
  *)
    printf 'gcloud: unexpected storage subcommand %s\n' "${2:-}" >&2
    exit 1
    ;;
  esac
  ;;
*)
  printf 'gcloud: unexpected subcommand %s\n' "${1:-}" >&2
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
# A stand-in for `fugaro`, for scripts/live-checks/check211.sh's test only. The
# `--json` reads come from files the case wrote, the usage line `run --help`
# prints is the check's offline probe for the ab-judge recipe, the ONE launch is
# the run the case named, and `cancel` really ends the run it names (the
# teardown's own read-back is what sees it). Every call is logged with the
# directory it was made in.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}

# ended_yes ID: whether a run the fake `cancel` stopped reads as ended. The
# cancel really changes what the runs list says, which is what makes the
# teardown's own read-back a reading of state that changed.
ended_yes() {
  [ -f "$FAKE_JSON_DIR/ended" ] && grep -qx "$1" "$FAKE_JSON_DIR/ended"
}

# The runs list: the judge run's own row, bent by the mode a case is about. A
# row whose `run` ref is empty is the one shape that leaves the check with no
# prefix to read, and a row that never reads as ended is the one the teardown
# has to settle itself.
#
# The default branch is the one a healthy evaluate-mode run really carries:
# internal/runner's runEvaluateMode writes "fugaro/<its own run id>" as the
# branch lock's key and never pushes it, so a row without it would be a row no
# real release produces -- and a check that graded it as a published branch
# would fail every correct release.
rows_doc() {
  local terminal=true status=succeeded outcome=judged branch="fugaro/$FAKE_RUN_ID" pr=0 runref="$FAKE_SLUG/$FAKE_RUN_ID"
  case "$mode" in
  rows-running)
    if ! ended_yes "$FAKE_RUN_ID"; then terminal=false; status=running; outcome=""; fi
    ;;
  rows-running-stuck)
    terminal=false
    status=running
    outcome=""
    ;;
  rows-missing)
    printf '{"project":"%s","runs":[],"totals":{},"warnings":[]}\n' "$FAKE_PROJECT"
    return 0
    ;;
  rows-no-ref) runref="" ;;
  rows-no-branch) branch="" ;;
  # A branch the release would only carry if the run pushed one: a healthy
  # evaluate-mode run's row carries the branch lock's key 'fugaro/<its own run
  # id>' and nothing else. This fake sees only its own environment, so the name
  # is spelled here and the case that reads it spells the same one.
  rows-branch) branch="fugaro/chk211-run-0002" ;;
  rows-pr) pr=41 ;;
  rows-branch-and-pr)
    branch="fugaro/chk211-run-0002"
    pr=41
    ;;
  esac
  printf '{"project":"%s","runs":[{"run":"%s","repo":"acme/sandbox","run_id":"%s","status":"%s","stage":"finalize","terminal":%s,"settled":%s,"outcome":"%s","branch":"%s","pr":%s,"batch":""}],"totals":{},"warnings":[]}\n' \
    "$FAKE_PROJECT" "$runref" "$FAKE_RUN_ID" "$status" "$terminal" "$terminal" "$outcome" "$branch" "$pr"
}

case "${1:-}" in
version)
  case "$mode" in
  old-version) printf '0.23.0\n' ;;
  dev-build) printf 'dev-9f8e7d6\n' ;;
  *) printf '0.24.1\n' ;;
  esac
  ;;
run)
  # `run --help` is the check's offline feature probe: the usage line the real
  # command prints names the catalog's recipes, and the ab-judge recipe is
  # registered by the Subscription's ee/register, so a core build's line has no
  # such name.
  case "${2:-}" in
  --help | -h)
    case "$mode" in
    no-ab-judge)
      printf 'Usage:\n  fugaro run [flags]\n\nFlags:\n      --repo string     the repository to work in\n      --recipe string   run this recipe\n'
      ;;
    *)
      printf 'Usage:\n  fugaro run [flags]\n\nFlags:\n      --repo string     the repository to work in\n      --recipe string   run this recipe (catalog: ab-judge, agent, review)\n'
      ;;
    esac
    exit 0
    ;;
  esac
  positional=0
  skip=""
  for a in "${@:2}"; do
    if [ -n "$skip" ]; then skip=""; continue; fi
    case "$a" in
    --repo | --recipe | --batch) skip=1 ;;
    -*) ;;
    *) positional=$((positional + 1)) ;;
    esac
  done
  if [ "$positional" -eq 0 ]; then
    printf 'fugaro: pass the task as TEXT or with --task-file (- for stdin)\n' >&2
    exit 1
  fi
  case "$mode" in
  launch-fails)
    printf 'fugaro: the launch was refused: no judge model is published for this project\n' >&2
    exit 1
    ;;
  launch-hangs) sleep 300 ;;
  launch-nameless)
    printf '{"project":"%s","repo":"acme/sandbox","run_id":"","branch":"","status":"launched"}\n' "$FAKE_PROJECT"
    exit 0
    ;;
  esac
  printf '%s\n' "$FAKE_RUN_ID" >"$FAKE_JSON_DIR/launched"
  printf '{"project":"%s","run":"1","repo":"acme/sandbox","run_id":"%s","branch":"","execution":"exec","status":"launched","recipe":{"name":"ab-judge","source":"catalog"}}\n' \
    "$FAKE_PROJECT" "$FAKE_RUN_ID"
  ;;
doctor)
  case "$mode" in
  doctor-fails)
    printf 'fugaro: doctor could not read the installation\n' >&2
    exit 7
    ;;
  doctor-exit1)
    cat "$FAKE_JSON_DIR/doctor.json"
    exit 1
    ;;
  doctor-unreadable)
    printf 'not a document at all\n'
    exit 0
    ;;
  doctor-noproject)
    printf '{"version":1,"ok":true,"checks":[],"project":{"name":"chk211","gcp_project":"","region":"us-east5"}}\n'
    exit 0
    ;;
  esac
  cat "$FAKE_JSON_DIR/doctor.json"
  ;;
recipes)
  case "$mode" in
  recipes-fails)
    printf 'fugaro: the catalog could not be read\n' >&2
    exit 1
    ;;
  recipes-unreadable)
    printf 'not a document at all\n'
    exit 0
    ;;
  recipes-no-judge)
    printf '{"recipes":[{"name":"agent","source":"catalog","launchable":true,"steps":[{"kind":"agent"}]}],"notes":[]}\n'
    exit 0
    ;;
  recipes-shadowed)
    printf '{"recipes":[{"name":"ab-judge","source":"repo","launchable":false,"problem":"the recipe is not valid","steps":[{"kind":"agent"}]}],"notes":[]}\n'
    exit 0
    ;;
  esac
  cat "$FAKE_JSON_DIR/recipes.json"
  ;;
config)
  if [ "${2:-}" = "layer" ]; then
    case "$mode" in
    layer-fails)
      printf 'fugaro: no project layer applies to this checkout\n' >&2
      exit 1
      ;;
    layer-unreadable)
      printf 'not a document at all\n'
      exit 0
      ;;
    esac
    cat "$FAKE_JSON_DIR/layer.json"
  else
    printf 'fugaro: unexpected config subcommand %s\n' "${2:-}" >&2
    exit 1
  fi
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
  rows_doc
  ;;
cancel)
  # The teardown's stop. It really changes what the runs list answers: the run
  # reads as ended afterwards, which is the state the check reads back.
  printf '%s\n' "${2:-}" >>"$FAKE_JSON_DIR/cancelled"
  case "$mode" in
  cancel-fails)
    printf 'fugaro: the run could not be cancelled\n' >&2
    exit 1
    ;;
  rows-running-stuck)
    printf '{"project":"%s","run":"%s","status":"running"}\n' "$FAKE_PROJECT" "${2:-}"
    exit 0
    ;;
  esac
  printf '%s\n' "${2:-}" >>"$FAKE_JSON_DIR/ended"
  printf '{"project":"%s","run":"%s","status":"cancelled","marker":true,"hard":true}\n' "$FAKE_PROJECT" "${2:-}"
  ;;
*)
  printf 'fugaro: unexpected subcommand %s\n' "${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# A tool the check must NEVER call: a judge run publishes nothing, so this check
# reaches no git remote, no forge and no production installation. It logs and
# fails rather than answering, so a case reads what was called instead of
# trusting the check's own account of itself.
write_fake_never() { # dir name
  mkdir -p "$1/bin"
  cat >"$1/bin/$2" <<'NEVER_EOF'
#!/usr/bin/env bash
# A stand-in that logs any call and fails, for scripts/live-checks/check211.sh's
# test only.
printf '%s %s (in %s)\n' "${0##*/}" "$*" "$PWD" >>"$FAKE_LOG_DIR/never.calls"
printf '%s: Check 211 must never call this\n' "${0##*/}" >&2
exit 3
NEVER_EOF
  chmod +x "$1/bin/$2"
}

# ------------------------------------------------------------------ documents
# The judge run's own objects, at the keys A5.2 writes them: the launcher's
# manifest and the run's verdicts under the run's prefix, and one stored judge
# input per judged pair at the key its verdict names (A5.3).
write_manifest() { # write_manifest DIR PAIRS [JUDGE_MODEL]
  local dir=$1 pairs=$2 judge=${3-$judge_model} i first=1
  mkdir -p "$dir/json/bucket/ab"
  {
    printf '{"version":1,"judge_model":"%s","pairs":[' "$judge"
    for i in $(seq 1 "$pairs"); do
      if [ "$first" = 1 ]; then first=0; else printf ','; fi
      printf '{"pair_id":"%s","champion":{"run_id":"%s","branch":"%s","model":"%s","patch_object":"runs/%s/%s/ab/patch.diff"},"challenger":{"run_id":"%s","branch":"%s","model":"%s","patch_object":"runs/%s/%s/ab/patch.diff"}}' \
        "$(pair_id "$i")" \
        "$(arm_run "$i" champ)" "$(arm_branch "$i" champ)" "$champion_model" "$slug" "$(arm_run "$i" champ)" \
        "$(arm_run "$i" chall)" "$(arm_branch "$i" chall)" "$challenger_model" "$slug" "$(arm_run "$i" chall)"
    done
    printf ']}\n'
  } >"$dir/json/bucket/ab/manifest.json"
}

write_verdicts() { # write_verdicts DIR PAIRS [JUDGE_MODEL]
  local dir=$1 pairs=$2 judge=${3-$judge_model} i first=1 winner
  mkdir -p "$dir/json/bucket/ab"
  {
    printf '{"version":1,"judge_model":"%s","pairs":[' "$judge"
    for i in $(seq 1 "$pairs"); do
      winner=champion
      if [ $((i % 2)) = 0 ]; then winner=challenger; fi
      if [ "$first" = 1 ]; then first=0; else printf ','; fi
      printf '{"pair_id":"%s","winner":"%s","reason":"tests","input":"%s","passes":[{"swap":false,"label":"A","arm":"%s","reason":"tests"},{"swap":true,"label":"A","arm":"%s","reason":"tests"}]}' \
        "$(pair_id "$i")" "$winner" "$(input_key "$i")" "$winner" "$winner"
    done
    printf ']}\n'
  } >"$dir/json/bucket/ab/verdicts.json"
}

write_input() { # write_input DIR I
  cat >"$1/json/bucket/ab/inputs/$(input_name "$2").json" <<JSON
{"version":1,"pair_id":"$(pair_id "$2")","judge_model":"$judge_model","passes":[
{"swap":false,"system":"$JUDGE_SYSTEM","prompt":"$PROMPT_A","answer":"A correctness"},
{"swap":true,"system":"$JUDGE_SYSTEM","prompt":"$PROMPT_B","answer":"A correctness"}]}
JSON
}

write_inputs() { # write_inputs DIR PAIRS
  local i
  mkdir -p "$1/json/bucket/ab/inputs"
  for i in $(seq 1 "$2"); do write_input "$1" "$i"; done
}

write_doctor() { # write_doctor DIR
  cat >"$1/json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[],"project":{"name":"chk211","gcp_project":"$project","region":"us-east5"}}
JSON
}

write_recipes() { # write_recipes DIR
  cat >"$1/json/recipes.json" <<JSON
{"recipes":[{"name":"ab-judge","source":"catalog","description":"judge two A/B arms","launchable":true,"steps":[{"kind":"ab_judge"}]},{"name":"agent","source":"catalog","launchable":true,"steps":[{"kind":"agent"}]}],"notes":[]}
JSON
}

# write_layer DIR STYLE: the project layer as `fugaro config layer --json` hands
# it over, whose `ab:` block is where ab.judge_model lives (D553). STYLE is
# `block` (the happy path), `flow` (the same block in flow style), `no-judge` (a
# block that names no judge_model) or `no-ab` (no block at all).
write_layer() { # write_layer DIR [STYLE]
  local style=${2:-block} yaml
  case "$style" in
  flow) yaml='agent:\n  models:\n    coder: '"$champion_model"'\nab: {judge_model: '"$judge_model"', rate: 0.1}\nworkflow: agent\n' ;;
  no-judge) yaml='agent:\n  models:\n    coder: '"$champion_model"'\nab:\n  rate: 0.1\nworkflow: agent\n' ;;
  no-ab) yaml='agent:\n  models:\n    coder: '"$champion_model"'\nworkflow: agent\n' ;;
  *) yaml='agent:\n  models:\n    coder: '"$champion_model"'\nab:\n  judge_model: '"$judge_model"'\n  rate: 0.1\nworkflow: agent\n' ;;
  esac
  cat >"$1/json/layer.json" <<JSON
{"where":"gs://fugaro-config-${project}/projects/${project}/layer.yaml","generation":7,"sha256":"3f2a1b0c","yaml":"$yaml"}
JSON
}

write_docs() { # write_docs DIR PAIRS
  local dir=$1 pairs=$2
  write_doctor "$dir"
  write_recipes "$dir"
  write_layer "$dir" block
  write_manifest "$dir" "$pairs"
  write_verdicts "$dir" "$pairs"
  write_inputs "$dir" "$pairs"
}

# A repository checkout: the check refuses a path that is not one, and every
# command it runs is run from the checkout the judge run belongs to.
write_checkout() { # write_checkout DIR
  mkdir -p "$1"
  printf 'project: chk211\ngcp_project: %s\n' "$project" >"$1/fugaro.yaml"
}

# edit_json FILE CODE: hand one document to python for a bend, with the file's
# path as sys.argv[1]. A case says what it bent in one line over the fixture
# rather than in a second fixture that could drift from this one.
edit_json() { # edit_json FILE CODE
  python3 -c "import json,sys
path=sys.argv[1]
d=json.load(open(path))
$2
json.dump(d,open(path,'w'))
" "$1"
}

# into_input DIR I WHERE TEXT: append TEXT to WHERE (`system`, `prompt` or
# `answer`) of BOTH passes of the Ith stored judge input, which is how a case
# bends the object the judge was handed without writing a second fixture.
into_input() { # into_input DIR I WHERE TEXT
  edit_json "$1/json/bucket/ab/inputs/$(input_name "$2").json" \
    "for ps in d['passes']: ps['$3'] = (ps.get('$3') or '') + ' $4'"
}

# bend_champion_model / bend_challenger_model DIR MODEL: the arm's model as the
# manifest carries it. A5.1's vocabulary is drawn from that string, so a case
# bends it to read one word the release does or does not redact.
bend_champion_model() { # bend_champion_model DIR MODEL
  edit_json "$1/json/bucket/ab/manifest.json" \
    "for v in d['pairs']: v['champion']['model'] = '$2'"
}
bend_challenger_model() { # bend_challenger_model DIR MODEL
  edit_json "$1/json/bucket/ab/manifest.json" \
    "for v in d['pairs']: v['challenger']['model'] = '$2'"
}

prepare() { # prepare DIR [PAIRS]
  local dir=$1 pairs=${2:-5}
  mkdir -p "$dir/home" "$dir/state" "$dir/logs" "$dir/tmp" "$dir/json/bucket/ab/inputs"
  write_fake_gcloud "$dir"
  write_fake_fugaro "$dir"
  write_fake_never "$dir" git
  write_fake_never "$dir" gh
  write_fake_never "$dir" belong
  write_docs "$dir" "$pairs"
  write_checkout "$dir/sandbox"
  write_checkout "$dir/other"
  : >"$dir/json/launched"
  : >"$dir/json/ended"
  : >"$dir/json/cancelled"
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
  : >"$dir/logs/never.calls"
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
  # may itself be started from one (AI_AGENT is set in Fugaro's own container).
  # The case that measures that refusal sets the marker again, and a variable
  # given as an argument wins over `env -u`.
  bounded env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE_LOG_DIR="$dir/logs" FAKE_MODE="${FAKE_MODE:-default}" \
    FAKE_JSON_DIR="$dir/json" FAKE_PROJECT="$project" FAKE_SLUG="$slug" \
    FAKE_RUN_ID="$judge_run" FAKE_REF="$ref" \
    CHECK211_SANDBOX="$dir/sandbox" CHECK211_YES=1 \
    CHECK211_RUN_TIMEOUT_SECONDS=3 CHECK211_POLL_SECONDS=1 \
    CHECK211_CANCEL_TIMEOUT_SECONDS=3 CHECK211_CLI_TIMEOUT_SECONDS=20 \
    CHECK211_LAUNCH_TIMEOUT_SECONDS=20 CHECK211_DOCTOR_TIMEOUT_SECONDS=20 \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$target" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  calls=$(cat "$dir/logs/fugaro.calls")
  gcalls=$(cat "$dir/logs/gcloud.calls")
}

# ------------------------------------------------------------------ assertions
assert_scratch_removed() { # dir what
  [ -n "$scratch" ] || {
    fail "$2: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$2: the scratch directory $scratch is still there"
}

assert_no_launch() { # what
  expect_absent "$calls" "run --repo" "$1: no judge run is launched"
}

# assert_never_called DIR what: no git remote, no forge and no production
# installation was reached, which is half of what "a judge run publishes
# nothing" means.
assert_never_called() { # dir what
  if [ -s "$1/logs/never.calls" ]; then
    fail "$2: a tool this check must never call was called: $(cat "$1/logs/never.calls")"
  fi
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check211.sh --sandbox <a sandbox repository checkout> --yes" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, what the judge run costs, that the check is theirs, what
  # the teardown does -- and that U2's own question has no surface here.
  expect_contains "$out" "two model calls per pair it takes" "--help: the cost of the judge run is stated"
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  expect_contains "$out" "stopped with \`fugaro cancel --now\`" "--help: the teardown is part of the check"
  expect_contains "$out" 'which model wrote A?' "--help: U2's own question is named"
  expect_contains "$out" "has no surface in this" "--help: and the gap the release leaves is stated"
  assert_no_launch "--help"
  assert_never_called "$dir" "--help"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK211_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 211 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version row"
  expect_contains "$out" "FACT release: PASS" "dry run: the release gate"
  expect_contains "$out" "FACT agent-session: PASS" "dry run: the session guard"
  expect_contains "$out" "FACT belong: PASS" "dry run: the belong guard"
  expect_contains "$out" "FACT bound: PASS" "dry run: the spend is stated"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore row"
  expect_contains "$out" "CHECK 211 RESULT END (exit 0)" "dry run: the end line"
  # A row the plan did not measure reads INFO, never PASS: a plan is not a
  # reading, and a block that said PASS would be one nobody took.
  expect_contains "$out" "FACT input-blind: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$out" "FACT judge-label-guess: INFO — planned" "dry run: U2 is planned too"
  expect_contains "$err" "BILLABLE" "dry run: the plan names the launch as billable"
  expect_absent "$calls" "doctor" "dry run: the installation is not read"
  expect_absent "$calls" "recipes ls" "dry run: the catalog is not read"
  expect_absent "$calls" "config layer" "dry run: the layer is not read"
  expect_absent "$gcalls" "storage" "dry run: the bucket is not read"
  assert_no_launch "dry run"
  assert_never_called "$dir" "dry run"
  assert_scratch_removed "$dir" "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "FACT sandbox: PASS" "happy path: the checkout and its project"
  expect_contains "$out" "belongs to ${project}" "happy path: the project it read"
  expect_contains "$out" "FACT recipe: PASS" "happy path: the judge recipe"
  expect_contains "$out" "carries ab-judge from catalog, launchable, with the one registered ab_judge step" "happy path: what the catalog row read"
  expect_contains "$out" "FACT layer: PASS" "happy path: the premise"
  expect_contains "$out" "names ab.judge_model: ${judge_model}" "happy path: the judge model the layer names"
  expect_contains "$out" "FACT launch: PASS" "happy path: the one judge run"
  expect_contains "$out" "one judge run launched from $dir/sandbox: ${judge_run}" "happy path: the run it launched"
  expect_contains "$out" "FACT run-end: PASS" "happy path: the run ended"
  expect_contains "$out" "FACT publishes-nothing: PASS" "happy path: the run published nothing"
  expect_contains "$out" "FACT prefix: PASS" "happy path: the run's own prefix"
  expect_contains "$out" "runs/${ref}/ holds ab/manifest.json, ab/verdicts.json" "happy path: the objects it found"
  expect_contains "$out" "FACT pairs: PASS" "happy path: the manifest's pairs"
  expect_contains "$out" "hands the judge 5 pair(s) — 10 arms" "happy path: the pairs it read"
  expect_contains "$out" "FACT verdicts: PASS" "happy path: the verdicts against the manifest"
  expect_contains "$out" "FACT input-blind: PASS" "happy path: U1"
  expect_contains "$out" "all 5 stored judge input(s) the verdicts name" "happy path: how many inputs it read"
  expect_contains "$out" "CHECK 211 RESULT END (exit 0)" "happy path: the end line"
  # U2 is RECORDED and never graded: the row carries the rate and the gap.
  expect_contains "$out" "FACT judge-label-guess: INFO — the judge named a model in 0 of the 10 answers" "happy path: U2's rate"
  expect_contains "$out" "it named a model 0 time(s)" "happy path: what the answers said"
  expect_contains "$out" "named the challenger's arm 2 time(s), the champion's 3 and a tie 0" "happy path: the verdict split"
  expect_contains "$out" "cannot be asked here" "happy path: the gap the release leaves"
  expect_contains "$out" "FACT judge-pass-agreement: INFO — the judge's two swapped passes named the same arm in 5 of the 5 pair(s) it read and disagreed in 0" "happy path: the two passes agree"
  assert_scratch_removed "$dir" "happy path"
  # The commands the reading is made of: the launch from the checkout, the
  # objects out of the bucket at the run's OWN prefix, and nothing else.
  expect_contains "$calls" "run --repo $dir/sandbox --recipe ab-judge --json $JUDGE_TASK_TEXT (in $dir/sandbox)" "happy path: the launch command"
  expect_contains "$calls" "recipes ls --json" "happy path: the catalog"
  expect_contains "$calls" "config layer --json" "happy path: the layer"
  expect_contains "$calls" "runs ls --repo $dir/sandbox --json --limit 50" "happy path: the runs list"
  expect_contains "$gcalls" "storage cat gs://fugaro-runs-${project}/runs/${ref}/ab/manifest.json --project=${project}" "happy path: the manifest key"
  expect_contains "$gcalls" "storage cat gs://fugaro-runs-${project}/runs/${ref}/ab/verdicts.json --project=${project}" "happy path: the verdicts key"
  expect_contains "$gcalls" "storage cat gs://fugaro-runs-${project}/runs/${ref}/$(input_key 1) --project=${project}" "happy path: the first stored input's key"
  expect_contains "$gcalls" "storage cat gs://fugaro-runs-${project}/runs/${ref}/$(input_key 5) --project=${project}" "happy path: the last stored input's key"
  # The judge run's objects are the whole reading: an arm's own prefix is never
  # read, and nothing reaches a forge or a git remote.
  expect_absent "$gcalls" "$(arm_run 1 champ)" "happy path: no arm's own prefix is read"
  expect_absent "$gcalls" "patch.diff" "happy path: no patch object is read"
  expect_absent "$calls" "cancel" "happy path: a run that ended is not cancelled"
  assert_never_called "$dir" "happy path"
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
  assert_no_launch "an old build"
}

test_a_build_that_carries_no_judge_is_refused() {
  # The ab-judge recipe is registered by the Subscription's ee/register, so a
  # core build's usage line has no such name: the probe is offline, and the
  # refusal comes before anything is read.
  local dir=$work/nojudge
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-ab-judge
  expect_status "$status" 1 "a build that carries no judge"
  expect_contains "$out" "FACT release: FAIL" "a build that carries no judge: the row"
  expect_contains "$out" "does not name the 'ab-judge' recipe" "a build that carries no judge: the probe"
  assert_no_launch "a build that carries no judge"
}

test_a_dev_build_is_decided_by_the_feature_probe() {
  local dir=$work/dev
  prepare "$dir"
  run_check "$dir" FAKE_MODE=dev-build
  expect_status "$status" 0 "a dev build"
  expect_contains "$out" "FACT fugaro-version: PASS" "a dev build: the version it read"
  expect_contains "$out" "dev-9f8e7d6" "a dev build: the version"
  expect_contains "$out" "FACT release: PASS" "a dev build: the row"
  expect_contains "$out" "not a release number" "a dev build: the probe decides"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/nosandbox
  prepare "$dir"
  run_check "$dir" CHECK211_SANDBOX=
  expect_status "$status" 2 "a missing sandbox"
  expect_contains "$err" "Check 211 needs --sandbox" "a missing sandbox: the message"
  assert_no_launch "a missing sandbox"
}

test_a_path_that_is_not_a_checkout_stops_the_check() {
  local dir=$work/notacheckout
  prepare "$dir"
  mkdir -p "$dir/empty"
  run_check "$dir" CHECK211_SANDBOX="$dir/empty"
  expect_status "$status" 2 "a path that is not a checkout"
  expect_contains "$err" "carries no fugaro.yaml" "a path that is not a checkout: the message"
  assert_no_launch "a path that is not a checkout"
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
  assert_no_launch "xtrace"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$out" "FACT aborted: FAIL" "a coding agent's session: the row"
  expect_contains "$err" "Check 211 is yours to run at your own terminal" "a coding agent's session: the reason"
  expect_contains "$err" "no agent runs any step of it" "a coding agent's session: the refusal"
  assert_no_launch "a coding agent's session"
}

test_a_project_naming_belong_is_refused() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" FAKE_MODE=belong-account
  expect_status "$status" 1 "an account naming belong"
  expect_contains "$out" "FACT aborted: FAIL" "an account naming belong: the row"
  expect_contains "$err" "names belong" "an account naming belong: the reason"
  expect_contains "$err" "never run there" "an account naming belong: the refusal"
  assert_no_launch "an account naming belong"
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
  assert_no_launch "a checkout of a belong project"
}

test_yes_is_required() {
  local dir=$work/noyes
  prepare "$dir"
  run_check "$dir" CHECK211_YES=
  expect_status "$status" 2 "no --yes"
  expect_contains "$err" "it asks for --yes" "no --yes: the message"
  expect_contains "$err" "Nothing has been launched" "no --yes: what did not happen"
  assert_no_launch "no --yes"
}

test_an_unknown_argument_is_refused() {
  local dir=$work/arg
  prepare "$dir"
  run_check "$dir" -- --wat
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --wat" "an unknown argument: the message"
  assert_no_launch "an unknown argument"
}

test_a_min_pairs_that_is_not_a_number_is_refused() {
  local dir=$work/badmin
  prepare "$dir"
  run_check "$dir" -- --min-pairs lots
  expect_status "$status" 2 "a --min-pairs that is not a number"
  expect_contains "$err" "needs a whole number" "a --min-pairs that is not a number: the message"
  assert_no_launch "a --min-pairs that is not a number"
}

test_a_doctor_that_does_not_answer_is_not_measured() {
  local dir=$work/doctorfail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-fails
  expect_status "$status" 2 "a doctor that does not answer"
  expect_contains "$out" "FACT sandbox: FAIL" "a doctor that does not answer: the row"
  expect_contains "$err" "could NOT measure sandbox" "a doctor that does not answer: the exit names the row"
  assert_no_launch "a doctor that does not answer"
}

test_a_doctor_that_exits_1_with_its_document_is_an_answer() {
  local dir=$work/doctorexit1
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-exit1
  expect_status "$status" 0 "a doctor that exits 1 with its document"
  expect_contains "$out" "belongs to ${project}" "a doctor that exits 1 with its document: the project it read"
}

test_a_doctor_without_a_project_is_not_measured() {
  local dir=$work/noproj
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-noproject
  expect_status "$status" 2 "a doctor without a project"
  expect_contains "$out" "FACT sandbox: FAIL" "a doctor without a project: the row"
  expect_contains "$out" "answered without a GCP project" "a doctor without a project: what it found"
  assert_no_launch "a doctor without a project"
}

test_a_doctor_document_that_cannot_be_read_is_not_measured() {
  local dir=$work/doctorjunk
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-unreadable
  expect_status "$status" 2 "a doctor document that cannot be read"
  expect_contains "$out" "FACT sandbox: FAIL" "a doctor document that cannot be read: the row"
  expect_contains "$out" "printed a document this check could not read" "a doctor document that cannot be read: what it found"
  assert_no_launch "a doctor document that cannot be read"
}

test_a_recipe_list_that_fails_is_not_measured() {
  local dir=$work/recipesfail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=recipes-fails
  expect_status "$status" 2 "a recipe list that fails"
  expect_contains "$out" "FACT recipe: FAIL" "a recipe list that fails: the row"
  expect_contains "$err" "could NOT measure recipe" "a recipe list that fails: the exit names the row"
  assert_no_launch "a recipe list that fails"
}

test_a_recipe_list_without_the_judge_is_a_finding() {
  local dir=$work/recipesnone
  prepare "$dir"
  run_check "$dir" FAKE_MODE=recipes-no-judge
  expect_status "$status" 1 "a recipe list without the judge"
  expect_contains "$out" "FACT recipe: FAIL" "a recipe list without the judge: the row"
  expect_contains "$out" "none of them is ab-judge" "a recipe list without the judge: what it found"
  assert_no_launch "a recipe list without the judge"
}

test_a_shadowed_judge_recipe_is_a_finding() {
  # A recipe of that name from the repository shadows the catalog's, and a
  # shadow that is not the judge measures something else.
  local dir=$work/recipeshadow
  prepare "$dir"
  run_check "$dir" FAKE_MODE=recipes-shadowed
  expect_status "$status" 1 "a shadowed judge recipe"
  expect_contains "$out" "FACT recipe: FAIL" "a shadowed judge recipe: the row"
  expect_contains "$out" "carries ab-judge from repo but it is not the judge this check launches" "a shadowed judge recipe: what it found"
  assert_no_launch "a shadowed judge recipe"
}

test_a_recipe_document_that_cannot_be_read_is_not_measured() {
  local dir=$work/recipesjunk
  prepare "$dir"
  run_check "$dir" FAKE_MODE=recipes-unreadable
  expect_status "$status" 2 "a recipe document that cannot be read"
  expect_contains "$out" "FACT recipe: FAIL" "a recipe document that cannot be read: the row"
  expect_contains "$out" "printed a document this check could not read" "a recipe document that cannot be read: what it found"
  assert_no_launch "a recipe document that cannot be read"
}

test_a_layer_with_no_ab_block_is_a_finding() {
  local dir=$work/noab
  prepare "$dir"
  write_layer "$dir" no-ab
  run_check "$dir"
  expect_status "$status" 1 "a layer with no ab block"
  expect_contains "$out" "FACT layer: FAIL" "a layer with no ab block: the row"
  expect_contains "$out" "carries no top-level ab: block" "a layer with no ab block: what it found"
  expect_contains "$out" "fugaro config publish" "a layer with no ab block: the remedy"
  assert_no_launch "a layer with no ab block"
}

test_a_layer_without_a_judge_model_is_a_finding() {
  local dir=$work/nojudgemodel
  prepare "$dir"
  write_layer "$dir" no-judge
  run_check "$dir"
  expect_status "$status" 1 "a layer without a judge model"
  expect_contains "$out" "FACT layer: FAIL" "a layer without a judge model: the row"
  expect_contains "$out" "names no judge_model" "a layer without a judge model: what it found"
  expect_contains "$out" "keys are rate" "a layer without a judge model: the keys it read"
  assert_no_launch "a layer without a judge model"
}

test_a_layer_that_cannot_be_read_is_not_measured() {
  local dir=$work/layerfail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=layer-fails
  expect_status "$status" 2 "a layer that cannot be read"
  expect_contains "$out" "FACT layer: FAIL" "a layer that cannot be read: the row"
  expect_contains "$err" "could NOT measure layer" "a layer that cannot be read: the exit names the row"
  assert_no_launch "a layer that cannot be read"
}

test_a_layer_document_that_cannot_be_read_is_not_measured() {
  local dir=$work/layerjunk
  prepare "$dir"
  run_check "$dir" FAKE_MODE=layer-unreadable
  expect_status "$status" 2 "a layer document that cannot be read"
  expect_contains "$out" "FACT layer: FAIL" "a layer document that cannot be read: the row"
  expect_contains "$out" "printed a document this check could not read" "a layer document that cannot be read: what it found"
  assert_no_launch "a layer document that cannot be read"
}

test_a_flow_style_ab_block_is_read() {
  local dir=$work/flow
  prepare "$dir"
  write_layer "$dir" flow
  run_check "$dir"
  expect_status "$status" 0 "a flow style ab block"
  expect_contains "$out" "FACT layer: PASS" "a flow style ab block: the row"
  expect_contains "$out" "names ab.judge_model: ${judge_model}" "a flow style ab block: the judge model it read"
}

test_a_launch_that_fails_is_not_measured() {
  local dir=$work/launchfail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=launch-fails
  expect_status "$status" 2 "a launch that fails"
  expect_contains "$out" "FACT launch: FAIL" "a launch that fails: the row"
  expect_contains "$out" "the run was refused or the launch timed out" "a launch that fails: what it found"
  expect_contains "$err" "could NOT measure launch" "a launch that fails: the exit names the row"
}

test_a_launch_that_names_no_run_is_not_measured() {
  local dir=$work/nameless
  prepare "$dir"
  run_check "$dir" FAKE_MODE=launch-nameless
  expect_status "$status" 2 "a launch that names no run"
  expect_contains "$out" "FACT launch: FAIL" "a launch that names no run: the row"
  expect_contains "$out" "answered without a run id" "a launch that names no run: what it found"
}

test_a_launch_that_hangs_is_not_measured() {
  local dir=$work/hang
  prepare "$dir"
  run_check "$dir" FAKE_MODE=launch-hangs CHECK211_LAUNCH_TIMEOUT_SECONDS=2
  expect_status "$status" 2 "a launch that hangs"
  expect_contains "$out" "FACT launch: FAIL" "a launch that hangs: the row"
  expect_contains "$err" "could NOT measure launch" "a launch that hangs: the exit names the row"
}

test_a_run_that_never_ends_is_not_measured_and_cancelled() {
  # A run still going is not a reading: its verdicts are written after a
  # teardown that read only what it had left so far, so the row says it could
  # not measure and the teardown stops the run and reads it back.
  local dir=$work/running
  prepare "$dir"
  run_check "$dir" FAKE_MODE=rows-running
  expect_status "$status" 2 "a run that never ends"
  expect_contains "$out" "FACT run-end: FAIL" "a run that never ends: the row"
  expect_contains "$out" "had not ended after 3s" "a run that never ends: what it found"
  expect_contains "$err" "could NOT measure run-end" "a run that never ends: the exit names the row"
  expect_contains "$out" "FACT restore: PASS" "a run that never ends: the teardown"
  expect_contains "$calls" "cancel ${judge_run} --now --json" "a run that never ends: the run is stopped"
  expect_contains "$(cat "$dir/json/ended")" "$judge_run" "a run that never ends: the run really ended"
  assert_never_called "$dir" "a run that never ends"
}

test_a_run_the_cancel_does_not_end_is_a_finding() {
  # A cancel that answers while the run keeps going is the state the Restore row
  # may NOT read PASS over: the run can still write its verdicts after the check
  # is over, and nothing else here will be looking.
  local dir=$work/stuck
  prepare "$dir"
  run_check "$dir" FAKE_MODE=rows-running-stuck
  expect_status "$status" 1 "a run the cancel does not end"
  expect_contains "$out" "FACT restore: FAIL" "a run the cancel does not end: the row"
  expect_contains "$out" "the runs list still reads it as going after 3s" "a run the cancel does not end: what it found"
  expect_contains "$out" "Stop it again with 'fugaro cancel ${judge_run} --now'" "a run the cancel does not end: the remedy names the run"
  expect_contains "$out" "writes its verdicts after this check is over" "a run the cancel does not end: why it matters"
}

test_a_run_the_runs_list_does_not_carry_is_a_finding() {
  # A launched run the runs list does not answer for is a teardown that cannot
  # read what the run left, so the row says so and names it rather than skipping
  # it: a run nobody can read is not a run that left nothing.
  local dir=$work/unlisted
  prepare "$dir"
  run_check "$dir" FAKE_MODE=rows-missing
  expect_status "$status" 1 "a run the runs list does not carry"
  expect_contains "$out" "FACT restore: FAIL" "a run the runs list does not carry: the row"
  expect_contains "$out" "is not in 'fugaro runs ls --repo $dir/sandbox' at all" "a run the runs list does not carry: what it found"
  expect_contains "$out" "$judge_run" "a run the runs list does not carry: the run it names"
}

test_a_row_without_a_ref_is_not_measured() {
  # The ref is the prefix the objects are read at, and the check reads it from
  # the runs list rather than building it: a row without one leaves no key to
  # read, and a key this check guessed would be a key the release never named.
  local dir=$work/noref
  prepare "$dir"
  run_check "$dir" FAKE_MODE=rows-no-ref
  expect_status "$status" 2 "a row without a ref"
  expect_contains "$out" "FACT run-end: FAIL" "a row without a ref: the row"
  expect_contains "$out" "carries no run ref" "a row without a ref: what it found"
  expect_absent "$gcalls" "storage cat" "a row without a ref: no key is guessed"
}

test_the_branch_lock_key_is_not_a_published_branch() {
  # The healthy row: an evaluate-mode run's record carries the branch lock's key
  # "fugaro/<its own run id>" (internal/runner's runEvaluateMode) and never
  # pushes it, so a row that graded any branch as a published one would fail
  # every correct release. This is that row, read as it really comes.
  local dir=$work/lock
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the branch lock's key"
  expect_contains "$out" "FACT publishes-nothing: PASS" "the branch lock's key: the row"
  expect_contains "$out" "branch 'fugaro/${judge_run}'" "the branch lock's key: the branch it read"
  expect_contains "$out" "the branch lock's key alone and never pushes it" "the branch lock's key: why it is not a push"
}

test_a_row_with_no_branch_at_all_is_not_a_finding() {
  local dir=$work/nobranch
  prepare "$dir"
  run_check "$dir" FAKE_MODE=rows-no-branch
  expect_status "$status" 0 "a row with no branch"
  expect_contains "$out" "FACT publishes-nothing: PASS" "a row with no branch: the row"
  expect_contains "$out" "branch 'none'" "a row with no branch: what it read"
}

test_a_run_that_published_a_branch_is_a_finding() {
  local dir=$work/branch
  prepare "$dir"
  run_check "$dir" FAKE_MODE=rows-branch
  expect_status "$status" 1 "a run that published a branch"
  expect_contains "$out" "FACT publishes-nothing: FAIL" "a run that published a branch: the row"
  expect_contains "$out" "carries branch '${other_branch}', which is not 'fugaro/${judge_run}'" "a run that published a branch: the branch it found and the key it is not"
  expect_contains "$out" "A5.2, TestJudgeRunPublishesNothing" "a run that published a branch: why it matters"
}

test_a_run_that_opened_a_pull_request_is_a_finding() {
  local dir=$work/pr
  prepare "$dir"
  run_check "$dir" FAKE_MODE=rows-pr
  expect_status "$status" 1 "a run that opened a pull request"
  expect_contains "$out" "FACT publishes-nothing: FAIL" "a run that opened a pull request: the row"
  expect_contains "$out" "pull request 41" "a run that opened a pull request: the number it found"
}

test_a_runs_list_that_fails_is_not_measured() {
  # The run-end row cannot be read at all -- and the teardown cannot read the
  # list back either, so whether the run it launched is still going is unknown:
  # that is a finding (exit 1) and not a "could not measure", because the check
  # is leaving a run nobody has settled.
  local dir=$work/rowsfail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=rows-fails
  expect_status "$status" 1 "a runs list that fails"
  expect_contains "$out" "FACT run-end: FAIL" "a runs list that fails: the row"
  expect_contains "$out" "FACT publishes-nothing: FAIL" "a runs list that fails: the row beside it"
  expect_contains "$out" "FACT restore: FAIL" "a runs list that fails: the teardown"
  expect_contains "$out" "could not be read back" "a runs list that fails: what the teardown found"
  expect_contains "$out" "fugaro cancel ${judge_run} --now" "a runs list that fails: the remedy names the run"
}

test_a_runs_document_that_cannot_be_read_is_not_measured() {
  local dir=$work/rowsjunk
  prepare "$dir"
  run_check "$dir" FAKE_MODE=rows-unreadable
  expect_status "$status" 1 "a runs document that cannot be read"
  expect_contains "$out" "FACT run-end: FAIL" "a runs document that cannot be read: the row"
  expect_contains "$out" "printed a document this check could not read" "a runs document that cannot be read: what it found"
  expect_contains "$out" "FACT restore: FAIL" "a runs document that cannot be read: the teardown"
}

test_a_missing_manifest_is_a_finding() {
  local dir=$work/nomanifest
  prepare "$dir"
  rm -f "$dir/json/bucket/ab/manifest.json"
  run_check "$dir"
  expect_status "$status" 1 "a missing manifest"
  expect_contains "$out" "FACT prefix: FAIL" "a missing manifest: the row"
  expect_contains "$out" "does not hold ab/manifest.json" "a missing manifest: what is missing"
  expect_contains "$out" "What was found is ab/verdicts.json" "a missing manifest: what was found"
}

test_a_missing_verdicts_object_is_a_finding() {
  local dir=$work/noverdicts
  prepare "$dir"
  rm -f "$dir/json/bucket/ab/verdicts.json"
  run_check "$dir"
  expect_status "$status" 1 "a missing verdicts object"
  expect_contains "$out" "FACT prefix: FAIL" "a missing verdicts object: the row"
  expect_contains "$out" "does not hold ab/verdicts.json" "a missing verdicts object: what is missing"
}

test_a_manifest_that_cannot_be_read_is_a_finding() {
  local dir=$work/manifestjunk
  prepare "$dir"
  printf 'not a document at all\n' >"$dir/json/bucket/ab/manifest.json"
  run_check "$dir"
  expect_status "$status" 1 "a manifest that cannot be read"
  expect_contains "$out" "FACT prefix: FAIL" "a manifest that cannot be read: the row"
  expect_contains "$out" "is not a document this check could read" "a manifest that cannot be read: what it found"
}

test_a_manifest_without_a_judge_model_is_a_finding() {
  local dir=$work/manifestnojudge
  prepare "$dir"
  write_manifest "$dir" 5 ""
  run_check "$dir"
  expect_status "$status" 1 "a manifest without a judge model"
  expect_contains "$out" "FACT pairs: FAIL" "a manifest without a judge model: the row"
  expect_contains "$out" "the manifest names no judge_model" "a manifest without a judge model: what it found"
}

test_a_manifest_with_fewer_pairs_than_the_premise_is_a_finding() {
  local dir=$work/onepair
  prepare "$dir" 1
  run_check "$dir"
  expect_status "$status" 1 "a manifest with fewer pairs than the premise"
  expect_contains "$out" "FACT pairs: FAIL" "a manifest with fewer pairs than the premise: the row"
  expect_contains "$out" "holds 1 pair(s) and this check's premise is 5" "a manifest with fewer pairs than the premise: what it found"
  expect_contains "$out" "Sample more pairs" "a manifest with fewer pairs than the premise: the remedy"
  # And the premise is the check's own bound, not the plan's sentence: the same
  # manifest is the reading the plan asks for at --min-pairs 1.
  run_check "$dir" -- --min-pairs 1
  expect_status "$status" 0 "a manifest with fewer pairs than the premise: at --min-pairs 1"
  expect_contains "$out" "hands the judge 1 pair(s) — 2 arms" "a manifest with fewer pairs than the premise: the lowered premise"
}

test_a_manifest_with_an_incomplete_arm_is_a_finding() {
  local dir=$work/incomplete
  prepare "$dir"
  edit_json "$dir/json/bucket/ab/manifest.json" \
    "d['pairs'][1]['challenger']['patch_object']=''"
  run_check "$dir"
  expect_status "$status" 1 "a manifest with an incomplete arm"
  expect_contains "$out" "FACT pairs: FAIL" "a manifest with an incomplete arm: the row"
  expect_contains "$out" "the manifest's arms are incomplete for $(pair_id 2)/challenger" "a manifest with an incomplete arm: the arm it names"
}

test_a_verdict_count_that_does_not_match_the_manifest_is_a_finding() {
  local dir=$work/verdictcount
  prepare "$dir"
  edit_json "$dir/json/bucket/ab/verdicts.json" "d['pairs'].pop()"
  run_check "$dir"
  expect_status "$status" 1 "a verdict count that does not match the manifest"
  expect_contains "$out" "FACT verdicts: FAIL" "a verdict count that does not match the manifest: the row"
  expect_contains "$out" "the verdicts carry 4 entries where the manifest hands the judge 5 pairs" "a verdict count that does not match the manifest: what it found"
}

test_verdicts_naming_another_judge_model_is_a_finding() {
  local dir=$work/verdictjudge
  prepare "$dir"
  write_verdicts "$dir" 5 "openai/gpt-4o"
  run_check "$dir"
  expect_status "$status" 1 "verdicts naming another judge model"
  expect_contains "$out" "FACT verdicts: FAIL" "verdicts naming another judge model: the row"
  expect_contains "$out" "the verdicts name judge_model 'openai/gpt-4o'" "verdicts naming another judge model: what it found"
}

test_a_verdict_for_a_pair_the_manifest_does_not_hold_is_a_finding() {
  local dir=$work/unknownpair
  prepare "$dir"
  edit_json "$dir/json/bucket/ab/verdicts.json" "d['pairs'][2]['pair_id']='chk211-pair-9'"
  run_check "$dir"
  expect_status "$status" 1 "a verdict for an unknown pair"
  expect_contains "$out" "FACT verdicts: FAIL" "a verdict for an unknown pair: the row"
  expect_contains "$out" "entries for pairs the manifest does not hold: chk211-pair-9" "a verdict for an unknown pair: the pair it names"
}

test_a_pair_neither_judged_nor_skipped_is_a_finding() {
  local dir=$work/noinput
  prepare "$dir"
  edit_json "$dir/json/bucket/ab/verdicts.json" "d['pairs'][1]['input']=''"
  run_check "$dir"
  expect_status "$status" 1 "a pair neither judged nor skipped"
  expect_contains "$out" "FACT verdicts: FAIL" "a pair neither judged nor skipped: the row"
  expect_contains "$out" "$(pair_id 2) was neither judged nor skipped" "a pair neither judged nor skipped: the pair it names"
  expect_absent "$gcalls" "$(input_key 2)" "a pair neither judged nor skipped: its input is not read"
}

test_a_run_that_skipped_every_pair_is_a_finding() {
  local dir=$work/allskipped
  prepare "$dir"
  edit_json "$dir/json/bucket/ab/verdicts.json" \
    "for v in d['pairs']: v['input']=''; v['skipped']='truncated'"
  run_check "$dir"
  expect_status "$status" 1 "a run that skipped every pair"
  expect_contains "$out" "FACT verdicts: FAIL" "a run that skipped every pair: the row"
  expect_contains "$out" "judged none of its 5 pair(s)" "a run that skipped every pair: what it found"
  expect_absent "$gcalls" "ab/inputs/" "a run that skipped every pair: no stored input is read"
}

test_a_stored_input_that_is_not_in_the_bucket_is_a_finding() {
  # The verdict names a key the bucket does not hold: that is a run whose stored
  # input is missing, and the row says which pair rather than reporting the
  # pairs it could read as if they were all of them.
  local dir=$work/missinginput
  prepare "$dir"
  rm -f "$dir/json/bucket/ab/inputs/$(input_name 3).json"
  run_check "$dir"
  expect_status "$status" 1 "a stored input that is not in the bucket"
  expect_contains "$out" "FACT input-blind: FAIL" "a stored input that is not in the bucket: the row"
  expect_contains "$out" "the stored input $(input_key 3) of pair $(pair_id 3) could not be read out of the bucket" "a stored input that is not in the bucket: what it found"
  expect_contains "$out" "4 stored input(s) were read" "a stored input that is not in the bucket: how many it did read"
}

test_a_stored_input_that_is_not_a_document_is_a_finding() {
  local dir=$work/junkinput
  prepare "$dir"
  printf 'not a document at all\n' >"$dir/json/bucket/ab/inputs/$(input_name 2).json"
  run_check "$dir"
  expect_status "$status" 1 "a stored input that is not a document"
  expect_contains "$out" "FACT input-blind: FAIL" "a stored input that is not a document: the row"
  expect_contains "$out" "is not a document this check could read" "a stored input that is not a document: what it found"
}

test_stored_inputs_that_cannot_be_read_are_not_measured() {
  # Not one of the stored inputs the verdicts name could be read: the row is a
  # FAIL that says the check could not measure U1, and the exit is 2 rather than
  # 1 — a check that could not read the object must not report it as a finding
  # about the release.
  local dir=$work/alljunk
  prepare "$dir"
  local i
  for i in 1 2 3 4 5; do printf 'not a document at all\n' >"$dir/json/bucket/ab/inputs/$(input_name "$i").json"; done
  run_check "$dir"
  expect_status "$status" 2 "stored inputs that cannot be read"
  expect_contains "$out" "FACT input-blind: FAIL" "stored inputs that cannot be read: the row"
  expect_contains "$out" "not one of the 5 stored judge inputs the verdicts name could be read" "stored inputs that cannot be read: what it found"
  expect_contains "$out" "FACT judge-label-guess: INFO — not recorded" "stored inputs that cannot be read: U2 is not recorded either"
  expect_contains "$err" "could NOT measure input-blind" "stored inputs that cannot be read: the exit names the row"
}

test_a_run_id_in_the_prompt_is_a_finding() {
  local dir=$work/hitrun
  prepare "$dir"
  into_input "$dir" 1 prompt "$(arm_run 1 champ)"
  run_check "$dir"
  expect_status "$status" 1 "a run ID in the prompt"
  expect_contains "$out" "FACT input-blind: FAIL" "a run ID in the prompt: the row"
  expect_contains "$out" "pair $(pair_id 1) pass 1: the prompt carries the run '$(arm_run 1 champ)'" "a run ID in the prompt: the hit"
  expect_contains "$out" "TestBundleCarriesNoIdentifier" "a run ID in the prompt: what the row is about"
}

test_a_branch_in_the_prompt_is_a_finding() {
  # The branch carries the arm's run ID, so both needles fire: the branch is
  # what the bundle's own identifier check names first, and the run inside it is
  # the same string a second time.
  local dir=$work/hitbranch
  prepare "$dir"
  into_input "$dir" 2 prompt "$(arm_branch 2 chall)"
  run_check "$dir"
  expect_status "$status" 1 "a branch in the prompt"
  expect_contains "$out" "FACT input-blind: FAIL" "a branch in the prompt: the row"
  expect_contains "$out" "the prompt carries the branch '$(arm_branch 2 chall)'" "a branch in the prompt: the branch hit"
  expect_contains "$out" "the prompt carries the run '$(arm_run 2 chall)'" "a branch in the prompt: the run inside it"
}

test_a_pair_id_in_the_prompt_is_a_finding() {
  local dir=$work/hitpair
  prepare "$dir"
  into_input "$dir" 3 prompt "$(pair_id 3)"
  run_check "$dir"
  expect_status "$status" 1 "a pair ID in the prompt"
  expect_contains "$out" "FACT input-blind: FAIL" "a pair ID in the prompt: the row"
  expect_contains "$out" "the prompt carries the pair '$(pair_id 3)'" "a pair ID in the prompt: the hit"
}

test_a_model_word_in_the_prompt_is_a_finding() {
  # A5.1's vocabulary is the model ID and the words its name is built from, at
  # least four bytes long: 'opus' is the champion's own word, and the arms are
  # what the judge is not supposed to be able to tell apart.
  local dir=$work/hitchampion
  prepare "$dir"
  into_input "$dir" 1 prompt "opus"
  run_check "$dir"
  expect_status "$status" 1 "a model word in the prompt"
  expect_contains "$out" "FACT input-blind: FAIL" "a model word in the prompt: the row"
  expect_contains "$out" "the prompt carries the model 'opus'" "a model word in the prompt: the hit"
}

test_the_judge_models_own_word_is_not_a_finding() {
  # A5.1 is handed the two arms' models and never the judge's (bundle.go's
  # modelWords(champion.Model, challenger.Model)), and the stored input names the
  # judge model in its own `judge_model` field anyway: a row that graded a diff
  # for naming it would fail a correct release after a billed run and blame the
  # anonymiser for something it was never asked to remove.
  local dir=$work/judgeword
  prepare "$dir"
  into_input "$dir" 4 prompt "haiku"
  run_check "$dir"
  expect_status "$status" 0 "the judge model's own word in the prompt"
  expect_contains "$out" "FACT input-blind: PASS" "the judge model's own word in the prompt: the row"
  expect_absent "$out" "the prompt carries the model" "the judge model's own word in the prompt: no hit is reported"
}

test_a_run_id_shaped_string_in_the_prompt_is_a_finding() {
  # A string the arms' own IDs do not name but that has a run ID's shape is what
  # A5.1's own backstop search is for: an identifier that reached the bundle by
  # another route.
  local dir=$work/hitshape
  prepare "$dir"
  into_input "$dir" 5 prompt "20260101-010101-abcd"
  run_check "$dir"
  expect_status "$status" 1 "a run-ID-shaped string in the prompt"
  expect_contains "$out" "FACT input-blind: FAIL" "a run-ID-shaped string in the prompt: the row"
  expect_contains "$out" "the prompt carries the run-id shape '20260101-010101-abcd'" "a run-ID-shaped string in the prompt: the hit"
}

test_a_line_that_looks_like_a_commit_trailer_is_not_a_finding() {
  # The plan's U1 names a commit trailer, and the object A5.1 anonymises carries
  # no commit metadata to strip: it is a TREE diff (bundle.go's Patch.Diff). A
  # hunk that mentions its own tooling is diff body text -- this repository alone
  # has 22 tracked files carrying "generated with" and 5 carrying "noreply@" --
  # so a row that graded those strings would fail a correct release and call it
  # the anonymiser not holding.
  local dir=$work/trailer
  prepare "$dir"
  into_input "$dir" 1 prompt "Co-Authored-By: someone@example.invalid noreply@example.invalid generated with a tool"
  run_check "$dir"
  expect_status "$status" 0 "a line that looks like a commit trailer"
  expect_contains "$out" "FACT input-blind: PASS" "a line that looks like a commit trailer: the row"
  expect_absent "$out" "the prompt carries the trailer" "a line that looks like a commit trailer: no hit is reported"
}

test_a_variant_word_after_the_version_is_not_a_finding() {
  # A5.1's walk of a model's name stops at its version (bundle.go's versionWord),
  # so 'flash' in deepseek-v4-flash is not in the vocabulary: it names no model
  # on its own, and a diff saying "flash" is a diff, not a leak.
  local dir=$work/hitvariant
  prepare "$dir"
  bend_challenger_model "$dir" deepseek/deepseek-v4-flash
  into_input "$dir" 1 prompt "flash"
  run_check "$dir"
  expect_status "$status" 0 "a variant word after the version"
  expect_contains "$out" "FACT input-blind: PASS" "a variant word after the version: the row"
  expect_absent "$out" "the prompt carries the model" "a variant word after the version: no hit is reported"
}

test_a_word_before_the_version_is_a_finding() {
  # The other half of the same rule: the words up to the version ARE the model's
  # name, so the same ID's own word is a leak and the row names it.
  local dir=$work/hitname
  prepare "$dir"
  bend_challenger_model "$dir" deepseek/deepseek-v4-flash
  into_input "$dir" 1 prompt "deepseek"
  run_check "$dir"
  expect_status "$status" 1 "a word before the version"
  expect_contains "$out" "FACT input-blind: FAIL" "a word before the version: the row"
  expect_contains "$out" "the prompt carries the model 'deepseek'" "a word before the version: the hit"
}

test_the_family_word_of_a_claude_id_is_a_finding() {
  # config.ModelFamily is part of A5.1's vocabulary: an ID spelled without its
  # vendor still redacts 'anthropic'. The champion here is written that way, so a
  # prompt naming the family is a hit -- a reader that took the words from the ID
  # alone would miss it and pass a leak.
  local dir=$work/hitfamily
  prepare "$dir"
  bend_champion_model "$dir" claude-opus-5-5
  into_input "$dir" 1 prompt "anthropic"
  run_check "$dir"
  expect_status "$status" 1 "the family word of a Claude ID"
  expect_contains "$out" "FACT input-blind: FAIL" "the family word of a Claude ID: the row"
  expect_contains "$out" "the prompt carries the model 'anthropic'" "the family word of a Claude ID: the hit"
}

test_a_system_text_that_names_a_run_is_a_finding() {
  # U1 is graded over what the judge was handed, and the system text is half of
  # it: a hit there is the same finding as a hit in the prompt.
  local dir=$work/hitsystem
  prepare "$dir"
  into_input "$dir" 2 system "$(arm_run 2 champ)"
  run_check "$dir"
  expect_status "$status" 1 "a system text that names a run"
  expect_contains "$out" "FACT input-blind: FAIL" "a system text that names a run: the row"
  expect_contains "$out" "pass 1: the system carries the run '$(arm_run 2 champ)'" "a system text that names a run: the hit"
}

test_a_model_named_in_the_answer_is_recorded_and_not_graded() {
  # U2 is a RATE: the judge's own answer may name a model, and that is recorded
  # and never a finding. What must not happen is the answer being graded as U1 —
  # the answer is the model's, not the bundle's, and the bundle is what U1 is
  # about. 'claude' is a word of the champion's name, so the answer that carries
  # it names a model once per pass, and the rate counts two.
  local dir=$work/answer
  prepare "$dir"
  into_input "$dir" 1 answer "claude"
  run_check "$dir"
  expect_status "$status" 0 "a model named in the answer"
  expect_contains "$out" "FACT input-blind: PASS" "a model named in the answer: U1 holds"
  expect_contains "$out" "FACT judge-label-guess: INFO — the judge named a model in 2 of the 10 answers" "a model named in the answer: the rate"
  expect_contains "$out" "it named a model 2 time(s)" "a model named in the answer: what the answers said"
}

test_the_two_passes_disagreeing_is_recorded_and_not_graded() {
  # The order is swapped on the second pass and only an arm both passes agree on
  # is decided, so a disagreement is a tie by the design (D552) and not a
  # finding: the row records how often the two passes named the same arm.
  local dir=$work/disagree
  prepare "$dir"
  edit_json "$dir/json/bucket/ab/verdicts.json" "d['pairs'][0]['passes'][1]['arm']='challenger'"
  run_check "$dir"
  expect_status "$status" 0 "the two passes disagreeing"
  expect_contains "$out" "FACT judge-pass-agreement: INFO — the judge's two swapped passes named the same arm in 4 of the 5 pair(s) it read and disagreed in 1" "the two passes disagreeing: the rate"
  expect_contains "$out" "a disagreement is a tie by the design (D552) and not a finding" "the two passes disagreeing: why it is not graded"
}

test_a_kept_scratch_directory_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK211_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$err" "CHECK211_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
  [ -d "$scratch" ] || fail "a kept scratch directory: $scratch was removed anyway"
  rm -rf "$scratch" 2>/dev/null || true
}

# THE MUTATION. The row that grades U1 is the plan's own reading -- "the stored
# judge input names no run ID, branch, model or commit trailer" -- and it is only
# worth anything if it really fails an input that names one. The case is a COPY
# of the script with the comparison replaced by `false`, run against the same
# fixture the case above fails: if the row were not load-bearing, the copy would
# read the same and this test would report nothing.
test_the_judge_input_row_is_load_bearing() {
  local dir=$work/mutate
  prepare "$dir"
  into_input "$dir" 1 prompt "$(arm_run 1 champ)"
  mkdir -p "$dir/copy"
  sed 's/if \[ -n "\$hits" \]; then/if false; then/' "$script" >"$dir/copy/check211.sh"
  grep -q "if false; then" "$dir/copy/check211.sh" || {
    fail "the mutation: the copy was not mutated (the pattern no longer matches)"
    return 0
  }
  run_copy "$dir" "$dir/copy/check211.sh"
  expect_contains "$out" "FACT input-blind: PASS" "the mutation: the broken copy passes the row"
  expect_absent "$out" "FACT input-blind: FAIL" "the mutation: the broken copy does not fail the row"
  expect_status "$status" 0 "the mutation: the broken copy ends clean"
  # And the real script, on the same fixture, fails it -- which is the assertion
  # the copy above shows would have been missed.
  run_check "$dir"
  expect_status "$status" 1 "the real script: the row fails"
  expect_contains "$out" "FACT input-blind: FAIL" "the real script: the row"
  expect_contains "$out" "the prompt carries the run '$(arm_run 1 champ)'" "the real script: the hit"
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
t test_a_build_older_than_the_release_is_refused
t test_a_build_that_carries_no_judge_is_refused
t test_a_dev_build_is_decided_by_the_feature_probe
t test_a_missing_sandbox_is_refused
t test_a_path_that_is_not_a_checkout_stops_the_check
t test_xtrace_refused
t test_a_coding_agents_session_is_refused
t test_a_project_naming_belong_is_refused
t test_a_checkout_of_a_belong_project_is_refused
t test_yes_is_required
t test_an_unknown_argument_is_refused
t test_a_min_pairs_that_is_not_a_number_is_refused
t test_a_doctor_that_does_not_answer_is_not_measured
t test_a_doctor_that_exits_1_with_its_document_is_an_answer
t test_a_doctor_without_a_project_is_not_measured
t test_a_doctor_document_that_cannot_be_read_is_not_measured
t test_a_recipe_list_that_fails_is_not_measured
t test_a_recipe_list_without_the_judge_is_a_finding
t test_a_shadowed_judge_recipe_is_a_finding
t test_a_recipe_document_that_cannot_be_read_is_not_measured
t test_a_layer_with_no_ab_block_is_a_finding
t test_a_layer_without_a_judge_model_is_a_finding
t test_a_layer_that_cannot_be_read_is_not_measured
t test_a_layer_document_that_cannot_be_read_is_not_measured
t test_a_flow_style_ab_block_is_read
t test_a_launch_that_fails_is_not_measured
t test_a_launch_that_names_no_run_is_not_measured
t test_a_launch_that_hangs_is_not_measured
t test_a_run_that_never_ends_is_not_measured_and_cancelled
t test_a_run_the_cancel_does_not_end_is_a_finding
t test_a_run_the_runs_list_does_not_carry_is_a_finding
t test_a_row_without_a_ref_is_not_measured
t test_the_branch_lock_key_is_not_a_published_branch
t test_a_row_with_no_branch_at_all_is_not_a_finding
t test_a_run_that_published_a_branch_is_a_finding
t test_a_run_that_opened_a_pull_request_is_a_finding
t test_a_runs_list_that_fails_is_not_measured
t test_a_runs_document_that_cannot_be_read_is_not_measured
t test_a_missing_manifest_is_a_finding
t test_a_missing_verdicts_object_is_a_finding
t test_a_manifest_that_cannot_be_read_is_a_finding
t test_a_manifest_without_a_judge_model_is_a_finding
t test_a_manifest_with_fewer_pairs_than_the_premise_is_a_finding
t test_a_manifest_with_an_incomplete_arm_is_a_finding
t test_a_verdict_count_that_does_not_match_the_manifest_is_a_finding
t test_verdicts_naming_another_judge_model_is_a_finding
t test_a_verdict_for_a_pair_the_manifest_does_not_hold_is_a_finding
t test_a_pair_neither_judged_nor_skipped_is_a_finding
t test_a_run_that_skipped_every_pair_is_a_finding
t test_a_stored_input_that_is_not_in_the_bucket_is_a_finding
t test_a_stored_input_that_is_not_a_document_is_a_finding
t test_stored_inputs_that_cannot_be_read_are_not_measured
t test_a_run_id_in_the_prompt_is_a_finding
t test_a_branch_in_the_prompt_is_a_finding
t test_a_pair_id_in_the_prompt_is_a_finding
t test_a_model_word_in_the_prompt_is_a_finding
t test_the_judge_models_own_word_is_not_a_finding
t test_a_run_id_shaped_string_in_the_prompt_is_a_finding
t test_a_line_that_looks_like_a_commit_trailer_is_not_a_finding
t test_a_variant_word_after_the_version_is_not_a_finding
t test_a_word_before_the_version_is_a_finding
t test_the_family_word_of_a_claude_id_is_a_finding
t test_a_system_text_that_names_a_run_is_a_finding
t test_a_model_named_in_the_answer_is_recorded_and_not_graded
t test_the_two_passes_disagreeing_is_recorded_and_not_graded
t test_a_kept_scratch_directory_is_reported
t test_the_judge_input_row_is_load_bearing

if [ "$failures" -ne 0 ]; then
  echo "check211_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check211_test.sh: ok"
