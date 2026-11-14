#!/usr/bin/env bash
# Tests for scripts/live-checks/check491.sh, with fake `gcloud` and `fugaro` on
# PATH: NOTHING here reaches a cloud, a project, a run or a forge, no agent runs
# a live command, and the fakes record every call they are given — in which
# directory it was made — so a case can assert what the check did and did not do.
#
# The fakes are a small model of one machine: the fake `fugaro` answers the calls
# the check makes (version, the two `--help` probes, doctor, the plan, the
# launch, the runs list and the cancel) and the fake `gcloud` serves the parent's
# record and each filed run's task. The plan is served per call — the first plan
# read, the launch, the second plan read — so a case says what the check saw
# before and after it filed. The cases that matter are the ones where the model
# is bent: a build older than the release, a parent pull request that is not
# merged, a record of another run or with no deferrals, a plan that does not
# cover the record's groups (a foreign set of keys, a group named twice, a group
# nobody names, one the cap left behind), a launch that is refused or names a
# task with no run, a listing that leaves a run out or carries one no deferral
# filed, a task filed without the debt mark, against another parent, with other
# keys or at another mode, a second call that would file the same work again, and
# a cancel that does not answer.
#
# Every command is bounded (see `bounded` below): a check that hangs fails here
# with the case's own name rather than hanging the suite that runs this file.
#
# Usage: bash scripts/live-checks/check491_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check491.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK491_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK491_TEST_HEAD:-900}")" ;;
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
CASE_TIMEOUT=${CHECK491_TEST_TIMEOUT:-60}

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

project=fugaro-chk491-test
parent_run=run-20261114100000-1
filed_run=run-20261114110000-2
filed_run2=run-20261114110000-3
# The repository's slug, as the plan carries it: ONE path segment, where the
# repository is named `owner/name`. The record's and the tasks' object names are
# built from it, so a case that reads a key back is reading the check's choice.
slug=acme-sandbox-9f8e7d6c
repo=acme/sandbox
# The two findings the fake record defers, tied together by the triage into ONE
# group: the plan files one task for both, which is the grouping the check is
# about. A case that wants two groups drops the group key (see write_record).
finding_key=deferredkey0001
finding_key2=deferredkey0002
finding_key3=deferredkey0003

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check491.sh's test only. It
# answers the account read the check makes and serves the one object each call
# names: the parent's record, or the task of the run the object key ends in.
# Nothing here reaches a real project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
config)
  if [ "${2:-}" = "get-value" ] && [ "${3:-}" = "account" ]; then
    case "$mode" in
    belong-account) printf 'chk491-owner@belong.example\n' ;;
    *) printf 'chk491-owner@example.com\n' ;;
    esac
  fi
  ;;
storage)
  if [ "${2:-}" = "cat" ]; then
    object=${3:-}
    case "$mode:$object" in
    record-missing:*result.json)
      echo "ERROR: (gcloud.storage.cat) NotFound: No such object" >&2
      exit 1
      ;;
    task-missing:*task.json)
      echo "ERROR: (gcloud.storage.cat) NotFound: No such object" >&2
      exit 1
      ;;
    esac
    case "$object" in
    *result.json) cat "$FAKE_JSON_DIR/record.json" ;;
    *task.json)
      run=${object%/task.json}
      run=${run##*/}
      if [ -f "$FAKE_JSON_DIR/task-$run.json" ]; then
        cat "$FAKE_JSON_DIR/task-$run.json"
      else
        cat "$FAKE_JSON_DIR/task.json"
      fi
      ;;
    *)
      echo "gcloud: unexpected object ${object:-none}" >&2
      exit 1
      ;;
    esac
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

write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check491.sh's test only. The
# `--json` reads come from files the case wrote — the plan is served per read, so
# the check sees one plan before it files and another after — the two `--help`
# probes print the usage lines the real commands print, and every call is logged
# with the directory it was made in.
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
followups)
  case " $* " in
  *" --help "*)
    case "$mode" in
    no-feature)
      printf 'fugaro: unknown command "followups"\n' >&2
      exit 1
      ;;
    *) printf 'Usage:\n  fugaro followups RUN [--launch] [--max N] [--json]\n' ;;
    esac
    exit 0
    ;;
  esac
  case " $* " in
  *" --launch "*)
    printf 'launch\n' >>"$FAKE_LOG_DIR/launch.calls"
    case "$mode" in
    launch-refused)
      printf 'fugaro: launching the task for the group failed: the sandbox refused it\n' >&2
      exit 1
      ;;
    launch-hangs)
      sleep 5
      exit 0
      ;;
    esac
    cat "$FAKE_JSON_DIR/launch.json"
    exit 0
    ;;
  esac
  # The plan read: the Nth one answers the case's own plan$N.json, so the check
  # reads the plan it was given before it filed and the plan it is given after.
  n=1
  [ -f "$FAKE_LOG_DIR/plan.calls" ] && n=$(( $(wc -l <"$FAKE_LOG_DIR/plan.calls") + 1 ))
  printf 'plan %s\n' "$n" >>"$FAKE_LOG_DIR/plan.calls"
  case "$mode" in
  plan-refused)
    printf 'fugaro: pull request #77 of acme/sandbox is open, not merged: its findings are still being worked on. Re-run this after it is merged\n' >&2
    exit 1
    ;;
  followups-hangs)
    sleep 5
    exit 0
    ;;
  esac
  if [ -f "$FAKE_JSON_DIR/plan$n.json" ]; then
    cat "$FAKE_JSON_DIR/plan$n.json"
  else
    cat "$FAKE_JSON_DIR/plan1.json"
  fi
  ;;
runs)
  case "${2:-}" in
  ls)
    case " $* " in
    *" --help "*)
      case "$mode" in
      no-feature) printf 'Usage:\n  fugaro runs ls [--repo R] [--json] [--limit N]\n' ;;
      *) printf 'Usage:\n  fugaro runs ls [--repo R] [--json] [--limit N] [--debt]\n' ;;
      esac
      exit 0
      ;;
    esac
    printf 'debt\n' >>"$FAKE_LOG_DIR/debt.calls"
    case "$mode" in
    debt-hangs)
      sleep 5
      exit 0
      ;;
    esac
    cat "$FAKE_JSON_DIR/debt.json"
    ;;
  *)
    echo "fugaro: unexpected runs subcommand ${2:-}" >&2
    exit 1
    ;;
  esac
  ;;
doctor)
  cat "$FAKE_JSON_DIR/doctor.json"
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
    exit 0
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
# The keys of the one group the default record creates, and of the task the plan
# files for it: ONE group of two findings, which is what "one debt task per
# group" is read against.
group_keys="$finding_key,$finding_key2"

write_docs() { # dir
  local dir=$1 json=$1/json
  mkdir -p "$json"
  cat >"$json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[],"project":{"name":"chk491","gcp_project":"$project","region":"us-east5"}}
JSON
  cat >"$json/cancel.json" <<JSON
{"project":"$project","run":"$slug/$filed_run","status":"cancelled","marker":true,"hard":true}
JSON
  write_record "$dir" ok
  write_plan "$dir" ok
  write_launch "$dir" ok
  write_debt "$dir" ok
  write_task "$dir" "$filed_run" ok
}

# write_record DIR STATE: the parent's `result.json`. The state is the reading a
# case bends: the mode the parent resolved, the deferrals it left, the group the
# triage tied them to, and what its own total says.
write_record() { # write_record DIR STATE
  local state=$2 findings total=2 rigor='"rigor":{"mode":"startup","from":"request","at":"2026-11-14T10:00:00Z"}'
  findings='[{"key":"'"$finding_key"'","summary":"docs/notes.md says five","reason":"a one-word documentation correction","group":"g1","source":"agent"},{"key":"'"$finding_key2"'","summary":"docs/notes.md says four","reason":"the same sentence counted twice","group":"g1","source":"agent"}]'
  case "$state" in
  ok) ;;
  ungrouped)
    findings='[{"key":"'"$finding_key"'","summary":"docs/notes.md says five","reason":"a one-word documentation correction","source":"agent"},{"key":"'"$finding_key2"'","summary":"docs/notes.md says four","reason":"the same sentence counted twice","source":"agent"}]'
    ;;
  no-deferrals) findings='[]' total=0 ;;
  truncated)
    total=3
    ;;
  hotfix) rigor='"rigor":{"mode":"hotfix","from":"area","at":"2026-11-14T10:00:00Z"}' ;;
  esac
  cat >"$1/json/record.json" <<JSON
{"version":1,"run_id":"$parent_run","repo":"acme/sandbox","workflow":"agent","status":"succeeded","stage":"finalize","outcome":"ready",$rigor,"deferred":{"findings":$findings,"total":$total},"cost":{"model_usd":0.31,"total_usd":0.31},"reviews":[{"round":1,"tier":"standard","verdict":"pass","findings":2,"head":"abc1234"}]}
JSON
}

# write_plan DIR STATE: the plan the fake `fugaro followups` answers with — the
# first read into plan1.json and the read after the launch into plan2.json. A
# case writes the state it is about and the check reads it.
write_plan() { # write_plan DIR STATE
  local dir=$1 state=$2 one two
  case "$state" in
  ok)
    one='{"version":1,"repo":"'"$repo"'","slug":"'"$slug"'","run_id":"'"$parent_run"'","branch":"fugaro/20261114-100000-abcd","pr":77,"pr_url":"https://github.com/acme/sandbox/pull/77","deferred":2,"deferred_total":2,"max":10,"launched":false,"tasks":[{"keys":["'"$finding_key"'","'"$finding_key2"'"],"summary":"docs/notes.md says five","rigor":"startup","run_id":"","status":"planned"}]}'
    two='{"version":1,"repo":"'"$repo"'","slug":"'"$slug"'","run_id":"'"$parent_run"'","branch":"fugaro/20261114-100000-abcd","pr":77,"pr_url":"https://github.com/acme/sandbox/pull/77","deferred":2,"deferred_total":2,"max":10,"launched":false,"tasks":[],"skipped":[{"keys":["'"$finding_key"'","'"$finding_key2"'"],"run":"'"$filed_run"'"}]}'
    ;;
  filed)
    # Nothing left to file: every group of the record already has a run.
    one='{"version":1,"repo":"'"$repo"'","slug":"'"$slug"'","run_id":"'"$parent_run"'","branch":"fugaro/20261114-100000-abcd","pr":77,"pr_url":"https://github.com/acme/sandbox/pull/77","deferred":2,"deferred_total":2,"max":10,"launched":false,"tasks":[],"skipped":[{"keys":["'"$finding_key"'","'"$finding_key2"'"],"run":"'"$filed_run"'"}]}'
    two="$one"
    ;;
  ungrouped)
    one='{"version":1,"repo":"'"$repo"'","slug":"'"$slug"'","run_id":"'"$parent_run"'","branch":"fugaro/20261114-100000-abcd","pr":77,"pr_url":"https://github.com/acme/sandbox/pull/77","deferred":2,"deferred_total":2,"max":10,"launched":false,"tasks":[{"keys":["'"$finding_key"'"],"summary":"docs/notes.md says five","rigor":"startup","run_id":"","status":"planned"},{"keys":["'"$finding_key2"'"],"summary":"docs/notes.md says four","rigor":"startup","run_id":"","status":"planned"}]}'
    two='{"version":1,"repo":"'"$repo"'","slug":"'"$slug"'","run_id":"'"$parent_run"'","branch":"fugaro/20261114-100000-abcd","pr":77,"deferred":2,"deferred_total":2,"max":10,"launched":false,"tasks":[],"skipped":[{"keys":["'"$finding_key"'"],"run":"'"$filed_run"'"},{"keys":["'"$finding_key2"'"],"run":"'"$filed_run2"'"}]}'
    ;;
  foreign)
    one='{"version":1,"repo":"'"$repo"'","slug":"'"$slug"'","run_id":"'"$parent_run"'","pr":77,"deferred":2,"deferred_total":2,"max":10,"launched":false,"tasks":[{"keys":["'"$finding_key3"'"],"summary":"a finding nobody deferred","rigor":"startup","run_id":"","status":"planned"}]}'
    two="$one"
    ;;
  split)
    # The group's two findings filed as two tasks: the keys are the record's, but
    # no task names the group the record tied them into.
    one='{"version":1,"repo":"'"$repo"'","slug":"'"$slug"'","run_id":"'"$parent_run"'","pr":77,"deferred":2,"deferred_total":2,"max":10,"launched":false,"tasks":[{"keys":["'"$finding_key"'"],"summary":"one half","rigor":"startup","run_id":"","status":"planned"},{"keys":["'"$finding_key2"'"],"summary":"the other half","rigor":"startup","run_id":"","status":"planned"}]}'
    two="$one"
    ;;
  double)
    one='{"version":1,"repo":"'"$repo"'","slug":"'"$slug"'","run_id":"'"$parent_run"'","pr":77,"deferred":2,"deferred_total":2,"max":10,"launched":false,"tasks":[{"keys":["'"$finding_key"'","'"$finding_key2"'"],"summary":"docs/notes.md says five","rigor":"startup","run_id":"","status":"planned"}],"skipped":[{"keys":["'"$finding_key"'","'"$finding_key2"'"],"run":"'"$filed_run"'"}]}'
    two="$one"
    ;;
  over-cap)
    one='{"version":1,"repo":"'"$repo"'","slug":"'"$slug"'","run_id":"'"$parent_run"'","pr":77,"deferred":2,"deferred_total":2,"max":1,"launched":false,"over_cap":1,"tasks":[]}'
    two="$one"
    ;;
  truncated)
    one='{"version":1,"repo":"'"$repo"'","slug":"'"$slug"'","run_id":"'"$parent_run"'","pr":77,"deferred":2,"deferred_total":3,"max":10,"launched":false,"tasks":[{"keys":["'"$finding_key"'","'"$finding_key2"'"],"summary":"docs/notes.md says five","rigor":"startup","run_id":"","status":"planned"}]}'
    two='{"version":1,"repo":"'"$repo"'","slug":"'"$slug"'","run_id":"'"$parent_run"'","pr":77,"deferred":2,"deferred_total":3,"max":10,"launched":false,"tasks":[],"skipped":[{"keys":["'"$finding_key"'","'"$finding_key2"'"],"run":"'"$filed_run"'"}]}'
    ;;
  again)
    # The plan read after this call would file the group again.
    one='{"version":1,"repo":"'"$repo"'","slug":"'"$slug"'","run_id":"'"$parent_run"'","pr":77,"deferred":2,"deferred_total":2,"max":10,"launched":false,"tasks":[{"keys":["'"$finding_key"'","'"$finding_key2"'"],"summary":"docs/notes.md says five","rigor":"startup","run_id":"","status":"planned"}]}'
    two="$one"
    ;;
  left-one)
    two='{"version":1,"repo":"'"$repo"'","slug":"'"$slug"'","run_id":"'"$parent_run"'","pr":77,"deferred":2,"deferred_total":2,"max":10,"launched":false,"tasks":[],"skipped":[{"keys":["'"$finding_key"'"],"run":"'"$filed_run"'"}]}'
    one='{"version":1,"repo":"'"$repo"'","slug":"'"$slug"'","run_id":"'"$parent_run"'","pr":77,"deferred":2,"deferred_total":2,"max":10,"launched":false,"tasks":[{"keys":["'"$finding_key"'","'"$finding_key2"'"],"summary":"docs/notes.md says five","rigor":"startup","run_id":"","status":"planned"}]}'
    ;;
  no-run)
    one='{"version":1,"repo":"'"$repo"'","slug":"'"$slug"'","run_id":"'"$parent_run"'","pr":77,"deferred":2,"deferred_total":2,"max":10,"launched":false,"tasks":[{"keys":["'"$finding_key"'","'"$finding_key2"'"],"summary":"docs/notes.md says five","rigor":"startup","run_id":"","status":"planned"}]}'
    two="$one"
    ;;
  unwritten)
    # A plan with no run at all: the record's key cannot be built from it.
    one='{"version":1,"repo":"","slug":"","run_id":"","pr":77,"deferred":2,"deferred_total":2,"max":10,"launched":false,"tasks":[]}'
    two="$one"
    ;;
  esac
  printf '%s\n' "$one" >"$dir/json/plan1.json"
  printf '%s\n' "$two" >"$dir/json/plan2.json"
}

# write_launch DIR STATE: the document `fugaro followups --launch` answers with.
write_launch() { # write_launch DIR STATE
  local dir=$1 state=$2
  case "$state" in
  ok)
    cat >"$dir/json/launch.json" <<JSON
{"version":1,"repo":"$repo","slug":"$slug","run_id":"$parent_run","branch":"fugaro/20261114-100000-abcd","pr":77,"pr_url":"https://github.com/acme/sandbox/pull/77","deferred":2,"deferred_total":2,"max":10,"launched":true,"tasks":[{"keys":["$finding_key","$finding_key2"],"summary":"docs/notes.md says five","rigor":"startup","run_id":"$filed_run","status":"launched"}]}
JSON
    ;;
  ungrouped)
    cat >"$dir/json/launch.json" <<JSON
{"version":1,"repo":"$repo","slug":"$slug","run_id":"$parent_run","branch":"fugaro/20261114-100000-abcd","pr":77,"deferred":2,"deferred_total":2,"max":10,"launched":true,"tasks":[{"keys":["$finding_key"],"summary":"one","rigor":"startup","run_id":"$filed_run","status":"launched"},{"keys":["$finding_key2"],"summary":"the other","rigor":"startup","run_id":"$filed_run2","status":"launched"}]}
JSON
    ;;
  no-run)
    # A task the command reports as launched without naming its run: the run is
    # going and this check can neither read it nor stop it.
    cat >"$dir/json/launch.json" <<JSON
{"version":1,"repo":"$repo","slug":"$slug","run_id":"$parent_run","branch":"fugaro/20261114-100000-abcd","pr":77,"deferred":2,"deferred_total":2,"max":10,"launched":true,"tasks":[{"keys":["$finding_key","$finding_key2"],"summary":"docs/notes.md says five","rigor":"startup","run_id":"","status":"launched"}]}
JSON
    ;;
  not-launched)
    cat >"$dir/json/launch.json" <<JSON
{"version":1,"repo":"$repo","slug":"$slug","run_id":"$parent_run","branch":"fugaro/20261114-100000-abcd","pr":77,"deferred":2,"deferred_total":2,"max":10,"launched":false,"tasks":[{"keys":["$finding_key","$finding_key2"],"summary":"docs/notes.md says five","rigor":"startup","run_id":"","status":"planned"}]}
JSON
    ;;
  esac
}

# write_debt DIR STATE: the listing `fugaro runs ls --debt --json` answers with.
# `empty` is a listing a moment too early; `extra` a row no deferral filed;
# `wrong-parent` and `wrong-keys` a row filed under the wrong reading of one
# group.
write_debt() { # write_debt DIR STATE
  local dir=$1 state=$2
  local row row2
  row='{"run":"'"$slug/$filed_run"'","run_id":"'"$filed_run"'","repo":"'"$repo"'","status":"running","stage":"bootstrap","terminal":false,"follow_up_of":{"run":"'"$parent_run"'","keys":["'"$finding_key"'","'"$finding_key2"'"]}}'
  row2='{"run":"'"$slug/$filed_run2"'","run_id":"'"$filed_run2"'","repo":"'"$repo"'","status":"running","stage":"bootstrap","terminal":false,"follow_up_of":{"run":"'"$parent_run"'","keys":["'"$finding_key2"'"]}}'
  case "$state" in
  ok) cat >"$dir/json/debt.json" <<JSON
{"project":"$project","runs":[$row],"totals":{"runs":1},"warnings":[]}
JSON
    ;;
  ungrouped)
    # One row per task the launch filed: an ungrouped record's two groups are
    # two tasks, and each carries its own key.
    row='{"run":"'"$slug/$filed_run"'","run_id":"'"$filed_run"'","repo":"'"$repo"'","status":"running","terminal":false,"follow_up_of":{"run":"'"$parent_run"'","keys":["'"$finding_key"'"]}}'
    row2='{"run":"'"$slug/$filed_run2"'","run_id":"'"$filed_run2"'","repo":"'"$repo"'","status":"running","terminal":false,"follow_up_of":{"run":"'"$parent_run"'","keys":["'"$finding_key2"'"]}}'
    cat >"$dir/json/debt.json" <<JSON
{"project":"$project","runs":[$row,$row2],"totals":{"runs":2},"warnings":[]}
JSON
    ;;
  empty) cat >"$dir/json/debt.json" <<JSON
{"project":"$project","runs":[],"totals":{"runs":0},"warnings":[]}
JSON
    ;;
  extra) cat >"$dir/json/debt.json" <<JSON
{"project":"$project","runs":[{"run":"$slug/run-20261114090000-9","run_id":"run-20261114090000-9","repo":"$repo","status":"succeeded","terminal":true},$row],"totals":{"runs":2},"warnings":[]}
JSON
    ;;
  wrong-parent) cat >"$dir/json/debt.json" <<JSON
{"project":"$project","runs":[{"run":"$slug/$filed_run","run_id":"$filed_run","repo":"$repo","status":"running","terminal":false,"follow_up_of":{"run":"run-20261113090000-9","keys":["$finding_key","$finding_key2"]}}],"totals":{"runs":1},"warnings":[]}
JSON
    ;;
  wrong-keys) cat >"$dir/json/debt.json" <<JSON
{"project":"$project","runs":[{"run":"$slug/$filed_run","run_id":"$filed_run","repo":"$repo","status":"running","terminal":false,"follow_up_of":{"run":"$parent_run","keys":["$finding_key3"]}}],"totals":{"runs":1},"warnings":[]}
JSON
    ;;
  esac
}

# write_task DIR RUN STATE: one filed run's own task.json.
write_task() { # write_task DIR RUN STATE
  local dir=$1 run=$2 state=$3 debt=true rigor=startup parent=$parent_run keys="[\"$finding_key\",\"$finding_key2\"]" recorded=$2
  case "$state" in
  ok) ;;
  first) keys="[\"$finding_key\"]" ;;
  second) keys="[\"$finding_key2\"]" ;;
  not-debt) debt=false ;;
  wrong-parent) parent=run-20261113090000-9 ;;
  wrong-keys) keys="[\"$finding_key3\"]" ;;
  wrong-run) recorded=run-20261114120000-9 ;;
  other-mode) rigor=pro ;;
  no-mode) rigor="" ;;
  esac
  local rigor_field=""
  [ -n "$rigor" ] && rigor_field="\"rigor\":\"$rigor\","
  cat >"$dir/json/task-$run.json" <<JSON
{"version":1,"run_id":"$recorded","repo":"$repo","ref":"main","workflow":"agent","task":"the deferred finding","requested_by":"chk491-owner@example.com",${rigor_field}"debt":$debt,"follow_up_of":{"run":"$parent","keys":$keys}}
JSON
}

# A repository checkout: the check refuses a path that is not one, and every
# command is run from the checkout it belongs to.
write_checkout() { # dir
  mkdir -p "$1"
  printf 'project: chk491\n' >"$1/fugaro.yaml"
}

prepare() { # dir
  mkdir -p "$1/home" "$1/state" "$1/logs" "$1/tmp"
  write_fake_gcloud "$1"
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
  : >"$dir/logs/launch.calls"
  : >"$dir/logs/debt.calls"
  : >"$dir/logs/plan.calls"
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
    FAKE_JSON_DIR="$dir/json" \
    CHECK491_SANDBOX="$dir/sandbox" CHECK491_RUN="$parent_run" CHECK491_YES=1 \
    CHECK491_LIST_TIMEOUT_SECONDS=2 CHECK491_POLL_SECONDS=1 \
    CHECK491_CLI_TIMEOUT_SECONDS=20 CHECK491_LAUNCH_TIMEOUT_SECONDS=20 \
    CHECK491_CANCEL_TIMEOUT_SECONDS=20 CHECK491_DOCTOR_TIMEOUT_SECONDS=20 \
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
  expect_absent "$(cat "$1/logs/launch.calls")" "launch" "$2: nothing is filed"
}

assert_no_cancel() { # dir what
  expect_absent "$(cat "$1/logs/cancel.calls")" "cancel" "$2: no run is cancelled"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check491.sh --sandbox <a sandbox checkout> --run <the parent run> --yes" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, that the tasks it files are runs and that it stops them.
  expect_contains "$out" "one sandbox run started per group" "--help: the cost of a run is stated"
  expect_contains "$out" "Nothing it files outlives it" "--help: what it does with the runs it files"
  expect_absent "$out" "Nothing it launches can outlive it" "--help: the sentence names what it does — it files tasks, it does not launch runs of its own"
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  assert_no_launch "$dir" "--help"
}

test_a_missing_run_is_refused() {
  local dir=$work/norun
  prepare "$dir"
  run_check "$dir" CHECK491_RUN=
  expect_status "$status" 2 "no --run"
  expect_contains "$err" "Check 491 needs --run" "no --run: the refusal"
  expect_absent "$out" "FACT " "no --run: no block is printed for a run that never started"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK491_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 491 RESULT" "dry run: the block"
  expect_contains "$out" "FACT fugaro-version: PASS" "dry run: the version row"
  expect_contains "$out" "FACT agent-session: PASS" "dry run: the session guard"
  expect_contains "$out" "FACT belong: PASS" "dry run: the belong guard"
  expect_contains "$out" "FACT bound: PASS" "dry run: the cost is stated"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore row"
  expect_contains "$out" "CHECK 491 RESULT END (exit 0)" "dry run: the end line"
  # A row the plan did not measure reads INFO, never PASS: a plan is not a
  # reading, and a block that said PASS would be one nobody took.
  expect_contains "$out" "FACT parent-plan: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$out" "FACT idempotent: INFO — planned" "dry run: the second plan is planned too"
  expect_contains "$err" "BILLABLE" "dry run: the plan says the tasks cost money"
  expect_contains "$err" "cancelled from the exit trap" "dry run: the plan says what happens to the runs it files"
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
  expect_contains "$out" "FACT parent-plan: PASS" "happy path: the plan"
  expect_contains "$out" "planned from a MERGED pull request: #77 of acme/sandbox" "happy path: the merge the plan proves"
  expect_contains "$out" "FACT parent-record: PASS" "happy path: the parent's record"
  expect_contains "$out" "it deferred 2 of 2 finding(s)" "happy path: what the record defers"
  expect_contains "$out" "FACT groups: PASS" "happy path: one task per group"
  expect_contains "$out" "1 group(s) are named by exactly one task each: 1 to file and 0 already filed" "happy path: the reading itself"
  expect_contains "$out" "FACT launched: PASS" "happy path: the filing"
  expect_contains "$out" "filed ONE debt task per group — 1 of them" "happy path: one task per group"
  expect_contains "$out" "FACT debt-list: PASS" "happy path: the listing"
  expect_contains "$out" "FACT debt-task: PASS" "happy path: the tasks themselves"
  expect_contains "$out" "FACT mode: PASS" "happy path: the mode each task carries"
  expect_contains "$out" "FACT idempotent: PASS" "happy path: nothing left to file"
  expect_contains "$out" "a second 'fugaro followups ${parent_run}' writes nothing" "happy path: what idempotent means"
  expect_contains "$out" "FACT cancelled: PASS" "happy path: the runs are stopped"
  expect_contains "$out" "FACT restore: PASS" "happy path: the restore"
  expect_contains "$out" "CHECK 491 RESULT END (exit 0)" "happy path: the end line"
  # Every command ran in the checkout it belongs to, the record and the tasks
  # were read at their own keys — the one-segment slug, not the owner/name the
  # plan names the repository with — the plan was read twice around the launch,
  # and the run this check filed was cancelled.
  local calls
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_contains "$calls" "doctor --json (in $dir/sandbox)" "happy path: doctor runs in the sandbox checkout"
  expect_contains "$calls" "followups $parent_run --json --max 10 (in $dir/sandbox)" "happy path: the plan is read in the checkout"
  expect_contains "$calls" "followups $parent_run --launch --max 10 --json (in $dir/sandbox)" "happy path: the launch is made in the checkout"
  expect_contains "$calls" "runs ls --repo $repo --debt --json --limit 50" "happy path: the listing is read for the repository the plan named"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "gs://fugaro-runs-${project}/runs/${slug}/${parent_run}/result.json" "happy path: the parent's record is read at its own key"
  expect_contains "$(cat "$dir/logs/gcloud.calls")" "gs://fugaro-runs-${project}/runs/${slug}/${filed_run}/task.json" "happy path: the filed task is read at its own key"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "runs/acme/sandbox/" "happy path: the key is the one-segment slug, not the owner/name the plan names"
  expect_contains "$(cat "$dir/logs/cancel.calls")" "cancel $filed_run --now --json" "happy path: the run this check filed is cancelled"
  assert_scratch_removed "$dir" "happy path"
}

test_every_group_is_filed_and_the_second_read_writes_nothing() {
  local dir=$work/twogroups
  prepare "$dir"
  write_record "$dir" ungrouped
  write_plan "$dir" ungrouped
  write_launch "$dir" ungrouped
  write_debt "$dir" ungrouped
  write_task "$dir" "$filed_run" first
  write_task "$dir" "$filed_run2" second
  run_check "$dir"
  expect_status "$status" 0 "two groups"
  expect_contains "$out" "2 group(s) are named by exactly one task each: 2 to file and 0 already filed" "two groups: the reading"
  expect_contains "$out" "filed ONE debt task per group — 2 of them" "two groups: two runs"
  expect_contains "$out" "FACT debt-list: PASS" "two groups: both listed"
  expect_contains "$out" "FACT debt-task: PASS" "two groups: both read"
  local calls
  calls=$(cat "$dir/logs/cancel.calls")
  expect_contains "$calls" "cancel $filed_run --now --json" "two groups: the first run is cancelled"
  expect_contains "$calls" "cancel $filed_run2 --now --json" "two groups: the second run is cancelled"
  assert_scratch_removed "$dir" "two groups"
}

test_the_filing_is_billable_and_needs_yes() {
  local dir=$work/noyes
  prepare "$dir"
  run_check "$dir" CHECK491_YES=
  expect_status "$status" 2 "no --yes"
  expect_contains "$err" "asks for --yes" "no --yes: the check says what it wants"
  assert_no_launch "$dir" "no --yes"
  assert_no_cancel "$dir" "no --yes"
  assert_scratch_removed "$dir" "no --yes"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/nosandbox
  prepare "$dir"
  run_check "$dir" CHECK491_SANDBOX=
  expect_status "$status" 2 "no sandbox"
  expect_contains "$err" "Check 491 needs --sandbox" "no sandbox: the refusal"
  expect_absent "$out" "FACT " "no sandbox: no block is printed for a run that never started"
}

test_a_path_that_is_not_a_checkout_stops_the_check() {
  local dir=$work/norepo
  prepare "$dir"
  mkdir -p "$dir/nothing"
  run_check "$dir" CHECK491_SANDBOX="$dir/nothing"
  expect_status "$status" 2 "not a repository"
  expect_contains "$err" "carries no fugaro.yaml" "not a repository: the refusal"
  assert_no_launch "$dir" "not a repository"
  assert_scratch_removed "$dir" "not a repository"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "xtrace"
  expect_contains "$out" "FACT xtrace: FAIL" "xtrace: the row"
  expect_contains "$err" "this shell is tracing" "xtrace: the refusal"
  assert_no_launch "$dir" "xtrace"
  assert_scratch_removed "$dir" "xtrace"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "agent session"
  expect_contains "$err" "coding agent's session" "agent session: the refusal"
  expect_contains "$out" "CHECK 491 RESULT END (exit 1)" "agent session: the block"
  assert_no_launch "$dir" "agent session"
  assert_scratch_removed "$dir" "agent session"
}

test_an_account_naming_belong_is_refused() {
  local dir=$work/belongacct
  prepare "$dir"
  run_check "$dir" FAKE_MODE=belong-account
  expect_status "$status" 1 "belong account"
  expect_contains "$err" "names belong" "belong account: the refusal"
  assert_no_launch "$dir" "belong account"
  assert_scratch_removed "$dir" "belong account"
}

test_a_project_naming_belong_is_refused() {
  local dir=$work/belongproject
  prepare "$dir"
  python3 - "$dir/json/doctor.json" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, "r", encoding="utf-8") as fh:
    doc = json.load(fh)
doc["project"]["gcp_project"] = "belong-prod"
with open(path, "w", encoding="utf-8") as fh:
    json.dump(doc, fh)
PY
  run_check "$dir"
  expect_status "$status" 1 "belong project"
  expect_contains "$err" "belongs to belong-prod" "belong project: the refusal"
  assert_no_launch "$dir" "belong project"
  assert_scratch_removed "$dir" "belong project"
}

test_a_build_older_than_the_release_is_refused() {
  local dir=$work/old
  prepare "$dir"
  run_check "$dir" FAKE_MODE=old-version
  expect_status "$status" 1 "old build"
  expect_contains "$out" "FACT release: FAIL" "old build: the row"
  expect_contains "$err" "older than 0.24.0" "old build: the refusal"
  assert_no_launch "$dir" "old build"
  assert_scratch_removed "$dir" "old build"
}

test_a_build_without_the_feature_is_refused() {
  local dir=$work/nofeature
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-feature
  expect_status "$status" 1 "no feature"
  expect_contains "$out" "FACT release: FAIL" "no feature: the row"
  expect_contains "$out" "no follow-ups" "no feature: why the probe failed"
  assert_no_launch "$dir" "no feature"
  assert_scratch_removed "$dir" "no feature"
}

test_a_dev_build_is_decided_by_the_feature_probe() {
  local dir=$work/dev
  prepare "$dir"
  run_check "$dir" FAKE_MODE=dev-build
  expect_status "$status" 0 "dev build"
  expect_contains "$out" "FACT release: PASS" "dev build: the row"
  expect_contains "$out" "is not a release number" "dev build: why the probe decides"
}

test_the_parent_pull_request_is_not_merged() {
  local dir=$work/unmerged
  prepare "$dir"
  run_check "$dir" FAKE_MODE=plan-refused
  expect_status "$status" 1 "unmerged parent"
  expect_contains "$out" "FACT parent-plan: FAIL" "unmerged parent: the row"
  expect_contains "$out" "is open, not merged" "unmerged parent: the command's own words"
  expect_contains "$out" "Merge the parent's pull request" "unmerged parent: the remedy"
  expect_contains "$out" "FACT parent-record: FAIL" "unmerged parent: every row below is not reached"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage" "unmerged parent: no record is read without the plan"
  assert_no_launch "$dir" "unmerged parent"
  assert_no_cancel "$dir" "unmerged parent"
  assert_scratch_removed "$dir" "unmerged parent"
}

test_a_plan_that_names_no_run_is_a_finding() {
  local dir=$work/noplan
  prepare "$dir"
  write_plan "$dir" unwritten
  run_check "$dir"
  expect_status "$status" 1 "plan without a run"
  expect_contains "$out" "FACT parent-plan: FAIL" "plan without a run: the row"
  expect_contains "$out" "names no repository" "plan without a run: why"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "storage" "plan without a run: no key could be built"
  assert_no_launch "$dir" "plan without a run"
  assert_scratch_removed "$dir" "plan without a run"
}

test_a_record_that_cannot_be_read_is_a_finding() {
  local dir=$work/norecord
  prepare "$dir"
  run_check "$dir" FAKE_MODE=record-missing
  expect_status "$status" 1 "no record"
  expect_contains "$out" "FACT parent-record: FAIL" "no record: the row"
  expect_contains "$out" "could not be read" "no record: why"
  assert_no_launch "$dir" "no record"
  assert_scratch_removed "$dir" "no record"
}

test_a_record_of_another_run_is_a_finding() {
  local dir=$work/otherrecord
  prepare "$dir"
  python3 - "$dir/json/record.json" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, "r", encoding="utf-8") as fh:
    doc = json.load(fh)
doc["run_id"] = "run-20260101000000-9"
with open(path, "w", encoding="utf-8") as fh:
    json.dump(doc, fh)
PY
  run_check "$dir"
  expect_status "$status" 1 "another run's record"
  expect_contains "$out" "FACT parent-record: FAIL" "another run's record: the row"
  expect_contains "$out" "is another run's" "another run's record: why"
  expect_contains "$out" "FACT groups: FAIL" "another run's record: the grouping is not graded"
  assert_no_launch "$dir" "another run's record"
  assert_scratch_removed "$dir" "another run's record"
}

test_a_parent_that_deferred_nothing_is_a_finding() {
  local dir=$work/nodeferrals
  prepare "$dir"
  write_record "$dir" no-deferrals
  run_check "$dir"
  expect_status "$status" 1 "no deferrals"
  expect_contains "$out" "FACT groups: FAIL" "no deferrals: the row"
  expect_contains "$out" "deferred no finding at all" "no deferrals: why"
  assert_no_launch "$dir" "no deferrals"
  assert_scratch_removed "$dir" "no deferrals"
}

test_a_task_naming_keys_that_are_no_group_is_a_finding() {
  local dir=$work/foreign
  prepare "$dir"
  write_plan "$dir" foreign
  run_check "$dir"
  expect_status "$status" 1 "foreign keys"
  expect_contains "$out" "FACT groups: FAIL" "foreign keys: the row"
  expect_contains "$out" "are no single group of the record" "foreign keys: why"
  assert_no_launch "$dir" "foreign keys"
  assert_scratch_removed "$dir" "foreign keys"
}

test_a_group_split_across_two_tasks_is_a_finding() {
  local dir=$work/split
  prepare "$dir"
  write_plan "$dir" split
  run_check "$dir"
  expect_status "$status" 1 "split group"
  expect_contains "$out" "FACT groups: FAIL" "split group: the row"
  expect_contains "$out" "named by neither a task nor a filed run" "split group: the group the record tied is the one nobody files"
  assert_no_launch "$dir" "split group"
  assert_scratch_removed "$dir" "split group"
}

test_a_group_named_twice_is_a_finding() {
  local dir=$work/double
  prepare "$dir"
  write_plan "$dir" double
  run_check "$dir"
  expect_status "$status" 1 "group twice"
  expect_contains "$out" "FACT groups: FAIL" "group twice: the row"
  expect_contains "$out" "is named twice" "group twice: why"
  assert_no_launch "$dir" "group twice"
  assert_scratch_removed "$dir" "group twice"
}

test_a_group_the_cap_left_behind_is_a_finding() {
  local dir=$work/cap
  prepare "$dir"
  write_plan "$dir" over-cap
  run_check "$dir"
  expect_status "$status" 1 "cap"
  expect_contains "$out" "FACT groups: FAIL" "cap: the row"
  expect_contains "$out" "the cap left 1 group(s) unnamed" "cap: why"
  expect_contains "$out" "raise --max" "cap: the remedy"
  assert_no_launch "$dir" "cap"
  assert_scratch_removed "$dir" "cap"
}

test_a_record_that_carries_fewer_findings_than_it_deferred_is_a_finding() {
  local dir=$work/truncated
  prepare "$dir"
  write_record "$dir" truncated
  write_plan "$dir" truncated
  run_check "$dir"
  expect_status "$status" 1 "truncated record"
  expect_contains "$out" "FACT groups: FAIL" "truncated record: the row"
  expect_contains "$out" "the writer's own cap" "truncated record: why"
  assert_no_launch "$dir" "truncated record"
  assert_scratch_removed "$dir" "truncated record"
}

test_a_launch_that_is_refused_is_a_finding() {
  local dir=$work/launchfails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=launch-refused
  expect_status "$status" 1 "launch refused"
  expect_contains "$out" "FACT launched: FAIL" "launch refused: the row"
  expect_contains "$out" "was refused" "launch refused: why"
  expect_contains "$out" "leaves the ones before it filed and running" "launch refused: what the owner has to go and check"
  expect_contains "$out" "FACT debt-list: FAIL" "launch refused: the rows below are not measured"
  expect_contains "$out" "there is no filed run whose ID this check was given" "launch refused: why they are not measured"
  expect_contains "$out" "FACT cancelled: FAIL" "launch refused: a run the command filed and did not name cannot be cancelled"
  expect_contains "$out" "FACT restore: FAIL" "launch refused: the restore says a run may still be going"
  assert_no_cancel "$dir" "launch refused: a run the command did not name is not this check's to cancel"
  assert_scratch_removed "$dir" "launch refused"
}

test_a_task_filed_without_a_run_is_a_finding() {
  local dir=$work/norunfiled
  prepare "$dir"
  write_launch "$dir" no-run
  run_check "$dir"
  expect_status "$status" 1 "task without a run"
  expect_contains "$out" "FACT launched: FAIL" "task without a run: the row"
  expect_contains "$out" "without a run ID" "task without a run: why"
  expect_contains "$out" "FACT debt-list: FAIL" "task without a run: there is no run ID for a listing to be read against"
  expect_contains "$out" "FACT cancelled: FAIL" "task without a run: the file it started cannot be cancelled"
  assert_no_cancel "$dir" "task without a run"
  assert_scratch_removed "$dir" "task without a run"
}

test_a_listing_that_leaves_a_run_out_is_a_finding() {
  local dir=$work/debtmissing
  prepare "$dir"
  write_debt "$dir" empty
  run_check "$dir"
  expect_status "$status" 1 "listing leaves the run out"
  expect_contains "$out" "FACT debt-list: FAIL" "listing leaves the run out: the row"
  expect_contains "$out" "not listed: $filed_run" "listing leaves the run out: why"
  assert_scratch_removed "$dir" "listing leaves the run out"
}

test_a_listing_row_no_deferral_filed_is_a_finding() {
  local dir=$work/debtextra
  prepare "$dir"
  write_debt "$dir" extra
  run_check "$dir"
  expect_status "$status" 1 "listing carries an ordinary run"
  expect_contains "$out" "FACT debt-list: FAIL" "listing carries an ordinary run: the row"
  expect_contains "$out" "no parent deferral" "listing carries an ordinary run: why"
  assert_scratch_removed "$dir" "listing carries an ordinary run"
}

test_a_listing_that_names_another_parent_is_a_finding() {
  local dir=$work/debtparent
  prepare "$dir"
  write_debt "$dir" wrong-parent
  run_check "$dir"
  expect_status "$status" 1 "listing names another parent"
  expect_contains "$out" "FACT debt-list: FAIL" "listing names another parent: the row"
  expect_contains "$out" "names parent run-20261113090000-9" "listing names another parent: why"
  assert_scratch_removed "$dir" "listing names another parent"
}

test_a_listing_with_other_keys_is_a_finding() {
  local dir=$work/debtkeys
  prepare "$dir"
  write_debt "$dir" wrong-keys
  run_check "$dir"
  expect_status "$status" 1 "listing with other keys"
  expect_contains "$out" "FACT debt-list: FAIL" "listing with other keys: the row"
  expect_contains "$out" "carries keys $finding_key3" "listing with other keys: why"
  assert_scratch_removed "$dir" "listing with other keys"
}

test_a_task_without_the_debt_mark_is_a_finding() {
  local dir=$work/notdebt
  prepare "$dir"
  write_task "$dir" "$filed_run" not-debt
  run_check "$dir"
  expect_status "$status" 1 "task without the mark"
  expect_contains "$out" "FACT debt-task: FAIL" "task without the mark: the row"
  expect_contains "$out" "debt=false" "task without the mark: why"
  assert_scratch_removed "$dir" "task without the mark"
}

test_a_task_naming_another_parent_is_a_finding() {
  local dir=$work/taskparent
  prepare "$dir"
  write_task "$dir" "$filed_run" wrong-parent
  run_check "$dir"
  expect_status "$status" 1 "task names another parent"
  expect_contains "$out" "FACT debt-task: FAIL" "task names another parent: the row"
  expect_contains "$out" "follow_up_of.run=run-20261113090000-9" "task names another parent: why"
  assert_scratch_removed "$dir" "task names another parent"
}

test_a_task_with_other_keys_is_a_finding() {
  local dir=$work/taskkeys
  prepare "$dir"
  write_task "$dir" "$filed_run" wrong-keys
  run_check "$dir"
  expect_status "$status" 1 "task with other keys"
  expect_contains "$out" "FACT debt-task: FAIL" "task with other keys: the row"
  expect_contains "$out" "follow_up_of.keys=$finding_key3" "task with other keys: why"
  assert_scratch_removed "$dir" "task with other keys"
}

test_a_task_that_is_not_the_run_it_is_stored_under_is_a_finding() {
  local dir=$work/taskrun
  prepare "$dir"
  write_task "$dir" "$filed_run" wrong-run
  run_check "$dir"
  expect_status "$status" 1 "task of another run"
  expect_contains "$out" "FACT debt-task: FAIL" "task of another run: the row"
  expect_contains "$out" "the task under its key records" "task of another run: why"
  assert_scratch_removed "$dir" "task of another run"
}

test_a_task_at_another_mode_is_a_finding() {
  local dir=$work/taskmode
  prepare "$dir"
  write_task "$dir" "$filed_run" other-mode
  run_check "$dir"
  expect_status "$status" 1 "task at another mode"
  expect_contains "$out" "FACT mode: FAIL" "task at another mode: the row"
  expect_contains "$out" "carries 'pro'" "task at another mode: why"
  assert_scratch_removed "$dir" "task at another mode"
}

test_a_task_with_no_mode_is_a_finding() {
  local dir=$work/tasknomode
  prepare "$dir"
  write_task "$dir" "$filed_run" no-mode
  run_check "$dir"
  expect_status "$status" 1 "task with no mode"
  expect_contains "$out" "FACT mode: FAIL" "task with no mode: the row"
  expect_contains "$out" "carries 'none'" "task with no mode: why"
  assert_scratch_removed "$dir" "task with no mode"
}

test_a_parent_whose_mode_a_task_cannot_carry_is_a_finding() {
  local dir=$work/hotfix
  prepare "$dir"
  write_record "$dir" hotfix
  run_check "$dir"
  expect_status "$status" 1 "hotfix parent"
  expect_contains "$out" "FACT mode: FAIL" "hotfix parent: the row"
  expect_contains "$out" "resolved no mode this task could carry" "hotfix parent: why"
  assert_scratch_removed "$dir" "hotfix parent"
}

test_a_second_call_that_would_file_again_is_a_finding() {
  local dir=$work/again
  prepare "$dir"
  write_plan "$dir" again
  run_check "$dir"
  expect_status "$status" 1 "second call files again"
  expect_contains "$out" "FACT idempotent: FAIL" "second call files again: the row"
  expect_contains "$out" "would file 1 task(s) again" "second call files again: why"
  assert_scratch_removed "$dir" "second call files again"
}

test_a_second_plan_that_leaves_a_group_is_a_finding() {
  local dir=$work/leftone
  prepare "$dir"
  write_plan "$dir" left-one
  run_check "$dir"
  expect_status "$status" 1 "second plan leaves a group"
  expect_contains "$out" "FACT idempotent: FAIL" "second plan leaves a group: the row"
  expect_contains "$out" "still leaves 1 group(s) unnamed" "second plan leaves a group: why"
  assert_scratch_removed "$dir" "second plan leaves a group"
}

test_nothing_left_to_file_files_nothing_and_cancels_nothing() {
  local dir=$work/filed
  prepare "$dir"
  write_plan "$dir" filed
  run_check "$dir"
  expect_status "$status" 0 "nothing left to file"
  expect_contains "$out" "FACT launched: PASS" "nothing left to file: the row"
  expect_contains "$out" "the plan has nothing to file" "nothing left to file: why"
  expect_contains "$out" "FACT debt-list: PASS" "nothing left to file: the already-filed run is read"
  expect_contains "$out" "FACT mode: PASS" "nothing left to file: its mode is read"
  expect_contains "$out" "FACT idempotent: PASS" "nothing left to file: nothing would be filed"
  expect_contains "$out" "FACT cancelled: INFO" "nothing left to file: the check cancelled nothing"
  expect_contains "$out" "the runs it did not file are not its own" "nothing left to file: why"
  expect_contains "$out" "This call filed no run of its own" "nothing left to file: the restore says so"
  assert_no_launch "$dir" "nothing left to file"
  assert_no_cancel "$dir" "nothing left to file"
  assert_scratch_removed "$dir" "nothing left to file"
}

test_keep_runs_leaves_the_filed_runs_going() {
  local dir=$work/keepruns
  prepare "$dir"
  run_check "$dir" CHECK491_KEEP_RUNS=1
  expect_status "$status" 0 "keep runs"
  expect_contains "$out" "FACT cancelled: PASS" "keep runs: the row"
  expect_contains "$out" "LEFT GOING" "keep runs: what it did instead"
  expect_contains "$out" "FACT restore: PASS" "keep runs: the restore"
  expect_contains "$out" "CHECK491_KEEP_RUNS is set" "keep runs: the restore names the knob"
  assert_no_cancel "$dir" "keep runs"
  assert_scratch_removed "$dir" "keep runs"
}

test_a_cancel_that_does_not_answer_is_a_finding() {
  local dir=$work/cancelfails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=cancel-fails
  expect_status "$status" 1 "cancel fails"
  expect_contains "$out" "FACT cancelled: FAIL" "cancel fails: the row"
  expect_contains "$out" "did not accept 'fugaro cancel <run> --now'" "cancel fails: why"
  expect_contains "$out" "FACT restore: FAIL" "cancel fails: the restore says the run may still be going"
  assert_scratch_removed "$dir" "cancel fails"
}

test_the_record_and_the_plan_are_read_from_the_checkout() {
  local dir=$work/where
  prepare "$dir"
  run_check "$dir"
  local calls
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_contains "$calls" "(in $dir/sandbox)" "where: every fugaro call is made in the checkout"
  expect_absent "$calls" "(in $dir/other)" "where: nothing is run in another checkout"
}

test_an_unknown_argument_is_refused() {
  local dir=$work/unknown
  prepare "$dir"
  run_check "$dir" -- --nope
  expect_status "$status" 2 "unknown argument"
  expect_contains "$err" "unknown argument: --nope" "unknown argument: the refusal"
}

test_the_grouping_and_the_mode_rows_are_load_bearing() {
  # The happy path is read once more with the fixture bent under it: a plan whose
  # task names the group, and a task whose mode is the parent's, are what the two
  # rows pass on. A case that bent neither would pass whatever the rows said.
  local dir=$work/loadbearing
  prepare "$dir"
  write_plan "$dir" foreign
  run_check "$dir"
  expect_status "$status" 1 "load-bearing: the bent fixture is a finding"
  expect_contains "$out" "FACT groups: FAIL" "load-bearing: the grouping is what passes"
  expect_contains "$out" "$finding_key3" "load-bearing: the foreign key is named where it went wrong"
}

# Sharding, so the Go test that runs this file can run its cases side by side:
# CHECK_TEST_SHARD="i/n" runs every n-th test starting at i (each case works in
# its own directory under $work), CHECK_TEST_ONLY=name runs that one alone.
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
t test_a_missing_run_is_refused
t test_dry_run
t test_happy_path
t test_every_group_is_filed_and_the_second_read_writes_nothing
t test_the_filing_is_billable_and_needs_yes
t test_a_missing_sandbox_is_refused
t test_a_path_that_is_not_a_checkout_stops_the_check
t test_xtrace_refused
t test_a_coding_agents_session_is_refused
t test_an_account_naming_belong_is_refused
t test_a_project_naming_belong_is_refused
t test_a_build_older_than_the_release_is_refused
t test_a_build_without_the_feature_is_refused
t test_a_dev_build_is_decided_by_the_feature_probe
t test_the_parent_pull_request_is_not_merged
t test_a_plan_that_names_no_run_is_a_finding
t test_a_record_that_cannot_be_read_is_a_finding
t test_a_record_of_another_run_is_a_finding
t test_a_parent_that_deferred_nothing_is_a_finding
t test_a_task_naming_keys_that_are_no_group_is_a_finding
t test_a_group_split_across_two_tasks_is_a_finding
t test_a_group_named_twice_is_a_finding
t test_a_group_the_cap_left_behind_is_a_finding
t test_a_record_that_carries_fewer_findings_than_it_deferred_is_a_finding
t test_a_launch_that_is_refused_is_a_finding
t test_a_task_filed_without_a_run_is_a_finding
t test_a_listing_that_leaves_a_run_out_is_a_finding
t test_a_listing_row_no_deferral_filed_is_a_finding
t test_a_listing_that_names_another_parent_is_a_finding
t test_a_listing_with_other_keys_is_a_finding
t test_a_task_without_the_debt_mark_is_a_finding
t test_a_task_naming_another_parent_is_a_finding
t test_a_task_with_other_keys_is_a_finding
t test_a_task_that_is_not_the_run_it_is_stored_under_is_a_finding
t test_a_task_at_another_mode_is_a_finding
t test_a_task_with_no_mode_is_a_finding
t test_a_parent_whose_mode_a_task_cannot_carry_is_a_finding
t test_a_second_call_that_would_file_again_is_a_finding
t test_a_second_plan_that_leaves_a_group_is_a_finding
t test_nothing_left_to_file_files_nothing_and_cancels_nothing
t test_keep_runs_leaves_the_filed_runs_going
t test_a_cancel_that_does_not_answer_is_a_finding
t test_the_record_and_the_plan_are_read_from_the_checkout
t test_an_unknown_argument_is_refused
t test_the_grouping_and_the_mode_rows_are_load_bearing

if [ "$failures" -ne 0 ]; then
  echo "check491_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check491_test.sh: ok"
