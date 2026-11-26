#!/usr/bin/env bash
# Tests for scripts/live-checks/check212.sh, with fake `fugaro`, `gcloud`, `git`
# and `gh` on PATH: NOTHING here reaches a cloud, a project, a bucket, a forge or
# a run, no agent runs a live command, and the fakes record every call they are
# given — in which directory it was made — so a case can assert what the check did
# and did not do.
#
# The fakes are a small model of one sandbox machine: the fake `fugaro` answers
# version, `ab --help`, `recipes ls --json`, `doctor --json`, `config layer
# --json`, `models ls --json`, the launches (each one a counter, so the run IDs
# are the case's), the one judge launch and the runs list, and it really writes
# the project's layer file — `settings set layer.ab --add-entry` edits it and
# publishes it in the same call, `config publish` publishes the file it is
# handed — which is what the check's own setup writes and its Restore puts back;
# the fake `gcloud`
# serves a record per run ID and the judge's verdict object out of the case's own
# directory; the fake `git` holds a branch list the case writes, answers the
# repository's own origin, and really deletes from the list; the fake `gh` holds
# pull request states it really moves from OPEN to CLOSED — so the teardown's own
# read-backs are readings of state that changed, not of a log.
#
# The cases that matter are the ones where that model is bent: a build older than
# the release, a build without the `ab` command, a build whose catalog carries no
# judge recipe, a layer with no `ab:` block at all, a rate of zero, no challenger,
# no judge model (the check sets those four up rather than reporting them), a
# project with no layer file to write in, a project whose file is not what it
# published, a repository that opted out, a challenger on the direct route, a
# challenger whose provider route does not admit the repository, a challenger no
# route serves, no pair in the bound, an arm that never ends, a shadow that
# published, a record with no wall times, a cap the two arms do not share, a cost
# above the cap, a judge run that is refused, a judge run that publishes, a
# verdict object that is missing, that skipped the pair, that names another judge
# model or that carries one pass, a report with no pair for the challenger, a
# report that draws a conclusion, and — of the Restore itself — a launch still
# going when the check gives up and a teardown that cannot close what it opened.
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
  # The block's own rows are what a red case is read against, and the exit
  # status alone does not say which row disagreed.
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1; block: $(printf '%s' "${out:-}" | grep '^FACT' | tr '\n' '|')"
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
CASE_TIMEOUT=${CHECK212_TEST_TIMEOUT:-60}

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
# The project's own name, as `fugaro doctor --json` reports it and as its layer
# file is called (D830): the file this check writes the premise into.
project_name=chk212
# The repository's slug, as `fugaro runs ls --json` carries it in a row's `run`
# ref: ONE path segment, where the launch names the repository as `owner/name`.
# The record's object name is built from this one, so a case that reads the key
# back is reading the check's own choice.
slug=acme-sandbox-9f8e7d6c
# The models the fixtures name: the champion the installation is configured with,
# the provider model the layer routes as the challenger, and the judge model the
# layer names. The champion is on the direct route and the challenger is not,
# which is the whole of what check 212's route row reads.
champion_model=claude-opus-4-1
challenger_model=deepseek-v3.2
judge_model=claude-sonnet-5
# The run IDs the fake launcher hands out, by launch number: the check's own
# counter is what names them, a challenger's is its champion's with a `b`, and
# the judge run's is its own.
run_of() { printf 'chk212-run-%s' "$1"; }
challenger_of() { printf 'chk212-run-%sb' "$1"; }
judge_run=chk212-judge-1

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check212.sh's test only. It
# answers the account read the check makes, serves ONE run record per run ID out
# of the case's own directory (so the two arms' records are two documents), and
# serves the judge run's own verdict object. Nothing here reaches a real project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}

# The run ID out of a runs/<slug>/<id>/... object name: the segment after the
# slug, which is what the record and the verdict object are keyed by.
run_of_url() {
  printf '%s' "$1" | awk -F/ '{ s = 0; for (i = 1; i <= NF; i++) if ($i == "runs") s = i; if (s) print $(s + 2) }'
}

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
  case "${2:-}" in
  cat)
    url=${3:-}
    id=$(run_of_url "$url")
    if [ "$mode" = record-unreadable ]; then
      printf 'not a document at all\n'
      exit 0
    fi
    case "$url" in
    */ab/verdicts.json)
      if [ "$mode" = verdicts-missing ] || [ ! -f "$FAKE_JSON_DIR/verdicts.json" ]; then
        echo "ERROR: (gcloud.storage.cat) NotFound: No such object: $url" >&2
        exit 1
      fi
      cat "$FAKE_JSON_DIR/verdicts.json"
      exit 0
      ;;
    esac
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
# A stand-in for `fugaro`, for scripts/live-checks/check212.sh's test only. The
# `--json` reads come from files the case wrote, `ab --help` prints the usage
# line the real command prints (the check's offline feature probe), the launches
# are a counter the case reads back, the judge launch is its own single run,
# `cancel` really ends the run it names (the teardown's read-back is what sees
# it), and every call is logged with the directory it was made in.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}

# ended_yes ID: whether a run the fake `cancel` stopped reads as ended. The
# cancel really changes what the runs list says, which is what makes the
# teardown's own read-back a reading of state that changed.
ended_yes() {
  [ -f "$FAKE_JSON_DIR/ended" ] && grep -qx "$1" "$FAKE_JSON_DIR/ended"
}

# The rows `fugaro runs ls` answers with: one per launch the fake launcher made,
# plus the challenger's own row for a launch that sampled a pair, plus the judge
# run's row once it has been launched. The mode bends the shape a case is about.
rows_doc() {
  local want=$1 id batch arm armid pr branch outcome terminal status runref
  local first=1
  printf '{"project":"%s","runs":[' "$FAKE_PROJECT"
  while read -r id batch; do
    [ -n "$id" ] || continue
    for arm in champion challenger; do
      armid=$id
      if [ "$arm" = challenger ]; then
        if [ "$batch" = "-" ]; then continue; fi
        armid="${id}b"
      fi
      if [ -n "$want" ] && [ "$batch" != "$want" ]; then continue; fi
      if [ -f "$FAKE_JSON_DIR/omit" ] && grep -qx "$armid" "$FAKE_JSON_DIR/omit"; then continue; fi
      terminal=true
      status=succeeded
      branch="fugaro/$armid"
      outcome=pr
      pr=41
      if [ "$arm" = challenger ]; then
        branch=""
        outcome=none
        pr=0
      fi
      case "$mode" in
      # An arm that does not end on its own — and that the teardown's cancel does
      # end, which is what the run's own row reads afterwards.
      arms-not-ended)
        if [ "$arm" = challenger ] && ! ended_yes "$armid"; then
          terminal=false
          status=running
        fi
        ;;
      # A launch in flight: the run has not ended, and it reads as ended only once
      # something stops it. `rows-running-stuck` is the run nothing stops.
      rows-running | rows-running-stuck)
        if [ "$mode" = rows-running-stuck ] || ! ended_yes "$armid"; then
          terminal=false
          status=running
        fi
        ;;
      esac
      runref="$FAKE_SLUG/$armid"
      # The record's key is read out of this ref, so a row whose ref names another
      # run is the one shape that would make the check read a record that is not
      # its arm's.
      if [ "$mode" = arms-renamed ]; then runref="$FAKE_SLUG/$armid-elsewhere"; fi
      [ "$first" = 1 ] || printf ','
      first=0
      printf '{"run":"%s","repo":"acme/sandbox","run_id":"%s","status":"%s","stage":"finalize","terminal":%s,"settled":%s,"outcome":"%s","branch":"%s","pr":%s,"batch":"%s"}' \
        "$runref" "$armid" "$status" "$terminal" "$terminal" "$outcome" "$branch" "$pr" "$batch"
    done
    if [ "$mode" = batch-extra ] && [ "$batch" = "$want" ]; then
      printf ',{"run":"%s/%s-extra","repo":"acme/sandbox","run_id":"%s-extra","status":"succeeded","stage":"finalize","terminal":true,"settled":true,"outcome":"none","branch":"","pr":0,"batch":"%s"}' \
        "$FAKE_SLUG" "$id" "$id" "$batch"
    fi
  done <"$FAKE_JSON_DIR/launched"
  if [ -f "$FAKE_JSON_DIR/judge-launched" ]; then
    local jid
    jid=$(head -n 1 "$FAKE_JSON_DIR/judge-launched")
    terminal=true
    status=succeeded
    if [ "$mode" = judge-not-ended ] && ! ended_yes "$jid"; then
      terminal=false
      status=running
    fi
    if [ "$first" = 1 ]; then first=0; else printf ','; fi
    printf '{"run":"%s/%s","repo":"acme/sandbox","run_id":"%s","status":"%s","stage":"finalize","terminal":%s,"settled":%s,"outcome":"none","branch":"","pr":0,"batch":"%s"}' \
      "$FAKE_SLUG" "$jid" "$jid" "$status" "$terminal" "$terminal" "ab-judge"
  fi
  printf '],"totals":{},"warnings":[]}\n'
}

# layer_file: the project's own layer file, by the rule localcfg.LayerFile
# applies (D830) — the same path the check's setup writes and the Restore puts
# back, read here from the project the doctor document names.
layer_file() {
  local name
  name=$(python3 -c 'import json,sys; print((json.load(open(sys.argv[1])).get("project") or {}).get("name") or "")' \
    "$FAKE_JSON_DIR/doctor.json" 2>/dev/null || true)
  printf '%s/fugaro/projects/%s.layer.yaml\n' "${XDG_CONFIG_HOME:-$HOME/.config}" "$name"
}

# publish_layer FILE: `fugaro config publish`. The fake really publishes the
# file it is handed: the layer the project serves becomes that file's bytes,
# sha256 and all, so the check's own read after its write is a reading of state
# that changed.
publish_layer() { # publish_layer FILE
  local file=$1
  if [ ! -f "$file" ]; then
    printf 'fugaro: no layer file at %s\n' "$file" >&2
    return 1
  fi
  local n
  n=$(cat "$FAKE_JSON_DIR/publish-count" 2>/dev/null || echo 0)
  n=$((n + 1))
  printf '%s\n' "$n" >"$FAKE_JSON_DIR/publish-count"
  # A publish that fails while the layer stays where it was: every publish is
  # numbered, so a case can fail the setup's own or the Restore's and leave the
  # rest answering.
  if [ -f "$FAKE_JSON_DIR/publish-fail-at" ] && grep -qx "$n" "$FAKE_JSON_DIR/publish-fail-at"; then
    printf 'fugaro: the layer could not be published\n' >&2
    return 1
  fi
  python3 - "$file" "$FAKE_JSON_DIR/layer.json" "$FAKE_PROJECT" <<'PY'
import hashlib
import json
import sys

path, out, project = sys.argv[1], sys.argv[2], sys.argv[3]
raw = open(path, "rb").read()
generation = 7
try:
    generation = int(json.load(open(out)).get("generation") or 7) + 1
except Exception:
    pass
doc = {
    "where": "gs://fugaro-config-%s/projects/%s/layer.yaml" % (project, project),
    "generation": generation,
    "sha256": hashlib.sha256(raw).hexdigest(),
    "yaml": raw.decode("utf-8"),
}
json.dump(doc, open(out, "w"))
PY
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
  # `ab --help` is the check's offline feature probe, so the fake answers it with
  # the usage line the real command prints rather than with the report — and
  # before the modes below, because a build whose report fails still has the
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
  ab-unreadable)
    printf 'not a document at all\n'
    exit 0
    ;;
  esac
  cat "$FAKE_JSON_DIR/ab.json"
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
  esac
  cat "$FAKE_JSON_DIR/recipes.json"
  ;;
models)
  case "$mode" in
  models-fails)
    printf 'fugaro: the model table could not be read\n' >&2
    exit 1
    ;;
  models-unreadable)
    printf 'not a document at all\n'
    exit 0
    ;;
  esac
  cat "$FAKE_JSON_DIR/models.json"
  ;;
settings)
  # `settings set layer.ab --add-entry <member>=<value> --layer project`: the one
  # supported writer of a member of the layer's `ab` block. The fake writes the
  # member into the layer file — creating the block where the file has none, as
  # the real command does — and publishes the file in the same call, because the
  # write's whole point is that the layer stage runs behind it.
  [ "${2:-}" = "set" ] || {
    echo "fugaro: unexpected settings subcommand ${2:-}" >&2
    exit 1
  }
  [ "${3:-}" = "layer.ab" ] || {
    echo "fugaro: unexpected settings key ${3:-}" >&2
    exit 1
  }
  entry=""
  layer=""
  prev=""
  for a in "$@"; do
    case "$prev" in
    --add-entry) entry=$a ;;
    --layer) layer=$a ;;
    esac
    prev=$a
  done
  member=${entry%%=*}
  value=${entry#*=}
  case "$member" in
  rate | judge_model) ;;
  *)
    echo "fugaro: settings set layer.ab takes --add-entry rate=<a rate> or judge_model=<a model>" >&2
    exit 1
    ;;
  esac
  [ "$value" != "$entry" ] && [ -n "$value" ] || {
    echo "fugaro: settings set layer.ab needs --add-entry ${member}=<a value>" >&2
    exit 1
  }
  [ "$layer" = "project" ] || {
    echo "fugaro: settings set layer.ab needs --layer project" >&2
    exit 1
  }
  case "$mode" in
  settings-fails)
    printf 'fugaro: the layer was not written\n' >&2
    exit 1
    ;;
  esac
  file=$(layer_file)
  [ -f "$file" ] || {
    printf 'fugaro: no local project layer file at %s\n' "$file" >&2
    exit 1
  }
  python3 - "$file" "$member" "$value" <<'PY'
import re
import sys

path, member, value = sys.argv[1], sys.argv[2], sys.argv[3]
lines = open(path, encoding="utf-8").read().split("\n")
start = None
for i, line in enumerate(lines):
    if re.match(r"^ab\s*:", line):
        start = i
        break
if start is None:
    end = len(lines)
    while end and not lines[end - 1].strip():
        end -= 1
    lines[end:end] = ["ab:", "  %s: %s" % (member, value)]
    open(path, "w", encoding="utf-8").write("\n".join(lines))
    sys.exit(0)
rest = lines[start].split(":", 1)[1].strip()
if rest.startswith("{") and rest.endswith("}"):
    body = re.sub(r"%s\s*:\s*[^,}]*" % member, "", rest[1:-1])
    items = [p.strip() for p in body.split(",") if p.strip()]
    lines[start] = "ab: {%s}" % ", ".join(["%s: %s" % (member, value)] + items)
    open(path, "w", encoding="utf-8").write("\n".join(lines))
    sys.exit(0)
i = start + 1
while i < len(lines) and (not lines[i].strip() or lines[i][:1].isspace()):
    if re.match(r"^\s*%s\s*:" % member, lines[i]):
        lines[i] = "  %s: %s" % (member, value)
        open(path, "w", encoding="utf-8").write("\n".join(lines))
        sys.exit(0)
    i += 1
lines[i:i] = ["  %s: %s" % (member, value)]
open(path, "w", encoding="utf-8").write("\n".join(lines))
PY
  publish_layer "$file"
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
  esac
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
  elif [ "${2:-}" = "publish" ]; then
    # The publish the check ends its own write on. The fake really publishes:
    # the file it is given becomes the layer the project serves, which is what
    # makes the check's own re-read a reading of state that changed.
    [ -n "${3:-}" ] || {
      printf 'fugaro: config publish needs a file\n' >&2
      exit 1
    }
    publish_layer "${3}"
  else
    echo "fugaro: unexpected config subcommand ${2:-}" >&2
    exit 1
  fi
  ;;
run)
  recipe=""
  prev=""
  for a in "$@"; do
    if [ "$prev" = "--recipe" ]; then recipe=$a; fi
    prev=$a
  done
  if [ "$recipe" = "ab-judge" ]; then
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
  fi
  if [ "$recipe" = "ab-judge" ]; then
    case "$mode" in
    judge-fails)
      printf 'fugaro: a judge run for this repository has not finished; one judge run per repository at a time\n' >&2
      exit 1
      ;;
    judge-hangs)
      sleep 300
      ;;
    judge-nameless)
      printf '{"project":"%s","run":"","repo":"acme/sandbox","run_id":"","branch":"","status":"launched"}\n' "$FAKE_PROJECT"
      exit 0
      ;;
    esac
    printf '%s\n' "$FAKE_JUDGE_RUN" >>"$FAKE_JSON_DIR/judge-launched"
    printf '{"project":"%s","run":"%s/%s","repo":"acme/sandbox","run_id":"%s","branch":"","execution":"exec","status":"launched","recipe":{"name":"ab-judge","source":"catalog"}}\n' \
      "$FAKE_PROJECT" "$FAKE_SLUG" "$FAKE_JUDGE_RUN" "$FAKE_JUDGE_RUN"
    exit 0
  fi
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
  id="chk212-run-${n}"
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

# The fake `git` holds the remote's branch list in a file the case writes,
# answers the checkout's own origin and really deletes from the list, so the
# teardown's own read-back is a reading of state that changed.
write_fake_git() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/git" <<'GIT_EOF'
#!/usr/bin/env bash
# A stand-in for `git`, for scripts/live-checks/check212.sh's test only. It
# answers the three calls the check makes: the repository's own origin (which is
# the owner/name the layer's allow-lists are matched against), the remote's
# branch list, and the delete. Nothing here reaches a real remote.
set -euo pipefail
printf 'git %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/git.calls"
mode=${FAKE_MODE:-default}
branches="$FAKE_JSON_DIR/branches"

case "${1:-}" in
remote)
  if [ "$mode" = remote-url-fails ]; then
    printf 'fatal: not a git repository\n' >&2
    exit 128
  fi
  # The two shapes a checkout's origin carries, so a case can pick either: the
  # SCPlike one is the default and a https one is the other.
  if [ -f "$FAKE_JSON_DIR/origin-https" ]; then
    printf 'https://github.com/acme/sandbox.git\n'
  else
    printf 'git@github.com:acme/sandbox.git\n'
  fi
  ;;
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
# A stand-in for `gh`, for scripts/live-checks/check212.sh's test only. It holds
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
# write_recipes DIR [SHAPE]: the catalog `fugaro recipes ls --json` answers
# with. SHAPE is `ok`, `missing` (no ab-judge row at all) or `not-launchable`.
write_recipes() { # write_recipes DIR [SHAPE]
  local dir=$1 shape=${2:-ok} rows
  case "$shape" in
  missing)
    rows='[{"name":"default","source":"catalog","launchable":true},{"name":"standard","source":"catalog","launchable":true}]'
    ;;
  not-launchable)
    rows='[{"name":"default","source":"catalog","launchable":true},{"name":"ab-judge","source":"catalog","launchable":false,"problem":"this build was made without the features this recipe needs"}]'
    ;;
  *)
    rows='[{"name":"default","source":"catalog","launchable":true},{"name":"ab-judge","source":"catalog","launchable":true}]'
    ;;
  esac
  cat >"$dir/json/recipes.json" <<JSON
{"recipes":$rows,"notes":[]}
JSON
}

# write_models DIR [SHAPE]: the project's own model table, `fugaro models ls
# --json`. SHAPE bends the one thing the route row reads — whether the layer's
# challenger is a provider model this repository may use:
#   ok             the challenger is served by the provider route openrouter,
#                  whose allow-list names acme/sandbox
#   open           the same route, open to every repository (allow_data_to ["*"])
#   direct         the challenger is served by the direct Anthropic route
#   not-admitted   the provider route serves it and does not admit this repository
#   unserved       the model row exists and no route serves it
#   absent         no model row for the challenger at all
#   coder          the provider model is the one this repository runs as its own
#                  coder, which a pair may not name as the challenger
write_models() { # write_models DIR [SHAPE]
  local dir=$1 shape=${2:-ok} route=openrouter allow='"acme/sandbox"' models_extra present=yes coder=$champion_model
  case "$shape" in
  open) allow='"*"' ;;
  direct)
    route=direct
    allow='"*"'
    ;;
  not-admitted) allow='"other/repo"' ;;
  unserved)
    route=""
    allow=""
    ;;
  absent) present=no ;;
  coder) coder=$challenger_model ;;
  esac
  models_extra=""
  if [ "$present" = yes ]; then
    models_extra=",{\"id\":\"$challenger_model\",\"route\":\"$route\",\"source\":\"catalog\",\"input_per_m\":0.28,\"output_per_m\":0.42,\"allowed_to\":[${allow}]}"
  fi
  cat >"$dir/json/models.json" <<JSON
{"version":1,"project":"$project_name","catalog":{"source":"catalog","checked_at":"2026-11-01","age_days":3,"stale":false},"routes":[{"name":"direct","kind":"anthropic","base_url":"https://api.anthropic.com","host":"api.anthropic.com","models":["$champion_model"],"allow_data_to":["*"],"open_to_every_repo":true},{"name":"openrouter","kind":"openrouter","base_url":"https://openrouter.ai/api/v1","host":"openrouter.ai","models":["$challenger_model"],"route_fee_pct":5,"allow_data_to":[${allow}],"open_to_every_repo":false}],"models":[{"id":"$champion_model","route":"direct","source":"embedded","allowed_to":["*"]}${models_extra}],"repositories":[{"name":"acme/sandbox","state":"ok","coder":"$coder"}]}
JSON
}

# layer_text STYLE RATE CHALLENGERS JUDGE MAX: the project layer's own file,
# without the newline a file ends with. STYLE is `block` or `flow` (the two
# shapes this check's YAML reader has to read), RATE or JUDGE may be `none` to
# drop the key, and CHALLENGERS is a comma-separated list ("" is an empty list).
layer_text() { # layer_text STYLE RATE CHALLENGERS JUDGE MAX
  local style=$1 rate=$2 challengers=$3 judge=$4 max=${5:-5} items="" c
  if [ "$style" = "flow" ]; then
    local flow_judge=""
    [ "$judge" = none ] || flow_judge=", judge_model: $judge"
    local flow_rate=""
    [ "$rate" = none ] || flow_rate="rate: $rate, "
    printf 'agent:\n  models:\n    coder: %s\nab: {%schallengers: [%s]%s, max_pairs_per_day: %s}\nworkflow: agent' \
      "$champion_model" "$flow_rate" "$challengers" "$flow_judge" "$max"
    return 0
  fi
  local IFS=,
  for c in $challengers; do
    [ -n "$c" ] && items="${items}    - ${c}"$'\n'
  done
  unset IFS
  printf 'agent:\n  models:\n    coder: %s\nab:\n' "$champion_model"
  [ "$rate" = none ] || printf '  rate: %s\n' "$rate"
  if [ -z "$items" ]; then
    printf '  challengers: []\n'
  else
    printf '  challengers:\n%s' "$items"
  fi
  [ "$judge" = none ] || printf '  judge_model: %s\n' "$judge"
  printf '  max_pairs_per_day: %s\nworkflow: agent' "$max"
}

# layer_file_path DIR: the project's own layer file, at the path localcfg
# .LayerFile names (D830) for the project the doctor document names.
layer_file_path() { # layer_file_path DIR
  printf '%s/home/.config/fugaro/projects/%s.layer.yaml\n' "$1" "$project_name"
}

# layer_json DIR TEXT: the document `fugaro config layer --json` answers with for
# a layer file holding TEXT — its `yaml` the file's own bytes and its `sha256`
# the file's own sum, which is what the setup compares the file against before it
# writes. The generation moves with every publish, as the real one does.
layer_json() { # layer_json DIR TEXT
  python3 - "$1/json/layer.json" "$project" "$2" <<'PY'
import hashlib
import json
import sys

out, project, text = sys.argv[1], sys.argv[2], sys.argv[3] + "\n"
generation = 7
try:
    generation = int(json.load(open(out)).get("generation") or 7) + 1
except Exception:
    pass
doc = {
    "where": "gs://fugaro-config-%s/projects/%s/layer.yaml" % (project, project),
    "generation": generation,
    "sha256": hashlib.sha256(text.encode("utf-8")).hexdigest(),
    "yaml": text,
}
json.dump(doc, open(out, "w"))
PY
}

# write_layer_file DIR TEXT: the project's own layer file, holding TEXT plus the
# newline a file ends with.
write_layer_file() { # write_layer_file DIR TEXT
  mkdir -p "$1/home/.config/fugaro/projects"
  printf '%s\n' "$2" >"$(layer_file_path "$1")"
}

# write_layer DIR STYLE RATE CHALLENGERS JUDGE [MAX]: the published project layer
# and the layer file it was published from, byte for byte — the document `fugaro
# config layer --json` hands over and the file the setup writes and the Restore
# puts back. CHALLENGERS "" is a block with an empty list; RATE or JUDGE "none"
# drops the key.
write_layer() { # write_layer DIR STYLE RATE CHALLENGERS JUDGE [MAX]
  local dir=$1 style=$2 rate=$3 challengers=$4 judge=$5 max=${6:-5} text
  text=$(layer_text "$style" "$rate" "$challengers" "$judge" "$max")
  write_layer_file "$dir" "$text"
  layer_json "$dir" "$text"
}

# write_layer_without_ab DIR: a project whose layer carries no `ab:` block at all,
# file and document alike — the shape the check sets the sampler up in, and the
# state the owner's project is in.
write_layer_without_ab() { # write_layer_without_ab DIR
  local text
  text=$(printf 'agent:\n  models:\n    coder: %s\nworkflow: agent' "$champion_model")
  write_layer_file "$1" "$text"
  layer_json "$1" "$text"
}

write_doctor() { # write_doctor DIR
  cat >"$1/json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[],"project":{"name":"$project_name","gcp_project":"$project","region":"us-east5"}}
JSON
}

# write_champion DIR ID [START] [FINISH] [COST] [CAP]: the champion's own record
# — the arm that publishes as an unsampled run does, plus the pair block A3.3
# writes and the wall times and cost U4 and D567 are read from.
write_champion() { # write_champion DIR ID [START] [FINISH] [COST] [CAP]
  local id=$2 start=${3:-2026-11-14T10:00:00.123456789Z} finish=${4:-2026-11-14T10:12:30.987654321Z} cost=${5:-1.25} cap=${6:-5}
  cat >"$1/json/records/$id.json" <<JSON
{"version":1,"run_id":"$id","repo":"acme/sandbox","workflow":"agent","branch":"fugaro/$id","status":"succeeded","stage":"finalize","outcome":"pr","pr":{"number":41,"url":"https://example.invalid/acme/sandbox/pull/41"},"patch_object":"runs/$slug/$id/ab/patch.diff","patch_truncated":false,"started_at":"$start","finished_at":"$finish","cost_usd":$cost,"policy":{"effective":{"per_run_usd":$cap,"mode":"enforce"}},"ab":{"pair_id":"pair-$id","arm":"champion"}}
JSON
}

# write_challenger DIR ID [START] [FINISH] [COST] [CAP] [MODEL]: the challenger's
# own record — the shadow's whole claim: the arm, the publish mode, the owner's
# model, no branch, no pull request, outcome none — plus its wall time and cost.
# It carries no `pr` field at all: runstore.Record.PR is a pointer with
# `omitempty`, so a run that opened no pull request has none.
write_challenger() { # write_challenger DIR ID [START] [FINISH] [COST] [CAP] [MODEL]
  local id=$2 start=${3:-2026-11-14T10:00:30.111111111Z} finish=${4:-2026-11-14T10:13:05.222222222Z} cost=${5:-0.9} cap=${6:-5} model=${7:-$challenger_model}
  cat >"$1/json/records/$id.json" <<JSON
{"version":1,"run_id":"$id","repo":"acme/sandbox","workflow":"agent","branch":"","status":"succeeded","stage":"finalize","outcome":"none","patch_object":"runs/$slug/$id/ab/patch.diff","patch_truncated":false,"started_at":"$start","finished_at":"$finish","cost_usd":$cost,"policy":{"effective":{"per_run_usd":$cap,"mode":"enforce"}},"ab":{"pair_id":"pair-${id%b}","arm":"challenger","publish":"none","challenger":"$model","challenger_source":"owner"}}
JSON
}

# write_judge_record DIR ID: the judge run's own record — a mode: evaluate run
# that took no branch and opened no pull request.
write_judge_record() { # write_judge_record DIR ID
  local id=$2
  cat >"$1/json/records/$id.json" <<JSON
{"version":1,"run_id":"$id","repo":"acme/sandbox","workflow":"ab-judge","branch":"","status":"succeeded","stage":"finalize","outcome":"none","started_at":"2026-11-14T10:20:00.000000000Z","finished_at":"2026-11-14T10:21:00.000000000Z","cost_usd":0.05,"policy":{"effective":{"per_run_usd":5,"mode":"enforce"}}}
JSON
}

# write_verdicts DIR [WINNER] [PAIR] [MODEL] [PASSES]: the object the judge writes
# under its own prefix. PASSES 1 is a verdict read from a single pass, which the
# check refuses.
write_verdicts() { # write_verdicts DIR [WINNER] [PAIR] [MODEL] [PASSES]
  local dir=$1 winner=${2:-challenger} pair=${3:-pair-$(run_of 1)} model=${4:-$judge_model} passes=${5:-2} body
  if [ "$passes" = 2 ]; then
    body='{"swap":true,"label":"a","arm":"challenger","reason":"correctness"},{"swap":false,"label":"a","arm":"challenger","reason":"correctness"}'
  else
    body='{"swap":true,"label":"a","arm":"challenger","reason":"correctness"}'
  fi
  cat >"$dir/json/verdicts.json" <<JSON
{"version":1,"judge_model":"$model","pairs":[{"pair_id":"$pair","winner":"$winner","reason":"correctness","passes":[$body]}]}
JSON
}

# write_ab DIR CHALLENGER PAIRS RULE DECISIVE: `fugaro ab --json`, with one row
# per challenger named and the recommendation the case is about.
write_ab() { # write_ab DIR CHALLENGER PAIRS RULE DECISIVE
  local decision
  decision=keep
  [ "$4" = below-minimum ] && decision=no-conclusion
  cat >"$1/json/ab.json" <<JSON
{"project":"$project_name","source":"run-records","since":"2026-11-13T00:00:00Z","until":"2026-11-14T00:00:00Z","rows":[{"mode":"coder","tier":"standard","champion":"$champion_model","challenger":"$2","pairs":$3,"wins":$3,"losses":0,"ties":0,"champion_halts":0,"challenger_halts":0,"champion_follow_ups":0,"judge_families":["anthropic"],"judge_same_family":false,"unjudged":0,"truncated":false,"recommendation":{"decision":"$decision","rule":"$4","decisive":$5,"p":0.5,"win_share_lo":0.025,"win_share_hi":0.975,"median_cost_ratio":0.72,"exchange_rate":0.1}}],"warnings":[]}
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

# A repository checkout: the check refuses a path that is not one, every command
# is run from the checkout it belongs to, and its own `ab:` block is where a
# repository's opt-out lives.
write_checkout() { # write_checkout DIR [AB_RATE]
  mkdir -p "$1"
  printf 'project: chk212\n' >"$1/fugaro.yaml"
  if [ -n "${2:-}" ]; then
    printf 'ab:\n  rate: %s\n' "$2" >>"$1/fugaro.yaml"
  fi
}

write_docs() { # write_docs DIR
  local dir=$1
  mkdir -p "$dir/json/records"
  write_doctor "$dir"
  write_recipes "$dir"
  write_models "$dir"
  write_layer "$dir" block 0.1 "$challenger_model" "$judge_model"
  write_ab "$dir" "$challenger_model" 1 below-minimum 1
  write_verdicts "$dir"
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
  rm -f "$dir/json/judge-launched"
  FAKE_PAIR_AFTER=$after
  if [ "$after" = 0 ]; then
    write_pair_records "$dir" 1
    write_remote "$dir" "fugaro/$(run_of 1)"
  else
    write_remote "$dir" "fugaro/$(run_of 1)" "fugaro/$(run_of 2)" "fugaro/$(run_of $((after + 1)))"
    write_pair_records "$dir" "$((after + 1))"
    write_verdicts "$dir" challenger "pair-$(run_of "$((after + 1))")"
  fi
  write_judge_record "$dir" "$judge_run"
  write_prs "$dir" 41 OPEN
}

# write_pair_records DIR N: both records of the pair the Nth launch sampled,
# which is what the happy path and most bends start from.
write_pair_records() { # write_pair_records DIR N
  write_champion "$1" "$(run_of "$2")"
  write_challenger "$1" "$(challenger_of "$2")"
}

# --------------------------------------------------------------- running the check
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
  # argument it does not know, and the case that measures that refusal is the only
  # way to trace a run that is refusing to be traced.
  local bashargs=()
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  # The agent markers are dropped from this test's own environment: the check
  # refuses a coding agent's session by name, and the suite that runs this file may
  # itself be started from one. The case that measures that refusal sets the marker
  # again, and a variable given as an argument wins over `env -u`.
  bounded env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    XDG_CONFIG_HOME="$dir/home/.config" \
    FAKE_LOG_DIR="$dir/logs" FAKE_MODE="${FAKE_MODE:-default}" \
    FAKE_JSON_DIR="$dir/json" FAKE_PROJECT="$project" FAKE_SLUG="$slug" \
    FAKE_PAIR_AFTER="${FAKE_PAIR_AFTER:-0}" FAKE_JUDGE_RUN="$judge_run" \
    CHECK212_SANDBOX="$dir/sandbox" CHECK212_YES=1 CHECK212_MAX_LAUNCHES=3 \
    CHECK212_RUN_TIMEOUT_SECONDS=6 CHECK212_POLL_SECONDS=1 \
    CHECK212_CANCEL_TIMEOUT_SECONDS=3 \
    CHECK212_CLI_TIMEOUT_SECONDS=20 CHECK212_LAUNCH_TIMEOUT_SECONDS=20 \
    CHECK212_DOCTOR_TIMEOUT_SECONDS=20 CHECK212_JUDGE_TIMEOUT_SECONDS=20 \
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
# expect_first_line OUT NEEDLE WHAT: the first line of what the check printed.
# What it is about to do to the owner's own project is the one thing that has to
# be readable before anything else.
expect_first_line() { # expect_first_line OUT NEEDLE WHAT
  expect_contains "$(printf '%s\n' "$1" | head -n 1)" "$2" "$3"
}

calls_count() { # calls_count NEEDLE: how many `fugaro` calls carry it
  printf '%s\n' "$calls" | grep -c -- "$1" || true
}

sha256_file() { # sha256_file FILE
  python3 -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$1"
}

published_sha() { # published_sha DIR: the sum `fugaro config layer --json` reads
  python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("sha256") or "")' "$1/json/layer.json"
}

# assert_layer_restored DIR SUM WHAT: the two halves of the Restore's own claim
# about the layer — the file back byte for byte, and the project serving those
# bytes again.
assert_layer_restored() { # assert_layer_restored DIR SUM WHAT
  local got
  got=$(sha256_file "$(layer_file_path "$1")")
  [ "$got" = "$2" ] || fail "$3: the layer file is not back byte for byte: sha256 $got, want $2"
  got=$(published_sha "$1")
  [ "$got" = "$2" ] || fail "$3: the project reads back sha256 $got, not the layer it served before ($2)"
}

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

# setjson FILE EXPR: rewrite one member of a JSON document the case owns, so a
# case bends the one thing it is about rather than restating the whole fixture.
# The expression is given through the environment (quoting a Python body through
# `-c` and a shell string is how a test starts testing its own escaping) and runs
# with `d` bound to the parsed document.
setjson() { # setjson FILE EXPR
  SETJSON_EXPR=$2 python3 -c '
import json, os, sys
d = json.load(open(sys.argv[1]))
exec(os.environ["SETJSON_EXPR"])
json.dump(d, open(sys.argv[1], "w"))' "$1"
}

# write_layer_raw DIR TEXT: the layer file and the document, for the shapes
# layer_text does not make (a `ab:` block whose members are not the ones the check
# reads, a file the case edits itself).
write_layer_raw() { # write_layer_raw DIR TEXT
  write_layer_file "$1" "$2"
  layer_json "$1" "$2"
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
  # The header is what a person reads before running it: it has to say, where they
  # will see it, that the launches and the judge are billable, that the check is
  # theirs and that the teardown is part of it.
  expect_contains "$out" "up to THIRTY sandbox runs and what their tasks cost" "--help: the cost of the launches is stated"
  expect_contains "$out" "plus ONE" "--help: the judge run's own cost is stated"
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  expect_contains "$out" "closed and deleted by the Restore" "--help: the teardown is part of the check"
  expect_contains "$out" "BOTH WALL TIMES (U4)" "--help: U4 is named"
  assert_no_run "$dir" "--help"
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
  expect_contains "$out" "FACT bound: PASS" "dry run: the spend is stated"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "FACT restore: PASS" "dry run: the restore row"
  expect_contains "$out" "CHECK 212 RESULT END (exit 0)" "dry run: the end line"
  # A row the plan did not measure reads INFO, never PASS: a plan is not a
  # reading, and a block that said PASS would be one nobody took.
  expect_contains "$out" "FACT layer: INFO — planned" "dry run: a planned row is INFO"
  expect_contains "$out" "FACT judge-recipe: INFO — planned" "dry run: the recipe row is planned too"
  expect_contains "$out" "FACT judge-run: INFO — planned" "dry run: the judge row is planned too"
  expect_contains "$err" "up to 3 sandbox runs" "dry run: the plan names the spend"
  expect_absent "$calls" "doctor" "dry run: the installation is not read"
  expect_absent "$calls" "recipes ls" "dry run: the catalog is not read"
  expect_absent "$calls" "config layer" "dry run: the layer is not read"
  expect_absent "$calls" "models ls" "dry run: the model table is not read"
  expect_absent "$gcalls" "storage" "dry run: the bucket is not read"
  expect_absent "$gitcalls" "ls-remote" "dry run: the remote is not read"
  assert_no_run "$dir" "dry run"
  assert_scratch_removed "$dir" "dry run"
}

test_a_doctor_that_exits_1_with_its_document_is_an_answer() {
  local dir=$work/doctorexit1
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-exit1
  expect_status "$status" 0 "a doctor that exits 1 with its document"
  expect_contains "$out" "belongs to ${project}, and its origin is acme/sandbox" "a doctor that exits 1 with its document: the project it read"
}

test_a_doctor_that_does_not_answer_is_not_measured() {
  local dir=$work/doctorfail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-fails
  expect_status "$status" 2 "a doctor that does not answer"
  expect_contains "$out" "FACT sandbox: FAIL" "a doctor that does not answer: the row"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "happy path"
  expect_contains "$out" "FACT sandbox: PASS" "happy path: the checkout, its project and its origin"
  expect_contains "$out" "belongs to ${project}, and its origin is acme/sandbox" "happy path: what the sandbox row read"
  expect_contains "$out" "FACT layer: PASS" "happy path: the premise"
  expect_contains "$out" "names the judge model ${judge_model}" "happy path: the judge model it read"
  expect_contains "$out" "FACT route: PASS" "happy path: the challenger is a routed provider model"
  expect_contains "$out" "every challenger the layer names is a provider model routed for acme/sandbox" "happy path: what the route row read"
  expect_contains "$out" "${challenger_model} on openrouter (openrouter.ai, allow_data_to acme/sandbox)" "happy path: the route it resolved"
  expect_contains "$out" "FACT pair: PASS" "happy path: the launch that sampled"
  expect_contains "$out" "pair pair-$(run_of 1) on batch ab-pair-$(run_of 1)" "happy path: the pair it sampled"
  expect_contains "$out" "FACT arms: PASS" "happy path: the two arms"
  expect_contains "$out" "FACT champion: PASS" "happy path: the champion's record"
  expect_contains "$out" "FACT challenger: PASS" "happy path: the challenger's record"
  expect_contains "$out" "the same task, at the same commit, on the provider model ${challenger_model} the layer routed for acme/sandbox" "happy path: what the shadow's record says"
  expect_contains "$out" "FACT wall-times: PASS" "happy path: U4's two wall times"
  expect_contains "$out" "champion $(run_of 1) 750.864s" "happy path: the champion's wall time"
  expect_contains "$out" "challenger $(challenger_of 1) on ${challenger_model} 755.111s" "happy path: the challenger's wall time"
  expect_contains "$out" "The challenger's is 1.01x the champion's" "happy path: the ratio U4 is read off"
  expect_contains "$out" "FACT costs: PASS" "happy path: the two costs against the cap"
  expect_contains "$out" "champion $(run_of 1) cost 1.25 of 5" "happy path: the champion's cost"
  expect_contains "$out" "challenger $(challenger_of 1) on ${challenger_model} cost 0.9 of 5" "happy path: the challenger's cost"
  expect_contains "$out" "FACT judge-run: PASS" "happy path: the judge run"
  expect_contains "$out" "the judge run ${judge_run} ran over pair-$(run_of 1) and ended" "happy path: what the judge row read"
  expect_contains "$out" "FACT verdict: PASS" "happy path: the verdict object"
  expect_contains "$out" "carrying the pair pair-$(run_of 1): winner 'challenger'" "happy path: the verdict it read"
  expect_contains "$out" "FACT ab-report: PASS" "happy path: the pair in the report"
  expect_contains "$out" "by the rule below-minimum" "happy path: the rule the report states"
  expect_contains "$out" "CHECK 212 RESULT END (exit 0)" "happy path: the end line"
  assert_scratch_removed "$dir" "happy path"
  # The commands themselves: the launch is made from the checkout, the records are
  # read out of the project's bucket, the judge is launched by hand as the release
  # says it is, and its verdicts are read at its own prefix.
  expect_contains "$calls" "run --repo $dir/sandbox --json" "happy path: the launch command"
  expect_contains "$calls" "--json Add a line reading 'check 212' to CHECK212.md" "happy path: the small task the launches are made with"
  expect_contains "$calls" "run --repo $dir/sandbox --recipe ab-judge --json" "happy path: the judge launch"
  expect_contains "$calls" "models ls --json" "happy path: the model table read"
  expect_contains "$calls" "recipes ls --json" "happy path: the catalog read"
  expect_contains "$gcalls" "storage cat gs://fugaro-runs-${project}/runs/${slug}/$(run_of 1)/result.json --project=${project}" "happy path: the champion's record key"
  expect_contains "$gcalls" "storage cat gs://fugaro-runs-${project}/runs/${slug}/$(challenger_of 1)/result.json --project=${project}" "happy path: the challenger's record key"
  expect_contains "$gcalls" "storage cat gs://fugaro-runs-${project}/runs/${slug}/${judge_run}/ab/verdicts.json --project=${project}" "happy path: the verdict object's key"
  expect_contains "$calls" "runs ls --repo $dir/sandbox --batch ab-pair-$(run_of 1) --json" "happy path: the pair read back through the command the plan names"
  expect_contains "$calls" "ab --repo acme/sandbox --since 1d --json" "happy path: the report"
}

test_a_launch_loop_reaches_the_pair_after_its_first_samples() {
  # The plan's sentence is launches until a pair appears, so the pair is not the
  # first launch: the champion is the third, and the launches before it are NOT
  # graded as if they were the pair's.
  local dir=$work/late
  prepare "$dir" 2
  run_check "$dir" CHECK212_MAX_LAUNCHES=5
  expect_status "$status" 0 "a pair after two samples"
  expect_contains "$out" "FACT pair: PASS" "a pair after two samples: the row"
  expect_contains "$out" "launch 3 of at most 5 was sampled" "a pair after two samples: which launch"
  expect_contains "$out" "FACT champion: PASS" "a pair after two samples: the third launch's champion"
  expect_contains "$out" "runs/${slug}/$(run_of 3)/result.json" "a pair after two samples: the champion's record"
  expect_contains "$out" "the pair's id pair-$(run_of 3)" "a pair after two samples: the pair id is the champion's"
  # The first two launches are real runs: the check launches them and reports no
  # pair for each, and it must not have stopped at the first.
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
  # request really moved to CLOSED, which is what the row claims. The judge run is
  # in the same sweep and has nothing to delete or close, which is not a failure.
  expect_absent "$(cat "$dir/json/branches")" "fugaro/$(run_of 1)" "the teardown: the branch is gone from the remote"
  expect_contains "$(cat "$dir/json/prs")" "41 CLOSED" "the teardown: the pull request is closed where gh holds it"
  expect_contains "$out" "FACT restore: PASS" "the teardown: the row"
  expect_contains "$out" "every pull request their champions opened is closed, each read back" "the teardown: what the row says"
  # And the challenger's own branch is never deleted, because it never existed:
  # the row would be a claim about a branch nobody pushed.
  expect_absent "$gitcalls" "push origin --delete fugaro/$(challenger_of 1)" "the teardown: no delete for the shadow's branch"
  expect_absent "$ghcalls" "pr close 0" "the teardown: no close for a run that opened nothing"
}

test_a_missing_sandbox_is_refused() {
  # Without a checkout there is nothing to read the project, the layer or the
  # remote through, so the refusal comes before any scratch directory exists and
  # no block is printed: there is no run to report on.
  local dir=$work/nosandbox
  prepare "$dir"
  run_check "$dir" CHECK212_SANDBOX=
  expect_status "$status" 2 "no sandbox"
  expect_contains "$err" "Check 212 needs --sandbox" "no sandbox: the refusal"
  expect_absent "$out" "FACT " "no sandbox: no block is printed for a run that never started"
  assert_no_run "$dir" "no sandbox"
}

test_a_flag_with_no_value_is_refused() {
  local dir=$work/novalue
  prepare "$dir"
  run_check "$dir" -- --sandbox
  expect_status "$status" 2 "--sandbox with no value"
  expect_contains "$err" "--sandbox needs a path" "--sandbox with no value: the refusal"
  assert_no_run "$dir" "--sandbox with no value"
}

test_an_unknown_argument_is_refused() {
  local dir=$work/unknown
  prepare "$dir"
  run_check "$dir" -- --launch-anyway
  expect_status "$status" 2 "unknown argument"
  expect_contains "$err" "unknown argument: --launch-anyway" "unknown argument: the refusal"
  assert_no_run "$dir" "unknown argument"
}

test_a_path_that_is_not_a_checkout_is_refused() {
  local dir=$work/notacheckout
  prepare "$dir"
  run_check "$dir" CHECK212_SANDBOX="$dir/nowhere"
  expect_status "$status" 2 "a path that is not a directory"
  expect_contains "$err" "--sandbox is not a directory" "a path that is not a directory: the refusal"
  mkdir -p "$dir/empty"
  run_check "$dir" CHECK212_SANDBOX="$dir/empty"
  expect_status "$status" 2 "a directory that is not a checkout"
  expect_contains "$err" "carries no fugaro.yaml" "a directory that is not a checkout: the refusal"
  # Neither refusal reads a project or launches anything: a check that read the
  # layer for a directory that is not a repository would be reading another
  # project's.
  expect_absent "$calls" "doctor" "a path that is not a checkout: the project is not read"
  assert_no_run "$dir" "a path that is not a checkout"
}

test_yes_is_required_to_launch() {
  local dir=$work/noyes
  prepare "$dir"
  run_check "$dir" CHECK212_YES=
  expect_status "$status" 2 "no --yes"
  expect_contains "$err" "so it asks for --yes" "no --yes: the refusal names the flag"
  expect_contains "$err" "Nothing has been launched" "no --yes: the refusal says nothing was spent"
  expect_contains "$out" "FACT bound: PASS" "no --yes: the spend was stated first"
  assert_no_run "$dir" "no --yes"
  assert_scratch_removed "$dir" "no --yes"
}

test_a_traced_shell_is_refused() {
  # The check prints its own block, and a trace of a run that drives a real
  # `fugaro` is a second copy of everything that run printed.
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 1 "a traced shell"
  expect_contains "$out" "FACT xtrace: FAIL" "a traced shell: the row"
  expect_contains "$err" "this shell is tracing (bash -x)" "a traced shell: the refusal"
  assert_no_run "$dir" "a traced shell"
}

test_an_agent_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$out" "FACT agent-session: FAIL" "a coding agent's session: the row"
  expect_contains "$err" "so this is a coding agent's session" "a coding agent's session: the refusal"
  assert_no_run "$dir" "a coding agent's session"
}

test_the_belong_project_is_refused() {
  # The account half: the credential this would spend is a belong one.
  local dir=$work/belongaccount
  prepare "$dir"
  run_check "$dir" FAKE_MODE=belong-account
  expect_status "$status" 1 "a belong account"
  expect_contains "$out" "FACT belong: FAIL" "a belong account: the row"
  expect_contains "$err" "names belong" "a belong account: the refusal"
  assert_no_run "$dir" "a belong account"
}

test_the_belong_checkout_is_refused() {
  # The project half, read the way the check reads it — out of the checkout
  # itself, so a checkout of a belong project cannot be mistaken for a sandbox
  # one by anything the sandbox publishes.
  local dir=$work/belongproject
  prepare "$dir"
  setjson "$dir/json/doctor.json" 'd["project"]["gcp_project"] = "fugaro-belong-prod"'
  run_check "$dir"
  expect_status "$status" 1 "a belong checkout"
  expect_contains "$out" "FACT sandbox: FAIL" "a belong checkout: the row"
  expect_contains "$err" "this check is never run there" "a belong checkout: the refusal"
  assert_no_run "$dir" "a belong checkout"
}

test_a_build_without_the_ab_command_is_refused() {
  # A core build answers `fugaro ab` with D522's stub, which names no flag: the
  # check must not go on to spend launches measuring a build that samples
  # nothing.
  local dir=$work/nofeature
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-feature
  expect_status "$status" 1 "a build with no A/B"
  expect_contains "$out" "FACT release: FAIL" "a build with no A/B: the row"
  expect_contains "$err" "the installed fugaro carries no A/B" "a build with no A/B: the refusal"
  expect_absent "$calls" "recipes ls" "a build with no A/B: the catalog is not read"
  assert_no_run "$dir" "a build with no A/B"
}

test_a_build_older_than_the_release_is_refused() {
  local dir=$work/oldversion
  prepare "$dir"
  run_check "$dir" FAKE_MODE=old-version
  expect_status "$status" 1 "a build older than 0.24.0"
  expect_contains "$out" "FACT fugaro-version: PASS — 0.23.0" "an old build: the version it read"
  expect_contains "$out" "FACT release: FAIL" "an old build: the row"
  expect_contains "$err" "the installed fugaro is 0.23.0, older than 0.24.0" "an old build: the refusal names both versions"
  assert_no_run "$dir" "an old build"
}

test_a_dev_build_is_decided_by_the_feature_probe() {
  # `dev-9f8e7d6` is not a release number, and the check does not guess what it
  # sorts as: the `ab` command answering is what decides.
  local dir=$work/devbuild
  prepare "$dir"
  run_check "$dir" FAKE_MODE=dev-build
  expect_status "$status" 0 "a dev build"
  expect_contains "$out" "FACT release: PASS" "a dev build: the row"
  expect_contains "$out" "is not a release number" "a dev build: the row says how it decided"
  expect_contains "$out" "CHECK 212 RESULT END (exit 0)" "a dev build: the run went on"
}

test_a_catalog_without_the_judge_recipe_is_a_finding() {
  local dir=$work/norecipe
  prepare "$dir"
  write_recipes "$dir" missing
  run_check "$dir"
  expect_status "$status" 1 "a catalog with no ab-judge"
  expect_contains "$out" "FACT judge-recipe: FAIL" "a catalog with no ab-judge: the row"
  expect_contains "$out" "carries no recipe named ab-judge" "a catalog with no ab-judge: what it read"
  assert_no_run "$dir" "a catalog with no ab-judge"
}

test_a_judge_recipe_that_is_not_launchable_is_a_finding() {
  local dir=$work/notlaunchable
  prepare "$dir"
  write_recipes "$dir" not-launchable
  run_check "$dir"
  expect_status "$status" 1 "a recipe that is not launchable"
  expect_contains "$out" "is not launchable" "a recipe that is not launchable: the row"
  expect_contains "$out" "is refused for it" "a recipe that is not launchable: what follows"
  expect_absent "$calls" "run --repo" "a recipe that is not launchable: nothing is launched"
}

test_a_catalog_that_cannot_be_read_is_unmeasured() {
  local dir=$work/recipesfail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=recipes-fails
  expect_status "$status" 2 "a catalog that cannot be read"
  expect_contains "$out" "FACT judge-recipe: FAIL" "a catalog that cannot be read: the row reads FAIL"
  expect_contains "$err" "could NOT measure judge-recipe" "a catalog that cannot be read: the run says it could not measure"
  assert_no_run "$dir" "a catalog that cannot be read"
}

test_a_layer_without_an_ab_block_is_set_up_by_the_check() {
  # The owner's project carries no `ab:` block at all, which is the state this
  # check is here to establish rather than report: the rate and the judge model
  # go through `fugaro settings set layer.ab --add-entry`, the supported writer
  # of a member of that block, the challenger list into the project's own layer
  # file, and the Restore puts the file back byte for byte and publishes it
  # again.
  local dir=$work/noab before
  prepare "$dir"
  write_layer_without_ab "$dir"
  # The judge the setup picks is the first model of the table the repository may
  # use on the direct route, and the verdict object has to name it: the check
  # refuses a verdict written by a model the layer does not name (D553).
  write_verdicts "$dir" challenger "pair-$(run_of 1)" "$champion_model"
  before=$(sha256_file "$(layer_file_path "$dir")")
  run_check "$dir"
  expect_status "$status" 0 "a layer with no ab block"
  expect_first_line "$err" "the project layer's A/B state is read here and, where the rate, the challengers or the judge model are missing, set up" \
    "a layer with no ab block: the first line says what it does to the layer"
  expect_first_line "$err" "ab.rate 0.1, one challenger on a provider route this repository may use, and a judge model" \
    "a layer with no ab block: the first line names what it sets"
  expect_first_line "$err" "put back byte for byte at the end" "a layer with no ab block: the first line says it restores"
  expect_contains "$out" "FACT layer: PASS" "a layer with no ab block: the row"
  expect_contains "$out" "sets ab.rate: 0.1, names 1 challenger(s)" "a layer with no ab block: the state it read after writing"
  expect_contains "$out" "names the judge model ${champion_model}" "a layer with no ab block: the judge model it picked"
  expect_contains "$out" "This check SET IT UP first: ab.rate 0.1, set with 'fugaro settings set layer.ab --add-entry rate=0.1 --layer project' and the challenger ${challenger_model}, written into $(layer_file_path "$dir") and published with 'fugaro config publish $(layer_file_path "$dir")' and the judge model ${champion_model}, set with 'fugaro settings set layer.ab --add-entry judge_model=${champion_model} --layer project'" \
    "a layer with no ab block: the row names every write it made"
  expect_contains "$calls" "settings set layer.ab --add-entry rate=0.1 --layer project" "a layer with no ab block: the rate's own writer"
  expect_contains "$calls" "config publish $(layer_file_path "$dir")" "a layer with no ab block: the publish behind the list"
  expect_contains "$calls" "settings set layer.ab --add-entry judge_model=${champion_model} --layer project" "a layer with no ab block: the judge model's own writer"
  # The launches happen after the write, on the state it established, which is
  # the whole point of setting it up instead of stopping at the layer row.
  expect_contains "$out" "FACT pair: PASS" "a layer with no ab block: the launches ran after the setup"
  expect_contains "$out" "FACT restore: PASS" "a layer with no ab block: the teardown"
  expect_contains "$out" "The project layer was set up by this check, and $(layer_file_path "$dir") is back byte for byte (sha256 ${before})" \
    "a layer with no ab block: the Restore says the layer is back"
  assert_layer_restored "$dir" "$before" "a layer with no ab block"
}

test_a_layer_that_switched_ab_off_is_set_up_by_the_check() {
  # A rate of zero is the sampler switched off. The challenger and the judge
  # model are already named, so the one write is the rate's — and it goes through
  # `fugaro settings set`, which publishes the layer itself, so nothing here
  # publishes by hand.
  local dir=$work/rate0 before
  prepare "$dir"
  write_layer "$dir" block 0 "$challenger_model" "$judge_model"
  before=$(sha256_file "$(layer_file_path "$dir")")
  run_check "$dir"
  expect_status "$status" 0 "a layer at rate 0"
  expect_contains "$out" "FACT layer: PASS" "a layer at rate 0: the row"
  expect_contains "$out" "sets ab.rate: 0.1, names 1 challenger(s)" "a layer at rate 0: the rate it wrote"
  expect_contains "$out" "This check SET IT UP first: ab.rate 0.1, set with 'fugaro settings set layer.ab --add-entry rate=0.1 --layer project'. It copied" \
    "a layer at rate 0: the one write it names"
  # The one write is the rate's, and the one publish is the Restore's own: the
  # table is read for the route row below, but nothing is picked out of it.
  [ "$(calls_count "settings set")" = 1 ] || fail "a layer at rate 0: expected 1 write (the rate's), got $(calls_count "settings set")"
  expect_contains "$calls" "settings set layer.ab --add-entry rate=0.1 --layer project" "a layer at rate 0: the write it made"
  expect_absent "$calls" "judge_model=" "a layer at rate 0: no judge model is written, because none has to be"
  [ "$(calls_count "config publish")" = 1 ] || fail "a layer at rate 0: expected 1 publish (the Restore's), got $(calls_count "config publish")"
  assert_layer_restored "$dir" "$before" "a layer at rate 0"
}

test_a_layer_with_no_challenger_is_set_up_by_the_check() {
  # The rate and the judge model are the premise's and the list is empty: no
  # launch of it could ever sample — Admit skips with 'no-challenger' — so the
  # check names the model.
  local dir=$work/nochallenger before
  prepare "$dir"
  write_layer "$dir" block 0.1 "" "$judge_model"
  before=$(sha256_file "$(layer_file_path "$dir")")
  run_check "$dir"
  expect_status "$status" 0 "a layer with no challenger"
  expect_contains "$out" "FACT layer: PASS" "a layer with no challenger: the row"
  expect_contains "$out" "names 1 challenger(s)" "a layer with no challenger: the challenger it named"
  expect_contains "$out" "This check SET IT UP first: the challenger ${challenger_model}, written into $(layer_file_path "$dir") and published with 'fugaro config publish $(layer_file_path "$dir")'" \
    "a layer with no challenger: the write it names"
  expect_absent "$calls" "settings set" "a layer with no challenger: the rate and the judge model are left alone"
  expect_contains "$calls" "models ls --json" "a layer with no challenger: the table the challenger is picked out of"
  assert_layer_restored "$dir" "$before" "a layer with no challenger"
}

test_a_layer_with_no_judge_model_is_set_up_by_the_check() {
  # The judge run calls ab.judge_model, and a project that names none is refused
  # rather than judged on a model the owner did not pick (D553): the check names
  # one the repository may send its code to.
  local dir=$work/nojudge before
  prepare "$dir"
  write_layer "$dir" block 0.1 "$challenger_model" none
  write_verdicts "$dir" challenger "pair-$(run_of 1)" "$champion_model"
  before=$(sha256_file "$(layer_file_path "$dir")")
  run_check "$dir"
  expect_status "$status" 0 "a layer with no judge model"
  expect_contains "$out" "FACT layer: PASS" "a layer with no judge model: the row"
  expect_contains "$out" "names the judge model ${champion_model}" "a layer with no judge model: the judge it named"
  expect_contains "$out" "This check SET IT UP first: the judge model ${champion_model}, set with 'fugaro settings set layer.ab --add-entry judge_model=${champion_model} --layer project'" \
    "a layer with no judge model: the write it names"
  expect_contains "$calls" "models ls --json" "a layer with no judge model: the table the judge model is picked out of"
  expect_contains "$calls" "settings set layer.ab --add-entry judge_model=${champion_model} --layer project" "a layer with no judge model: the writer that publishes it"
  assert_layer_restored "$dir" "$before" "a layer with no judge model"
}

test_a_layer_the_repository_runs_as_its_own_coder_is_not_picked_as_the_challenger() {
  # A pair varies the champion's own model, so a challenger that IS the model the
  # repository runs is skipped with the closed reason 'champion-model' — naming it
  # would spend the whole launch bound without a pair. The row names the state,
  # which is what proves the pick passed over the one provider model there was.
  local dir=$work/coderpick before
  prepare "$dir"
  write_models "$dir" coder
  write_layer "$dir" block 0.1 "" "$judge_model"
  before=$(sha256_file "$(layer_file_path "$dir")")
  run_check "$dir"
  expect_status "$status" 1 "the repository's own coder as the only provider model"
  expect_contains "$out" "FACT layer: FAIL" "the repository's own coder as the only provider model: the row"
  expect_contains "$out" "no model of 'fugaro models ls --json' is served by a provider route that admits acme/sandbox" \
    "the repository's own coder as the only provider model: the pick found nothing, and the one provider model was the coder"
  expect_contains "$out" "fugaro providers add" "the repository's own coder as the only provider model: the fix it names"
  assert_no_run "$dir" "the repository's own coder as the only provider model"
  assert_layer_restored "$dir" "$before" "the repository's own coder as the only provider model"
}

test_a_project_with_no_provider_model_to_pick_is_a_finding() {
  # The plan's premise is a challenger on a provider route this repository may
  # use, and a project whose only provider model is not admitted to the
  # repository is one this check cannot set up: it is a finding about the
  # installation, and the row names the command that fixes it.
  local dir=$work/noprovider before
  prepare "$dir"
  write_models "$dir" not-admitted
  write_layer "$dir" block 0.1 "" "$judge_model"
  before=$(sha256_file "$(layer_file_path "$dir")")
  run_check "$dir"
  expect_status "$status" 1 "a project with no provider model for this repository"
  expect_contains "$out" "FACT layer: FAIL" "a project with no provider model for this repository: the row"
  expect_contains "$out" "did not set it up" "a project with no provider model for this repository: the row says the setup did not write"
  expect_contains "$out" "fugaro providers add" "a project with no provider model for this repository: the fix it names"
  # The only publish is the Restore's: no challenger was written into the file,
  # so nothing published the list.
  [ "$(calls_count "config publish")" = 1 ] || fail "a project with no provider model for this repository: expected 1 publish (the Restore's), got $(calls_count "config publish")"
  assert_no_run "$dir" "a project with no provider model for this repository"
  assert_layer_restored "$dir" "$before" "a project with no provider model for this repository"
}

test_a_layer_without_a_file_to_write_it_in_is_a_finding() {
  # The write lands in this machine's own layer file (D830) and nowhere else:
  # there is no command that publishes a layer this check made up, so a project
  # with no layer file here is one this check cannot set up.
  local dir=$work/nolocalfile
  prepare "$dir"
  write_layer_without_ab "$dir"
  rm -f "$(layer_file_path "$dir")"
  run_check "$dir"
  expect_status "$status" 1 "no layer file to write in"
  expect_contains "$out" "FACT layer: FAIL" "no layer file to write in: the row"
  expect_contains "$out" "carries no top-level ab: block" "no layer file to write in: the state the layer is in"
  expect_contains "$out" "there is no $(layer_file_path "$dir") to write it in" "no layer file to write in: the path it looked for"
  expect_absent "$calls" "settings set" "no layer file to write in: nothing is written"
  assert_no_run "$dir" "no layer file to write in"
}

test_a_layer_file_that_is_not_what_is_published_is_a_finding() {
  # The file is what the setup writes in, and publishing it puts every other edit
  # it carries live too: a file the project never published is a finding rather
  # than something this check republishes on the owner's behalf.
  local dir=$work/stale
  prepare "$dir"
  write_layer_without_ab "$dir"
  printf '# an edit this project never published\n' >>"$(layer_file_path "$dir")"
  run_check "$dir"
  expect_status "$status" 1 "a layer file that is not what is published"
  expect_contains "$out" "FACT layer: FAIL" "a layer file that is not what is published: the row"
  expect_contains "$out" "is not the layer this project published" "a layer file that is not what is published: what it read"
  expect_absent "$calls" "settings set" "a layer file that is not what is published: nothing is written"
  assert_no_run "$dir" "a layer file that is not what is published"
}

test_a_setup_write_that_fails_is_not_measured() {
  # `fugaro settings set layer.ab --add-entry` is the supported writer of the
  # rate, so a write of it that does not answer leaves the premise off and the
  # rest of the check unmeasured — and the Restore still puts the layer back.
  local dir=$work/setupfails before
  prepare "$dir"
  write_layer_without_ab "$dir"
  before=$(sha256_file "$(layer_file_path "$dir")")
  run_check "$dir" FAKE_MODE=settings-fails
  expect_status "$status" 2 "a setup write that fails"
  expect_contains "$out" "FACT layer: FAIL" "a setup write that fails: the row reads FAIL"
  expect_contains "$err" "could NOT measure layer" "a setup write that fails: the run says it could not measure"
  assert_no_run "$dir" "a setup write that fails"
  assert_layer_restored "$dir" "$before" "a setup write that fails"
}

test_a_publish_that_fails_is_not_measured() {
  # The challenger list goes into the file and out through `fugaro config
  # publish`: a publish that does not answer leaves the layer without the
  # challenger the sampler needs, so nothing below is measured.
  local dir=$work/publishfails before
  prepare "$dir"
  write_layer "$dir" block 0.1 "" "$judge_model"
  before=$(sha256_file "$(layer_file_path "$dir")")
  printf '1\n' >"$dir/json/publish-fail-at"
  run_check "$dir"
  expect_status "$status" 2 "a publish that fails"
  expect_contains "$out" "FACT layer: FAIL" "a publish that fails: the row reads FAIL"
  expect_contains "$err" "could NOT measure layer" "a publish that fails: the run says it could not measure"
  assert_no_run "$dir" "a publish that fails"
  assert_layer_restored "$dir" "$before" "a publish that fails"
}

test_a_restore_that_cannot_publish_the_layer_back_is_a_finding() {
  # The Restore is only half done when the file is back: the project has to serve
  # those bytes again, and a publish that does not answer is what the row says.
  local dir=$work/restorefails before
  prepare "$dir"
  write_layer "$dir" block 0.1 "" "$judge_model"
  before=$(sha256_file "$(layer_file_path "$dir")")
  # The first publish is the challenger's, so what fails here is the Restore's.
  printf '2\n' >"$dir/json/publish-fail-at"
  run_check "$dir"
  expect_status "$status" 1 "a Restore that cannot publish the layer back"
  expect_contains "$out" "FACT restore: FAIL" "a Restore that cannot publish the layer back: the row"
  expect_contains "$out" "The project layer was set up by this check and could NOT be put back" \
    "a Restore that cannot publish the layer back: the note"
  expect_contains "$out" "$(layer_file_path "$dir") is back byte for byte" "a Restore that cannot publish the layer back: the half that is met"
  # The bend really bit: the file is back and the project is not serving it.
  local got
  got=$(sha256_file "$(layer_file_path "$dir")")
  [ "$got" = "$before" ] || fail "a Restore that cannot publish the layer back: the file is not back byte for byte: sha256 $got, want $before"
  got=$(published_sha "$dir")
  [ "$got" != "$before" ] || fail "a Restore that cannot publish the layer back: the publish did not fail after all"
}

test_a_repository_that_opted_out_is_a_finding() {
  # A repository may lower the rate to zero, which is the opt-out (D556): the
  # check reads the checkout's own fugaro.yaml, not just the project's layer.
  local dir=$work/optout
  prepare "$dir"
  write_checkout "$dir/sandbox" 0
  run_check "$dir"
  expect_status "$status" 1 "a repository that opted out"
  expect_contains "$out" "FACT layer: FAIL" "a repository that opted out: the row"
  expect_contains "$out" "which is the opt-out" "a repository that opted out: what it read"
  assert_no_run "$dir" "a repository that opted out"
}

test_a_layer_that_cannot_be_read_is_unmeasured() {
  local dir=$work/nolayer
  prepare "$dir"
  run_check "$dir" FAKE_MODE=layer-missing
  expect_status "$status" 2 "a project with no published layer"
  expect_contains "$out" "FACT layer: FAIL" "a project with no published layer: the row reads FAIL"
  expect_contains "$err" "could NOT measure layer" "a project with no published layer: the run says so"
  assert_no_run "$dir" "a project with no published layer"
}

test_the_layer_reads_a_flow_style_ab_block() {
  # `fugaro config layer --json` hands over the layer's own text, and the two
  # shapes a YAML `ab:` block is written in are both layers this check runs on.
  local dir=$work/flowlayer
  prepare "$dir"
  write_layer "$dir" flow 0.1 "$challenger_model" "$judge_model"
  run_check "$dir"
  expect_status "$status" 0 "a flow-style layer"
  expect_contains "$out" "FACT layer: PASS" "a flow-style layer: the row"
  expect_contains "$out" "names the judge model ${judge_model}" "a flow-style layer: the judge model is read"
  expect_contains "$out" "CHECK 212 RESULT END (exit 0)" "a flow-style layer: the whole check ran"
}

test_a_challenger_on_the_direct_route_is_a_finding() {
  # A challenger on the direct Anthropic route is a second run on the champion's
  # own route, which is the one pair U4's question cannot be read from.
  local dir=$work/directroute
  prepare "$dir"
  write_models "$dir" direct
  run_check "$dir"
  expect_status "$status" 1 "a challenger on the direct route"
  expect_contains "$out" "FACT route: FAIL" "a challenger on the direct route: the row"
  expect_contains "$out" "is served by the direct route" "a challenger on the direct route: what it read"
  assert_no_run "$dir" "a challenger on the direct route"
}

test_a_challenger_whose_route_does_not_admit_the_repository_is_a_finding() {
  local dir=$work/notadmitted
  prepare "$dir"
  write_models "$dir" not-admitted
  run_check "$dir"
  expect_status "$status" 1 "a challenger this repository may not use"
  expect_contains "$out" "FACT route: FAIL" "a challenger this repository may not use: the row"
  expect_contains "$out" "does not admit acme/sandbox" "a challenger this repository may not use: what it read"
  assert_no_run "$dir" "a challenger this repository may not use"
}

test_a_challenger_no_route_serves_is_a_finding() {
  local dir=$work/unserved
  prepare "$dir"
  write_models "$dir" unserved
  run_check "$dir"
  expect_status "$status" 1 "a challenger no route serves"
  expect_contains "$out" "FACT route: FAIL" "a challenger no route serves: the row"
  expect_contains "$out" "is served by no route" "a challenger no route serves: what it read"
  assert_no_run "$dir" "a challenger no route serves"
}

test_a_route_open_to_every_repository_is_admitted() {
  local dir=$work/openroute
  prepare "$dir"
  write_models "$dir" open
  run_check "$dir"
  expect_status "$status" 0 "a route open to every repository"
  expect_contains "$out" "FACT route: PASS" "a route open to every repository: the row"
  expect_contains "$out" "allow_data_to *" "a route open to every repository: what it read"
}

test_a_model_table_that_cannot_be_read_is_unmeasured() {
  local dir=$work/modelsfail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=models-fails
  expect_status "$status" 2 "a model table that cannot be read"
  expect_contains "$out" "FACT route: FAIL" "a model table that cannot be read: the row reads FAIL"
  expect_contains "$err" "could NOT measure route" "a model table that cannot be read: the run says so"
  assert_no_run "$dir" "a model table that cannot be read"
}

test_a_bound_with_no_pair_is_a_finding() {
  # Nothing forces a sample — a sample is the sampler's own hash of the run ID —
  # so a bound that never samples is the one finding this row can carry, and it
  # says how to widen it rather than passing.
  local dir=$work/nopair
  prepare "$dir" 99
  run_check "$dir"
  expect_status "$status" 1 "a bound with no pair"
  expect_contains "$out" "FACT pair: FAIL" "a bound with no pair: the row"
  expect_contains "$out" "3 launches and none sampled a pair" "a bound with no pair: what it spent"
  expect_contains "$out" "3 of them answered with no pair block at all" "a bound with no pair: what they answered"
  expect_contains "$out" "widen it with --max-launches" "a bound with no pair: the lever it names"
  expect_contains "$out" "FACT restore: PASS" "a bound with no pair: the three launches are still torn down"
  expect_contains "$gitcalls" "push origin --delete fugaro/$(run_of 2)" "a bound with no pair: the second launch's branch too"
}

test_a_skipped_launch_is_reported_with_its_reason() {
  local dir=$work/skipped
  prepare "$dir" 99
  run_check "$dir" FAKE_MODE=pair-skipped
  expect_status "$status" 1 "a skipped launch"
  expect_contains "$out" "FACT pair: FAIL" "a skipped launch: the row"
  expect_contains "$out" "3 were skipped with a closed reason: daily-count" "a skipped launch: the reason the sampler gave"
}

test_a_launch_that_is_refused_is_a_finding() {
  local dir=$work/runfails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=run-fails
  expect_status "$status" 1 "a launch that is refused"
  expect_contains "$out" "FACT pair: FAIL" "a launch that is refused: the row"
  expect_contains "$out" "fugaro run exited 1 on launch 1" "a launch that is refused: what it read"
  expect_contains "$out" "FACT restore: PASS" "a launch that is refused: the launches before it are still torn down"
}

test_a_launch_that_names_no_run_is_a_finding() {
  # The check follows and tears down the runs it launched: a launch that does
  # not name its own cannot be followed, and the check says so rather than
  # going on.
  local dir=$work/nameless
  prepare "$dir"
  run_check "$dir" FAKE_MODE=launch-nameless
  expect_status "$status" 1 "a launch that names no run"
  expect_contains "$out" "FACT pair: FAIL" "a launch that names no run: the row"
  expect_contains "$out" "named no run" "a launch that names no run: what it read"
  expect_absent "$calls" "cancel" "a launch that names no run: nothing is cancelled"
}

test_a_sampled_pair_without_a_challenger_is_a_finding() {
  local dir=$work/noshadow
  prepare "$dir" 1
  run_check "$dir" FAKE_MODE=pair-broken
  expect_status "$status" 1 "a pair whose challenger did not start"
  expect_contains "$out" "FACT pair: FAIL" "a pair whose challenger did not start: the row"
  expect_contains "$out" "and the challenger did not start" "a pair whose challenger did not start: what it read"
  expect_contains "$out" "FACT restore: PASS" "a pair whose challenger did not start: the champion is still torn down"
  expect_contains "$gitcalls" "push origin --delete fugaro/$(run_of 1)" "a pair whose challenger did not start: the champion's branch is deleted"
}

test_an_arm_that_does_not_end_is_unmeasured() {
  local dir=$work/notended
  prepare "$dir"
  run_check "$dir" FAKE_MODE=arms-not-ended
  expect_status "$status" 2 "an arm that does not end"
  expect_contains "$out" "FACT arms: FAIL" "an arm that does not end: the row reads FAIL"
  expect_contains "$out" "had not both ended after 6s" "an arm that does not end: what it waited"
  expect_contains "$err" "could NOT measure arms" "an arm that does not end: the run says so"
  expect_contains "$calls" "cancel $(challenger_of 1) --now" "an arm that does not end: the teardown stops it"
  expect_contains "$out" "FACT restore: PASS" "an arm that does not end: the teardown read it back as ended"
}

test_a_runs_list_that_does_not_answer_is_unmeasured() {
  local dir=$work/rowsfail
  prepare "$dir"
  run_check "$dir" FAKE_MODE=rows-fails
  expect_status "$status" 1 "a runs list that does not answer"
  expect_contains "$out" "FACT arms: FAIL" "a runs list that does not answer: the arms row reads FAIL"
  expect_contains "$out" "FACT restore: FAIL" "a runs list that does not answer: the teardown cannot settle what it opened"
  expect_contains "$out" "could not be read back" "a runs list that does not answer: what the teardown says"
  # The arms row is the one that could not be measured, and it carries the
  # reason in its own detail: the exit status is 1 here because the teardown
  # genuinely could not settle what it had opened.
  expect_contains "$out" "had not both ended after 6s" "a runs list that does not answer: the arms row carries the reason"
}

test_a_batch_carrying_a_third_row_is_a_finding() {
  local dir=$work/batchthird
  prepare "$dir"
  run_check "$dir" FAKE_MODE=batch-extra
  expect_status "$status" 1 "a batch carrying a third row"
  expect_contains "$out" "FACT arms: FAIL" "a batch carrying a third row: the row"
  expect_contains "$out" "answered 3 row(s) and the pair is two" "a batch carrying a third row: what it read"
}

test_two_rows_keyed_elsewhere_are_a_finding() {
  local dir=$work/renamed
  prepare "$dir"
  run_check "$dir" FAKE_MODE=arms-renamed
  expect_status "$status" 1 "two rows keyed elsewhere"
  expect_contains "$out" "FACT arms: FAIL" "two rows keyed elsewhere: the row"
  expect_contains "$out" "a row keyed elsewhere is another run's" "two rows keyed elsewhere: what it read"
}

test_a_champion_record_that_is_another_run_s_is_a_finding() {
  local dir=$work/championother
  prepare "$dir"
  setjson "$dir/json/records/$(run_of 1).json" 'd["run_id"] = "someone-elses-run"'
  run_check "$dir"
  expect_status "$status" 1 "a champion record that is another run's"
  expect_contains "$out" "FACT champion: FAIL" "a champion record that is another run's: the row"
  expect_contains "$out" "records run_id=someone-elses-run" "a champion record that is another run's: what it read"
}

test_a_champion_without_the_arm_is_a_finding() {
  local dir=$work/championarm
  prepare "$dir"
  setjson "$dir/json/records/$(run_of 1).json" 'del d["ab"]["arm"]'
  run_check "$dir"
  expect_status "$status" 1 "a champion with no arm"
  expect_contains "$out" "FACT champion: FAIL" "a champion with no arm: the row"
  expect_contains "$out" "carries ab.arm 'absent' and this arm is 'champion'" "a champion with no arm: what it read"
}

test_a_champion_carrying_another_pair_is_a_finding() {
  local dir=$work/championpair
  prepare "$dir"
  setjson "$dir/json/records/$(run_of 1).json" 'd["ab"]["pair_id"] = "pair-someone-elses"'
  run_check "$dir"
  expect_status "$status" 1 "a champion carrying another pair"
  expect_contains "$out" "FACT champion: FAIL" "a champion carrying another pair: the row"
  expect_contains "$out" "carries ab.pair_id 'pair-someone-elses'" "a champion carrying another pair: what it read"
}

test_a_record_that_cannot_be_read_is_unmeasured() {
  local dir=$work/norecord
  prepare "$dir"
  run_check "$dir" FAKE_MODE=record-missing
  expect_status "$status" 2 "a record the bucket does not carry"
  expect_contains "$out" "FACT champion: FAIL" "a record the bucket does not carry: the row reads FAIL"
  expect_contains "$err" "could NOT measure champion" "a record the bucket does not carry: the run says so"
}

test_a_challenger_that_published_is_a_finding() {
  local dir=$work/shadowpublished
  prepare "$dir"
  setjson "$dir/json/records/$(challenger_of 1).json" \
    'd["branch"] = "fugaro/pushed"; d["pr"] = {"number": 42, "url": "https://example.invalid/acme/sandbox/pull/42"}; d["outcome"] = "pr"'
  run_check "$dir"
  expect_status "$status" 1 "a shadow that published"
  expect_contains "$out" "FACT challenger: FAIL" "a shadow that published: the row"
  expect_contains "$out" "carries outcome 'pr'" "a shadow that published: the outcome is read first"
}

test_a_challenger_with_no_publish_mode_is_a_finding() {
  local dir=$work/nopublish
  prepare "$dir"
  setjson "$dir/json/records/$(challenger_of 1).json" 'del d["ab"]["publish"]'
  run_check "$dir"
  expect_status "$status" 1 "a challenger with no publish mode"
  expect_contains "$out" "carries ab.publish 'absent' and the arm's own block is 'none'" "a challenger with no publish mode: what it read"
}

test_a_challenger_recording_another_model_is_a_finding() {
  # The route row proved the layer's list is made of routed provider models;
  # this row proves the pair that was sampled drew from that list.
  local dir=$work/othermodel
  prepare "$dir"
  write_challenger "$dir" "$(challenger_of 1)" "" "" "" "" "gpt-4o"
  run_check "$dir"
  expect_status "$status" 1 "a challenger on a model the layer never named"
  expect_contains "$out" "FACT challenger: FAIL" "a challenger on a model the layer never named: the row"
  expect_contains "$out" "which is not one of the provider models this check resolved as routed" "a challenger on a model the layer never named: what it read"
  expect_contains "$out" "${challenger_model}" "a challenger on a model the layer never named: the list it read is named"
}

test_a_challenger_whose_model_came_from_the_repository_is_a_finding() {
  local dir=$work/reposource
  prepare "$dir"
  setjson "$dir/json/records/$(challenger_of 1).json" 'd["ab"]["challenger_source"] = "repo"'
  run_check "$dir"
  expect_status "$status" 1 "a challenger the repository named"
  expect_contains "$out" "carries ab.challenger_source 'repo'" "a challenger the repository named: what it read"
}

test_a_record_with_no_wall_times_is_a_finding() {
  local dir=$work/notimes
  prepare "$dir"
  setjson "$dir/json/records/$(run_of 1).json" 'del d["started_at"]; del d["finished_at"]'
  run_check "$dir"
  expect_status "$status" 1 "a record with no wall times"
  expect_contains "$out" "FACT wall-times: FAIL" "a record with no wall times: the row"
  expect_contains "$out" "U4's two wall times could not both be read" "a record with no wall times: what it read"
}

test_a_wall_time_at_zero_is_a_finding() {
  local dir=$work/zerotime
  prepare "$dir"
  setjson "$dir/json/records/$(run_of 1).json" 'd["finished_at"] = d["started_at"]'
  run_check "$dir"
  expect_status "$status" 1 "a wall time at zero"
  expect_contains "$out" "FACT wall-times: FAIL" "a wall time at zero: the row"
  expect_contains "$out" "a duration at or below zero" "a wall time at zero: what it read"
}

test_the_wall_times_row_records_both_and_applies_no_threshold() {
  # U4 asks the check to RECORD both wall times; the plan sets no bar for the
  # ratio, so a challenger an hour slower is a figure to read rather than a
  # finding — and the row says so in its own words.
  local dir=$work/slowshadow
  prepare "$dir"
  setjson "$dir/json/records/$(challenger_of 1).json" 'd["finished_at"] = "2026-11-14T11:13:05.222222222Z"'
  run_check "$dir"
  expect_status "$status" 0 "a much slower challenger"
  expect_contains "$out" "FACT wall-times: PASS" "a much slower challenger: the row"
  expect_contains "$out" "The challenger's is 5.80x the champion's" "a much slower challenger: the ratio is recorded"
  expect_contains "$out" "it sets no threshold for the ratio" "a much slower challenger: the row says no bar was applied"
}

test_a_pair_with_no_per_run_cap_is_a_finding() {
  local dir=$work/nocap
  prepare "$dir"
  setjson "$dir/json/records/$(run_of 1).json" 'd["policy"]["effective"]["per_run_usd"] = 0'
  setjson "$dir/json/records/$(challenger_of 1).json" 'd["policy"]["effective"]["per_run_usd"] = 0'
  run_check "$dir"
  expect_status "$status" 1 "a pair with no per-run cap"
  expect_contains "$out" "FACT costs: FAIL" "a pair with no per-run cap: the row"
  expect_contains "$out" "is not on the records" "a pair with no per-run cap: what it read"
}

test_two_arms_under_different_caps_are_a_finding() {
  # D567: the challenger runs under the champion's cap unchanged, so two records
  # that disagree about the figure are a pair budgeted separately.
  local dir=$work/differentcap
  prepare "$dir"
  setjson "$dir/json/records/$(challenger_of 1).json" 'd["policy"]["effective"]["per_run_usd"] = 7'
  run_check "$dir"
  expect_status "$status" 1 "two arms under different caps"
  expect_contains "$out" "FACT costs: FAIL" "two arms under different caps: the row"
  expect_contains "$out" "ran under different per-run caps" "two arms under different caps: what it read"
}

test_a_record_with_no_spend_is_a_finding() {
  local dir=$work/nospend
  prepare "$dir"
  setjson "$dir/json/records/$(run_of 1).json" 'd["cost_usd"] = 0'
  run_check "$dir"
  expect_status "$status" 1 "a record with no spend"
  expect_contains "$out" "FACT costs: FAIL" "a record with no spend: the row"
  expect_contains "$out" "carries no spend" "a record with no spend: what it read"
}

test_a_cost_above_the_cap_is_a_finding() {
  local dir=$work/overcap
  prepare "$dir"
  setjson "$dir/json/records/$(challenger_of 1).json" 'd["cost_usd"] = 9'
  run_check "$dir"
  expect_status "$status" 1 "a challenger above the cap"
  expect_contains "$out" "FACT costs: FAIL" "a challenger above the cap: the row"
  expect_contains "$out" "is above the per-run cap 5" "a challenger above the cap: what it read"
}

test_a_judge_run_that_is_refused_is_a_finding() {
  local dir=$work/judgefails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=judge-fails
  expect_status "$status" 1 "a judge run that is refused"
  expect_contains "$out" "FACT judge-run: FAIL" "a judge run that is refused: the row"
  expect_contains "$out" "exited 1" "a judge run that is refused: what it read"
  expect_contains "$out" "FACT restore: PASS" "a judge run that is refused: the pair is still torn down"
}

test_a_judge_that_names_no_run_is_a_finding() {
  local dir=$work/judgenameless
  prepare "$dir"
  run_check "$dir" FAKE_MODE=judge-nameless
  expect_status "$status" 1 "a judge launch that names no run"
  expect_contains "$out" "FACT judge-run: FAIL" "a judge launch that names no run: the row"
  expect_contains "$out" "the judge launch named no run" "a judge launch that names no run: what it read"
}

test_a_judge_that_publishes_is_a_finding() {
  # The judge is a mode: evaluate run with no checkout of its own, and its
  # blindness rests on its reading the two patch objects rather than anything it
  # pushed.
  local dir=$work/judgepushed
  prepare "$dir"
  setjson "$dir/json/records/${judge_run}.json" \
    'd["branch"] = "fugaro/judged"; d["pr"] = {"number": 43, "url": "https://example.invalid/acme/sandbox/pull/43"}'
  run_check "$dir"
  expect_status "$status" 1 "a judge that published"
  expect_contains "$out" "FACT judge-run: FAIL" "a judge that published: the row"
  expect_contains "$out" "left branch 'fugaro/judged' and pr 43" "a judge that published: what it read"
}

test_a_judge_that_never_ends_is_unmeasured() {
  local dir=$work/judgehangs
  prepare "$dir"
  run_check "$dir" FAKE_MODE=judge-not-ended
  expect_status "$status" 2 "a judge run that does not end"
  expect_contains "$out" "FACT judge-run: FAIL" "a judge run that does not end: the row reads FAIL"
  expect_contains "$out" "had not ended after 6s" "a judge run that does not end: what it waited"
  expect_contains "$calls" "cancel ${judge_run} --now" "a judge run that does not end: the teardown stops it"
  expect_contains "$out" "FACT restore: PASS" "a judge run that does not end: the teardown read it back as ended"
}

test_a_missing_verdict_object_is_unmeasured() {
  local dir=$work/noverdicts
  prepare "$dir"
  run_check "$dir" FAKE_MODE=verdicts-missing
  expect_status "$status" 2 "a verdict object the bucket does not carry"
  expect_contains "$out" "FACT verdict: FAIL" "a verdict object the bucket does not carry: the row reads FAIL"
  expect_contains "$out" "left no readable verdict object" "a verdict object the bucket does not carry: what it read"
}

test_a_verdict_for_another_pair_is_a_finding() {
  local dir=$work/otherverdict
  prepare "$dir"
  write_verdicts "$dir" challenger pair-someone-elses
  run_check "$dir"
  expect_status "$status" 1 "a verdict for another pair"
  expect_contains "$out" "FACT verdict: FAIL" "a verdict for another pair: the row"
  expect_contains "$out" "carries no entry for the pair" "a verdict for another pair: what it read"
}

test_a_skipped_pair_is_a_finding() {
  # A skipped pair decided nothing, and reading it as a tie would count a pair
  # nobody judged.
  local dir=$work/skippedpair
  prepare "$dir"
  setjson "$dir/json/verdicts.json" 'd["pairs"][0]["skipped"] = "no-diff"; d["pairs"][0].pop("winner")'
  run_check "$dir"
  expect_status "$status" 1 "a skipped pair"
  expect_contains "$out" "FACT verdict: FAIL" "a skipped pair: the row"
  expect_contains "$out" "SKIPPED the pair" "a skipped pair: what it read"
}

test_a_verdict_naming_another_judge_model_is_a_finding() {
  local dir=$work/otherjudge
  prepare "$dir"
  write_verdicts "$dir" challenger "pair-$(run_of 1)" "some-other-model"
  run_check "$dir"
  expect_status "$status" 1 "a verdict from another judge model"
  expect_contains "$out" "FACT verdict: FAIL" "a verdict from another judge model: the row"
  expect_contains "$out" "carries judge_model 'some-other-model'" "a verdict from another judge model: what it read"
}

test_a_verdict_from_one_pass_is_a_finding() {
  local dir=$work/onepass
  prepare "$dir"
  write_verdicts "$dir" challenger "pair-$(run_of 1)" "$judge_model" 1
  run_check "$dir"
  expect_status "$status" 1 "a verdict from one pass"
  expect_contains "$out" "FACT verdict: FAIL" "a verdict from one pass: the row"
  expect_contains "$out" "carries 1 pass(es) and the judge makes two per pair" "a verdict from one pass: what it read"
}

test_a_verdict_with_an_unknown_winner_is_a_finding() {
  local dir=$work/badwinner
  prepare "$dir"
  write_verdicts "$dir" maybe "pair-$(run_of 1)"
  run_check "$dir"
  expect_status "$status" 1 "a verdict with an unknown winner"
  expect_contains "$out" "FACT verdict: FAIL" "a verdict with an unknown winner: the row"
  expect_contains "$out" "which is none of champion, challenger or tie" "a verdict with an unknown winner: what it read"
}

test_a_report_without_the_pair_is_a_finding() {
  local dir=$work/noreport
  prepare "$dir"
  write_ab "$dir" "some-other-model" 1 below-minimum 1
  run_check "$dir"
  expect_status "$status" 1 "a report with no row for the challenger"
  expect_contains "$out" "FACT ab-report: FAIL" "a report with no row for the challenger: the row"
  expect_contains "$out" "carries no row for the challenger model ${challenger_model} with a pair in it" "a report with no row for the challenger: what it read"
  expect_contains "$out" "its rows name some-other-model in mode coder" "a report with no row for the challenger: the rows it did carry"
}

test_a_report_that_draws_a_conclusion_is_a_finding() {
  # D563's gate: under twenty decisive pairs the row carries no conclusion. A
  # rule other than below-minimum is a figure drawn from a sample that thin.
  local dir=$work/reportconcludes
  prepare "$dir"
  write_ab "$dir" "$challenger_model" 5 win 5
  run_check "$dir"
  expect_status "$status" 1 "a report that draws a conclusion"
  expect_contains "$out" "FACT ab-report: FAIL" "a report that draws a conclusion: the row"
  expect_contains "$out" "carries the rule 'win'" "a report that draws a conclusion: what it read"
  expect_contains "$out" "5 decisive" "a report that draws a conclusion: the thin sample is named"
}

test_a_report_at_the_minimum_is_unmeasured() {
  # The plan's row is that the report lists THIS pair as below the minimum: an
  # installation that has already reached twenty decisive pairs cannot show it,
  # and the row says it could not measure the fact rather than passing it.
  local dir=$work/reportatminimum
  prepare "$dir"
  write_ab "$dir" "$challenger_model" 20 below-minimum 20
  run_check "$dir"
  expect_status "$status" 2 "a report at the minimum"
  expect_contains "$out" "FACT ab-report: FAIL" "a report at the minimum: the row reads FAIL"
  expect_contains "$out" "which is at or above the twenty D563's gate is drawn at" "a report at the minimum: what it read"
  expect_contains "$err" "could NOT measure ab-report" "a report at the minimum: the run says so"
}

test_the_report_row_reads_the_row_with_the_lowest_decisive_count() {
  # A challenger may carry a row per rigor mode or tier, and the row the check
  # reads is the one the pair is in: the lowest decisive count is the one that
  # can still be below the minimum.
  local dir=$work/reportrows
  prepare "$dir"
  setjson "$dir/json/ab.json" 'import copy
second = copy.deepcopy(d["rows"][0])
second["tier"] = "deep"
second["pairs"] = 40
second["recommendation"]["decisive"] = 40
second["recommendation"]["rule"] = "keep"
second["recommendation"]["decision"] = "keep"
d["rows"].append(second)'
  run_check "$dir"
  expect_status "$status" 0 "two rows for the same challenger"
  expect_contains "$out" "FACT ab-report: PASS" "two rows for the same challenger: the row"
  expect_contains "$out" "tier standard" "two rows for the same challenger: the thinner row is the one read"
}

test_a_report_that_cannot_be_read_is_unmeasured() {
  local dir=$work/abfails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=ab-fails
  expect_status "$status" 2 "a report that does not answer"
  expect_contains "$out" "FACT ab-report: FAIL" "a report that does not answer: the row reads FAIL"
  expect_contains "$err" "could NOT measure ab-report" "a report that does not answer: the run says so"
}

test_a_report_document_that_is_not_json_is_unmeasured() {
  local dir=$work/abunreadable
  prepare "$dir"
  run_check "$dir" FAKE_MODE=ab-unreadable
  expect_status "$status" 2 "a report that is not a document"
  expect_contains "$out" "printed a document this check could not read" "a report that is not a document: what it read"
}

test_a_launch_that_has_not_ended_is_cancelled_and_read_back() {
  local dir=$work/stillgoing
  prepare "$dir"
  run_check "$dir" FAKE_MODE=rows-running
  expect_status "$status" 2 "launches still going"
  expect_contains "$calls" "cancel $(run_of 1) --now" "launches still going: the champion is stopped"
  expect_contains "$calls" "cancel $(challenger_of 1) --now" "launches still going: the shadow is stopped"
  expect_contains "$out" "FACT restore: PASS" "launches still going: the read-back finds them ended"
  expect_contains "$out" "were cancelled with 'fugaro cancel" "launches still going: the row says what it did"
}

test_a_run_the_cancel_does_not_end_is_a_finding() {
  # A run still going when the check gives up is settled by nothing the teardown
  # reads: it can push its branch after the check is over, so the teardown says
  # so and names the commands rather than claiming it cleaned up.
  local dir=$work/stuck
  prepare "$dir"
  run_check "$dir" FAKE_MODE=rows-running-stuck
  expect_status "$status" 1 "a run the cancel does not end"
  expect_contains "$out" "FACT restore: FAIL" "a run the cancel does not end: the row"
  expect_contains "$out" "still going" "a run the cancel does not end: what it read"
  expect_contains "$out" "settle each one by run id" "a run the cancel does not end: the commands it names"
}

test_a_branch_that_cannot_be_deleted_is_a_finding() {
  local dir=$work/pushfails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=push-fails
  expect_status "$status" 1 "a branch the teardown cannot delete"
  expect_contains "$out" "FACT restore: FAIL" "a branch the teardown cannot delete: the row"
  expect_contains "$out" "git push --delete exited 1" "a branch the teardown cannot delete: what it read"
  expect_contains "$gitcalls" "push origin --delete fugaro/$(run_of 1)" "a branch the teardown cannot delete: it really tried"
}

test_a_pull_request_that_cannot_be_closed_is_a_finding() {
  local dir=$work/closefails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=pr-close-fails
  expect_status "$status" 1 "a pull request the teardown cannot close"
  expect_contains "$out" "FACT restore: FAIL" "a pull request the teardown cannot close: the row"
  expect_contains "$out" "pull request 41 (gh pr close exited 1)" "a pull request the teardown cannot close: what it read"
}

test_a_machine_without_gh_still_deletes_the_branches() {
  # `gh` is the one tool this check uses that is not required: a machine without
  # it deletes the branches and says, in the Restore row, which pull requests are
  # left for you.
  local dir=$work/nogh
  prepare "$dir"
  rm -f "$dir/bin/gh"
  # A runner image has the real gh in /usr/bin, so the system directories are
  # mirrored without it rather than named on PATH.
  mkdir -p "$dir/sys"
  local d f
  for d in /usr/bin /bin; do
    for f in "$d"/*; do
      [[ ${f##*/} == gh || -d $f ]] && continue
      ln -sfn "$f" "$dir/sys/${f##*/}"
    done
  done
  run_check "$dir" PATH="$dir/bin:$dir/sys"
  expect_status "$status" 1 "a machine without gh"
  expect_contains "$out" "FACT restore: FAIL" "a machine without gh: the row"
  expect_contains "$out" "is not on PATH" "a machine without gh: the row names the tool"
  expect_contains "$gitcalls" "push origin --delete fugaro/$(run_of 1)" "a machine without gh: the branch is still deleted"
  expect_absent "$ghcalls" "pr close" "a machine without gh: nothing pretends to close it"
}

test_the_scratch_directory_is_removed_after_a_failure() {
  local dir=$work/scratchgone
  prepare "$dir"
  run_check "$dir" FAKE_MODE=run-fails
  expect_status "$status" 1 "a failing run's scratch directory"
  expect_contains "$out" "FACT scratch-removed: PASS" "a failing run's scratch directory: the row"
  assert_scratch_removed "$dir" "a failing run's scratch directory"
}

test_keep_scratch_keeps_the_scratch_directory() {
  # The debugging knob: the directory stays, and the block says the run did not
  # leave the machine as it found it rather than reporting a clean exit.
  local dir=$work/keptscratch
  prepare "$dir"
  run_check "$dir" CHECK212_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$err" "CHECK212_KEEP_SCRATCH is set" "a kept scratch directory: what it says"
  [ -n "$scratch" ] && [ -d "$scratch" ] || fail "a kept scratch directory: the directory is gone"
  [ -n "$scratch" ] && rm -rf "$scratch"
}

test_a_flag_beats_the_same_knob_in_the_environment() {
  # The environment is how a script sets a knob and the flag is what the owner
  # typed: a run that quietly used the other one would spend the sandbox's money
  # at a bound nobody chose.
  local dir=$work/flagwins
  prepare "$dir" 99
  run_check "$dir" CHECK212_MAX_LAUNCHES=3 -- --max-launches 1
  expect_status "$status" 1 "a flag over the environment"
  expect_contains "$out" "1 launches and none sampled a pair" "a flag over the environment: the flag's bound was used"
}

test_the_task_flag_is_what_the_launches_are_launched_with() {
  local dir=$work/taskflag
  prepare "$dir"
  run_check "$dir" -- --task "change nothing at all"
  expect_status "$status" 0 "the --task flag"
  expect_contains "$calls" "--json change nothing at all" "the --task flag: the task the launch was made with"
  expect_absent "$calls" "check 212' to CHECK212.md" "the --task flag: the default task is not used"
}

# ------------------------------------------------------------------- the suite
# CHECK_TEST_ONLY names one case; CHECK_TEST_SHARD i/n runs every nth case, so
# the suite can be spread over a loaded machine without any case depending on
# another's fixtures.
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
t test_a_doctor_that_exits_1_with_its_document_is_an_answer
t test_a_doctor_that_does_not_answer_is_not_measured
t test_a_launch_loop_reaches_the_pair_after_its_first_samples
t test_the_teardown_closes_the_pull_request_and_deletes_the_branch
t test_a_missing_sandbox_is_refused
t test_a_flag_with_no_value_is_refused
t test_an_unknown_argument_is_refused
t test_a_path_that_is_not_a_checkout_is_refused
t test_yes_is_required_to_launch
t test_a_traced_shell_is_refused
t test_an_agent_session_is_refused
t test_the_belong_project_is_refused
t test_the_belong_checkout_is_refused
t test_a_build_without_the_ab_command_is_refused
t test_a_build_older_than_the_release_is_refused
t test_a_dev_build_is_decided_by_the_feature_probe
t test_a_catalog_without_the_judge_recipe_is_a_finding
t test_a_judge_recipe_that_is_not_launchable_is_a_finding
t test_a_catalog_that_cannot_be_read_is_unmeasured
t test_a_layer_without_an_ab_block_is_set_up_by_the_check
t test_a_layer_that_switched_ab_off_is_set_up_by_the_check
t test_a_layer_with_no_challenger_is_set_up_by_the_check
t test_a_layer_with_no_judge_model_is_set_up_by_the_check
t test_a_layer_the_repository_runs_as_its_own_coder_is_not_picked_as_the_challenger
t test_a_project_with_no_provider_model_to_pick_is_a_finding
t test_a_layer_without_a_file_to_write_it_in_is_a_finding
t test_a_layer_file_that_is_not_what_is_published_is_a_finding
t test_a_setup_write_that_fails_is_not_measured
t test_a_publish_that_fails_is_not_measured
t test_a_restore_that_cannot_publish_the_layer_back_is_a_finding
t test_a_repository_that_opted_out_is_a_finding
t test_a_layer_that_cannot_be_read_is_unmeasured
t test_the_layer_reads_a_flow_style_ab_block
t test_a_challenger_on_the_direct_route_is_a_finding
t test_a_challenger_whose_route_does_not_admit_the_repository_is_a_finding
t test_a_challenger_no_route_serves_is_a_finding
t test_a_route_open_to_every_repository_is_admitted
t test_a_model_table_that_cannot_be_read_is_unmeasured
t test_a_bound_with_no_pair_is_a_finding
t test_a_skipped_launch_is_reported_with_its_reason
t test_a_launch_that_is_refused_is_a_finding
t test_a_launch_that_names_no_run_is_a_finding
t test_a_sampled_pair_without_a_challenger_is_a_finding
t test_an_arm_that_does_not_end_is_unmeasured
t test_a_runs_list_that_does_not_answer_is_unmeasured
t test_a_batch_carrying_a_third_row_is_a_finding
t test_two_rows_keyed_elsewhere_are_a_finding
t test_a_champion_record_that_is_another_run_s_is_a_finding
t test_a_champion_without_the_arm_is_a_finding
t test_a_champion_carrying_another_pair_is_a_finding
t test_a_record_that_cannot_be_read_is_unmeasured
t test_a_challenger_that_published_is_a_finding
t test_a_challenger_with_no_publish_mode_is_a_finding
t test_a_challenger_recording_another_model_is_a_finding
t test_a_challenger_whose_model_came_from_the_repository_is_a_finding
t test_a_record_with_no_wall_times_is_a_finding
t test_a_wall_time_at_zero_is_a_finding
t test_the_wall_times_row_records_both_and_applies_no_threshold
t test_a_pair_with_no_per_run_cap_is_a_finding
t test_two_arms_under_different_caps_are_a_finding
t test_a_record_with_no_spend_is_a_finding
t test_a_cost_above_the_cap_is_a_finding
t test_a_judge_run_that_is_refused_is_a_finding
t test_a_judge_that_names_no_run_is_a_finding
t test_a_judge_that_publishes_is_a_finding
t test_a_judge_that_never_ends_is_unmeasured
t test_a_missing_verdict_object_is_unmeasured
t test_a_verdict_for_another_pair_is_a_finding
t test_a_skipped_pair_is_a_finding
t test_a_verdict_naming_another_judge_model_is_a_finding
t test_a_verdict_from_one_pass_is_a_finding
t test_a_verdict_with_an_unknown_winner_is_a_finding
t test_a_report_without_the_pair_is_a_finding
t test_a_report_that_draws_a_conclusion_is_a_finding
t test_a_report_at_the_minimum_is_unmeasured
t test_the_report_row_reads_the_row_with_the_lowest_decisive_count
t test_a_report_that_cannot_be_read_is_unmeasured
t test_a_report_document_that_is_not_json_is_unmeasured
t test_a_launch_that_has_not_ended_is_cancelled_and_read_back
t test_a_run_the_cancel_does_not_end_is_a_finding
t test_a_branch_that_cannot_be_deleted_is_a_finding
t test_a_pull_request_that_cannot_be_closed_is_a_finding
t test_a_machine_without_gh_still_deletes_the_branches
t test_the_scratch_directory_is_removed_after_a_failure
t test_keep_scratch_keeps_the_scratch_directory
t test_a_flag_beats_the_same_knob_in_the_environment
t test_the_task_flag_is_what_the_launches_are_launched_with

if [ "$failures" -ne 0 ]; then
  echo "check212_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check212_test.sh: ok"
