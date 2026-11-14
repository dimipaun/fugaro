#!/usr/bin/env bash
# Tests for scripts/live-checks/check422.sh, with fake `fugaro`, `gcloud` and
# `curl` on PATH: NOTHING here reaches a cloud, a project or a database, no agent
# runs a live command, and the fakes record every call they are given — in which
# directory it was made — so a case can assert what the check did and did not do.
#
# The fakes are a small model of one installation at the previous version: the
# fake `fugaro` answers doctor, the feature probes, `craft ls`, and the two
# converges, and its converge REALLY MIGRATES the case's own config file the way
# MC4 does — each named account gains a typed claude-oauth credential and each
# craft gains the access its stages run on — so the check reads a file that has
# been converted rather than a file that says it has. The fake `gcloud` prints an
# access token and the fake `curl` serves the published policy node, once before
# the converges and once after.
#
# The cases that matter are the ones where that model is bent, because each of
# them is a state the check exists to tell apart from a converged installation: a
# build without the crafts vocabulary, a config of another installation, a
# converge that fails, a converge that leaves the person a step, a converge that
# migrates nothing, an account left unconverted with and without the note the
# migration owes about it, a second converge that moves the keys the first wrote,
# a crafts list with problems, a policy node that cannot be read, one whose craft
# members a converge rewrote, and — THE row — a converge that left a step for the
# person, which is a manual step and the thing the hard rule forbids.
#
# Every command is bounded (see `bounded` below): a check that hangs fails here
# with the case's own name rather than hanging the suite that runs this file.
#
# Usage: bash scripts/live-checks/check422_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check422.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK422_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK422_TEST_HEAD:-900}")" ;;
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
CASE_TIMEOUT=${CHECK422_TEST_TIMEOUT:-90}

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

name=chk422
project=fugaro-chk422-test
# The two accounts the accounts design names, and the account the review stage is
# pinned to: the migration has to give every account a credential and every craft
# the access its own stages run on, which is why one craft follows another
# account than the other three.
account_one=work
account_two=personal

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check422.sh's test only: it
# prints the access token the node read needs and records the call. Nothing here
# reaches a real project.
set -euo pipefail
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
case "${1:-}" in
auth)
  [ "${2:-}" = "print-access-token" ] || { echo "gcloud: unexpected auth verb ${2:-}" >&2; exit 1; }
  case "$mode" in
  token-fails)
    echo "ERROR: (gcloud.auth.print-access-token) There was a problem refreshing your current auth tokens" >&2
    exit 1
    ;;
  token-empty) exit 0 ;;
  esac
  printf 'fake-access-token\n'
  ;;
*)
  echo "gcloud: unexpected subcommand ${1:-}" >&2
  exit 1
  ;;
esac
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_curl() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A stand-in for `curl`, for scripts/live-checks/check422.sh's test only: it
# answers the ONE read the check makes — the published policy node — out of the
# case's own directory, serving the document before the converges on the first
# call and the one after them on the second. It records every call and its url.
set -euo pipefail
mode=${FAKE_MODE:-default}
out=""
url=""
n=1
while [ $# -gt 0 ]; do
  case "$1" in
  -o)
    out=$2
    shift 2
    ;;
  -w | -H)
    shift 2
    ;;
  -sS | -s | -S)
    shift
    ;;
  *)
    url=$1
    shift
    ;;
  esac
done
if [ -f "$FAKE_LOG_DIR/curl.count" ]; then
  n=$(( $(cat "$FAKE_LOG_DIR/curl.count") + 1 ))
fi
printf '%s\n' "$n" >"$FAKE_LOG_DIR/curl.count"
printf 'curl %s (call %s)\n' "$url" "$n" >>"$FAKE_LOG_DIR/curl.calls"
case "$mode" in
policy-unreadable)
  echo "curl: (6) Could not resolve host" >&2
  exit 6
  ;;
esac
case "$url" in
*/config/policy/shared.json) ;;
*)
  echo "curl: unexpected url $url" >&2
  exit 1
  ;;
esac
file="$FAKE_JSON_DIR/policy-before.json"
if [ "$n" -ge 2 ]; then file="$FAKE_JSON_DIR/policy-after.json"; fi
[ -n "$out" ] || { echo "curl: no -o" >&2; exit 1; }
cat "$file" >"$out"
printf '200'
CURL_EOF
  chmod +x "$1/bin/curl"
}

write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check422.sh's test only. The
# `--help` probes are the check's feature gate, doctor and `craft ls` answer from
# the case's own documents, and `update` is the migration itself: it converts the
# config it was given the way MC4 does (a typed credential per named account and
# an access per craft), reports the run as data, and counts its own calls so a
# case can say how many converges happened.
set -euo pipefail
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}
usage() { printf 'Usage:\n  fugaro %s\n' "$1"; }

# migrate CONFIG: the accounts design's keys become the craft vocabulary, in
# place, the way internal/cli/init_craft_migrate.go does it — a claude-oauth
# credential per account and, per craft, the account its own stages run on (the
# stage's own key where it has one, the project's default where it has none).
# A file that already holds the vocabulary is left alone: the real migration only
# adds, and it adds nothing twice.
migrate() { # migrate CONFIG
  local config=$1 accounts="" default="" review="" want="" craft=""
  if grep -q '^credentials:' "$config"; then
    return 0
  fi
  accounts=$(awk '
    function ind(s) { match(s, /^[ ]*/); return RLENGTH }
    /^claude_accounts:/ { inb=1; level=-1; next }
    inb && /^[^ ]/ { inb=0 }
    inb && NF {
      if (level < 0) level = ind($0)
      if (ind($0) == level) { n=$1; sub(/:.*/, "", n); print n }
    }
  ' "$config")
  default=$(sed -n 's/^default_claude_account:[[:space:]]*//p' "$config" | head -n 1)
  review=$(sed -n 's/^[[:space:]]*claude_account:[[:space:]]*//p' "$config" | head -n 1)
  [ -n "$accounts" ] || return 0
  {
    printf 'credentials:\n'
    for a in $accounts; do
      printf '    - name: %s\n      type: claude-oauth\n' "$a"
    done
    printf 'crafts:\n'
    for craft in coder review senior-review security-review; do
      case "$craft" in
      senior-review) want=${review:-$default} ;;
      *) want=$default ;;
      esac
      [ -n "$want" ] && printf '    %s:\n        access: %s\n' "$craft" "$want"
    done
  } >>"$config"
  return 0
}

case "${1:-}" in
version)
  printf '0.24.0\n'
  ;;
craft)
  [ "${2:-}" = "ls" ] || { echo "fugaro: unexpected craft subcommand ${2:-}" >&2; exit 1; }
  case "${3:-}" in
  --help)
    case "$mode" in
    no-feature) echo 'fugaro: unknown command "craft" for "fugaro"' >&2; exit 1 ;;
    esac
    usage "craft ls"
    ;;
  --json)
    case "$mode" in
    crafts-fails) printf 'fugaro: the crafts could not be read\n' >&2; exit 1 ;;
    crafts-unreadable) printf 'not json at all\n' ;;
    esac
    cat "$FAKE_JSON_DIR/crafts.json"
    ;;
  *)
    echo "fugaro: unexpected craft ls argument ${3:-}" >&2
    exit 1
    ;;
  esac
  ;;
update)
  case "$*" in
  *--help*)
    case "$mode" in
    no-json) printf 'Usage:\n  fugaro update [flags]\n' ;;
    *) printf 'Usage:\n  fugaro update [flags]\n\nFlags:\n      --json   print the result as JSON on stdout\n' ;;
    esac
    exit 0
    ;;
  esac
  n=1
  if [ -f "$FAKE_LOG_DIR/update.count" ]; then
    n=$(( $(cat "$FAKE_LOG_DIR/update.count") + 1 ))
  fi
  printf '%s\n' "$n" >"$FAKE_LOG_DIR/update.count"
  config=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --config)
      config=$2
      shift 2
      ;;
    *)
      shift
      ;;
    esac
  done
  case "$mode" in
  second-fails)
    if [ "$n" -ge 2 ]; then
      printf '{"project":"chk422","gcp_project":"fugaro-chk422-test","failed":{"stage":"images","error":"the image could not be built","fix":"run it again"},"left_for_you":[]}\n'
      exit 2
    fi
    ;;
  update-fails)
    printf '{"project":"chk422","gcp_project":"fugaro-chk422-test","failed":{"stage":"installation","error":"the apply failed","fix":"read the plan"},"left_for_you":[]}\n'
    exit 2
    ;;
  update-hangs)
    sleep 300
    ;;
  esac
  if [ "$mode" = update-unreadable ]; then
    printf 'not json at all\n'
    exit 0
  fi
  wrote=no
  case "$mode" in
  update-no-migration) ;;
  update-partial)
    # One account converted and the other left alone with NOTHING said about it,
    # which is the state MC4 forbids: what a migration cannot convert it names.
    if ! grep -q '^credentials:' "$config"; then
      {
        printf 'credentials:\n    - name: %s\n      type: claude-oauth\n' "$FAKE_ACCOUNT_ONE"
        printf 'crafts:\n    coder:\n        access: %s\n' "$FAKE_ACCOUNT_ONE"
      } >>"$config"
      wrote=yes
    fi
    ;;
  update-partial-note)
    if ! grep -q '^credentials:' "$config"; then
      {
        printf 'credentials:\n    - name: %s\n      type: claude-oauth\n' "$FAKE_ACCOUNT_ONE"
        printf 'crafts:\n    coder:\n        access: %s\n' "$FAKE_ACCOUNT_ONE"
      } >>"$config"
      wrote=yes
    fi
    printf 'note: the account %s, named by the review crafts, has no craft key: it is not a name a credential may carry\n' "$FAKE_ACCOUNT_TWO" >&2
    ;;
  update-rewrites)
    if [ "$n" -ge 2 ]; then
      # The second converge re-encodes the credentials block it wrote itself: the
      # same names, another text, which is what a migration that is not
      # idempotent leaves behind.
      sed 's/^      type: claude-oauth$/      type: claude-oauth # again/' "$config" >"$config.tmp"
      mv "$config.tmp" "$config"
      wrote=yes
    else
      migrate "$config"
      wrote=yes
    fi
    ;;
  *)
    migrate "$config"
    wrote=yes
    ;;
  esac
  notes='[]'
  case "$mode" in
  update-second-notes)
    if [ "$n" -ge 2 ]; then
      printf 'note: the account %s, named by the review crafts, has no craft key: no credential of this release stands for it\n' "$FAKE_ACCOUNT_TWO" >&2
    fi
    ;;
  update-partial-note)
    ;;
  esac
  left='[]'
  status=0
  failed=''
  case "$mode" in
  update-left)
    left='[{"stage":"policy","kind":"command","text":"fugaro project policy publish"}]'
    status=1
    ;;
  update-needs-you)
    left='[]'
    notes='[]'
    ;;
  update-error)
    printf '{"project":"chk422","error":"no project config is selected","left_for_you":[]}\n'
    exit 1
    ;;
  update-other-config)
    config=/tmp/somewhere-else.yaml
    ;;
  esac
  changed=1
  applied=true
  if [ "$n" -ge 2 ]; then
    changed=0
    applied=false
  fi
  if [ "$mode" = update-second-applies ]; then
    applied=true
    changed=2
  fi
  backup=""
  if [ "$wrote" = yes ] && [ "$n" -lt 2 ]; then backup="$config.bak-20261114"; fi
  if [ "$mode" = update-needs-you ]; then
    printf '{"project":"chk422","gcp_project":"fugaro-chk422-test","config":"%s","applied":false,"stages":[{"name":"installation","state":"done"},{"name":"images","state":"needs-you"}],"left_for_you":[]}\n' "$config"
    exit 0
  fi
  printf '{"project":"chk422","gcp_project":"fugaro-chk422-test","config":"%s","backup":"%s","applied":%s,"changes":{"imports":0,"creates":0,"updates":%s,"deletes":0,"replaces":0},"stages":[{"name":"installation","state":"done"},{"name":"policy","state":"done"}],"left_for_you":%s%s}\n' \
    "$config" "$backup" "$applied" "$changed" "$left" "$failed"
  exit "$status"
  ;;
doctor)
  case "$mode" in
  doctor-fails) printf 'fugaro: no installation here\n' >&2; exit 1 ;;
  doctor-unreadable) printf 'not json at all\n' ;;
  esac
  cat "$FAKE_JSON_DIR/doctor.json"
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
write_docs() { # write_docs DIR
  local dir=$1 json=$1/json
  mkdir -p "$json"
  write_doctor "$dir"
  write_crafts "$dir"
  write_policy "$dir" unchanged
}

write_doctor() { # write_doctor DIR [NAME PROJECT]
  cat >"$1/json/doctor.json" <<JSON
{"version":1,"ok":true,"checks":[],"project":{"name":"${2:-$name}","gcp_project":"${3:-$project}","region":"us-east5"}}
JSON
}

# write_config DIR STATE: the local config the check converges, at the path its
# own default derives — $XDG_CONFIG_HOME/fugaro/projects/<the name doctor
# answers>. `previous` is the shape MC4 converts; `converged` is the same
# installation after a converge, which the check asserts without watching the
# migration run; the other two states are a file this release does not write and
# an installation with no database.
write_config() { # write_config DIR STATE
  local dir=$1 state=$2 path=$1/xdg/fugaro/projects/$name.yaml
  mkdir -p "$(dirname "$path")"
  case "$state" in
  version-1)
    cat >"$path" <<YAML
version: 1
name: $name
gcp_project: $project
region: us-east5
YAML
    return 0
    ;;
  esac
  cat >"$path" <<YAML
version: 2
name: $name
gcp_project: $project
region: us-east5
terraform:
    runs_bucket: fugaro-runs-$project
YAML
  if [ "$state" != no-rtdb ]; then
    cat >>"$path" <<YAML
budget:
    rtdb_url: https://$project-default-rtdb.us-east5.firebasedatabase.app/
YAML
  fi
  if [ "$state" = converged ]; then
    cat >>"$path" <<YAML
credentials:
    - name: $account_one
      type: claude-oauth
    - name: $account_two
      type: claude-oauth
crafts:
    coder:
        access: $account_one
    review:
        access: $account_one
    senior-review:
        access: $account_two
    security-review:
        access: $account_one
YAML
    return 0
  fi
  cat >>"$path" <<YAML
claude_accounts:
    $account_one:
        allow_data_to:
            - acme/sandbox
    $account_two:
        allow_data_to:
            - acme/sandbox
default_claude_account: $account_one
stages:
    review:
        claude_account: $account_two
YAML
}

# write_crafts DIR [ACCESS] [PROBLEMS]: `craft ls --json`'s document. The default
# access is the one the migration derives from the fixture's accounts, so the row
# that reads the crafts back is reading the same installation the config
# describes; a case bends either half.
write_crafts() { # write_crafts DIR [ACCESS] [PROBLEMS]
  local access=${2:-coder=$account_one,review=$account_one,senior-review=$account_two,security-review=$account_one}
  local problems=${3:-[]} rows="" pair craft value
  for pair in ${access//,/ }; do
    craft=${pair%%=*}
    value=${pair#*=}
    rows="$rows${rows:+,}{\"craft\":\"$craft\",\"model\":\"claude-opus-4-1\",\"model_from\":\"local\",\"access\":\"$value\",\"access_from\":\"local\",\"inert\":false}"
  done
  cat >"$1/json/crafts.json" <<JSON
{"version":1,"project":"chk422","crafts":[$rows],"problems":$problems}
JSON
}

# write_policy DIR STATE: the published policy node, as the database itself
# answers it, before and after the converges. `unchanged` is the state the check
# measures — the migration writes the local config and no command of this release
# writes these members — and every other state is one a converge would be a
# finding in, or one there is nothing to read.
write_policy() { # write_policy DIR STATE
  local dir=$1 state=$2 before after
  before=$(policy_node 7)
  after=$(policy_node 7)
  case "$state" in
  unchanged) ;;
  migrated) after=$(policy_node 8) ;;
  rewritten) after=$(policy_node 7 "$account_two") ;;
  gone) after='null' ;;
  unpublished)
    before='null'
    after='null'
    ;;
  esac
  printf '%s\n' "$before" >"$1/json/policy-before.json"
  printf '%s\n' "$after" >"$1/json/policy-after.json"
}

# policy_node VERSION [ACCESS]: one published node, with the three members no
# command of this release writes. The generation is the argument so a case can
# move it alone.
policy_node() { # policy_node VERSION [ACCESS]
  local version=$1 access=${2:-$account_one}
  cat <<JSON
{"version":$version,"crafts":{"coder":{"model":"claude-opus-4-1","access":"$access"}},"craft_credentials":[{"name":"$account_one","type":"claude-oauth"}],"craft_pools":[]}
JSON
}

prepare() { # dir
  mkdir -p "$1/home" "$1/state" "$1/logs" "$1/tmp"
  write_fake_gcloud "$1"
  write_fake_curl "$1"
  write_fake_fugaro "$1"
  write_docs "$1"
  write_config "$1" previous
  # The account names the fake's partial migrations name in the note they owe:
  # they are the fixture's, and the fake has no reader of its own to take them
  # from.
  printf '%s\n' "$account_one" >"$1/state/account-one"
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
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/curl.calls"
  # The two counters are this case's, not the directory's: a case that runs the
  # check twice (the mutations, which run a copy and then the real script) has to
  # see the same run both times.
  rm -f "$dir/logs/update.count" "$dir/logs/curl.count"
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
    XDG_CONFIG_HOME="$dir/xdg" \
    FAKE_LOG_DIR="$dir/logs" FAKE_MODE="${FAKE_MODE:-default}" \
    FAKE_JSON_DIR="$dir/json" FAKE_ACCOUNT_ONE="$account_one" FAKE_ACCOUNT_TWO="$account_two" \
    CHECK422_TIMEOUT="${CHECK422_TIMEOUT:-30}" \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$target" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  config=$dir/xdg/fugaro/projects/$name.yaml
}

# ------------------------------------------------------------------ assertions
assert_scratch_removed() { # dir what
  [ -n "$scratch" ] || {
    fail "$2: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$2: the scratch directory $scratch is still there"
}

assert_no_update() { # dir what
  expect_absent "$(cat "$1/logs/fugaro.calls")" "update --yes" "$2: no converge ran"
}

assert_update_count() { # dir want what
  local got
  got=$(grep -c "update --yes" "$1/logs/fugaro.calls" || true)
  [ "$got" = "$2" ] || fail "$3: expected $2 converge(s), got $got"
}

assert_node_reads() { # dir want what
  local got
  got=$(grep -c "^curl " "$1/logs/curl.calls" || true)
  [ "$got" = "$2" ] || fail "$3: expected $2 read(s) of the node, got $got"
}

assert_no_node_read() { # dir what
  expect_absent "$(cat "$1/logs/curl.calls")" "curl " "$2: the node was read"
}

# assert_config: one reading of the config the check converged, as its own file
# carries it after the run.
assert_config_has() { # config needle what
  grep -q "$2" "$1" || fail "$3: the config does not carry '$2'"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "bash scripts/live-checks/check422.sh" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  # The header is what a person reads before running it: it has to say, where
  # they will see it, what the check costs and that it is theirs.
  expect_contains "$out" "two converges of a sandbox installation" "--help: the cost is stated"
  expect_contains "$out" "by YOU. No agent runs any step of it" "--help: who runs it"
  assert_no_update "$dir" "--help"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK422_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "CHECK 422 RESULT" "dry run: the block"
  expect_contains "$out" "CHECK 422 DRY RUN" "dry run: the plan"
  expect_contains "$out" "FACT fugaro-version: INFO" "dry run: the version row is a reading"
  expect_contains "$out" "FACT xtrace: PASS" "dry run: the trace guard"
  expect_contains "$out" "FACT agent-session: PASS" "dry run: the session guard"
  expect_contains "$out" "FACT installation: PASS — the installation in force is $name ($project" "dry run: the installation was read"
  expect_contains "$out" "FACT config-before: PASS" "dry run: the config was read"
  expect_contains "$out" "FACT update: INFO — planned" "dry run: the converge is planned, not run"
  expect_contains "$out" "FACT restore: PASS — a dry run" "dry run: the restore row says nothing was run"
  assert_no_update "$dir" "dry run"
  assert_no_node_read "$dir" "dry run"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "print-access-token" "dry run: no token was printed"
  assert_scratch_removed "$dir" "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the happy path"
  # Every row of the block, in the order the block prints them.
  expect_contains "$out" "FACT fugaro-version: INFO — 0.24.0" "the block: the version"
  expect_contains "$out" "FACT features: PASS" "the block: the feature gate"
  expect_contains "$out" "FACT xtrace: PASS" "the block: the trace guard"
  expect_contains "$out" "FACT agent-session: PASS" "the block: the session guard"
  expect_contains "$out" "FACT belong: PASS" "the block: the sandbox guard"
  expect_contains "$out" "FACT bound: PASS — TWO converges of the $name installation ($project)" "the block: what it costs"
  expect_contains "$out" "FACT installation: PASS" "the block: the installation"
  expect_contains "$out" "FACT config-before: PASS" "the block: the shape before"
  expect_contains "$out" "the accounts design's keys are still here" "the block: the previous shape is named"
  expect_contains "$out" "FACT update: PASS — the converge exited 0 over 2 stage(s)" "the block: the converge"
  expect_contains "$out" "it converted an installation whose config still held the accounts design" "the block: what the converge had to migrate"
  expect_contains "$out" "FACT no-step-left: PASS" "the block: THE row"
  expect_contains "$out" "left_for_you empty and no stage needing you" "THE row: what it read"
  expect_contains "$out" "FACT config-after: PASS" "the block: the file after the migration"
  expect_contains "$out" "$account_one" "the block: the credential the migration wrote is named"
  expect_contains "$out" "The older keys are still there" "the block: the migration only adds"
  expect_contains "$out" "FACT crafts: PASS" "the block: the crafts resolve"
  expect_contains "$out" "FACT policy: PASS" "the block: the published node"
  expect_contains "$out" "FACT second-update: PASS" "the block: the idempotence"
  expect_contains "$out" "planned no change at all" "the idempotence row: the empty plan"
  expect_contains "$out" "FACT restore: PASS" "the block: the restore"
  expect_contains "$out" "migration it wrote into" "the restore row: the migration stays"
  expect_contains "$out" "FACT scratch-removed: PASS" "the block: the scratch directory"
  expect_absent "$out" "FAIL" "the happy path: no row failed"
  # And the check really converged: two runs of the same command, the node read
  # once before and once after, and the file migrated.
  assert_update_count "$dir" 2 "the happy path"
  assert_node_reads "$dir" 2 "the happy path"
  assert_config_has "$config" "type: claude-oauth" "the happy path: the typed credential"
  assert_config_has "$config" "crafts:" "the happy path: the craft accesses"
  assert_config_has "$config" "access: $account_two" "the happy path: the craft that follows the other account"
  assert_scratch_removed "$dir" "the happy path"
}

test_a_converged_installation_asserts_the_end_state() {
  local dir=$work/converged
  prepare "$dir"
  write_config "$dir" converged
  run_check "$dir"
  expect_status "$status" 0 "an installation that already converged"
  expect_contains "$out" "FACT config-before: PASS" "the before row"
  expect_contains "$out" "converged already" "the before row: it says so"
  expect_contains "$out" "FACT config-after: INFO" "the after row is a reading and not a verdict"
  expect_contains "$out" "there was no migration to watch" "the after row: why"
  expect_contains "$out" "FACT second-update: PASS" "the idempotence still holds"
  assert_update_count "$dir" 2 "an installation that already converged"
}

test_a_config_of_another_installation_is_refused() {
  local dir=$work/other-install
  prepare "$dir"
  mkdir -p "$dir/elsewhere"
  write_config "$dir" previous
  sed 's/^gcp_project: .*/gcp_project: another-project/' "$dir/xdg/fugaro/projects/$name.yaml" >"$dir/elsewhere/config.yaml"
  run_check "$dir" -- --config "$dir/elsewhere/config.yaml"
  expect_status "$status" 2 "a config of another installation"
  expect_contains "$out" "FACT installation: FAIL" "the installation row"
  expect_contains "$out" "fugaro doctor answers $name ($project)" "the installation row: the two projects"
  assert_no_update "$dir" "a config of another installation"
}

test_the_config_named_is_the_one_converged() {
  local dir=$work/named-config
  prepare "$dir"
  mkdir -p "$dir/elsewhere"
  cp "$dir/xdg/fugaro/projects/$name.yaml" "$dir/elsewhere/config.yaml"
  run_check "$dir" -- --config "$dir/elsewhere/config.yaml"
  expect_status "$status" 0 "a config named with --config"
  expect_contains "$out" "FACT installation: PASS — the installation in force is $name ($project" "the installation row"
  expect_contains "$out" "named with --config" "it says where the config came from"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "--config $dir/elsewhere/config.yaml" "the converge names the file"
  assert_config_has "$dir/elsewhere/config.yaml" "type: claude-oauth" "the named file was migrated"
}

test_a_config_that_is_not_there_is_refused() {
  local dir=$work/no-config
  prepare "$dir"
  rm -f "$dir/xdg/fugaro/projects/$name.yaml"
  run_check "$dir"
  expect_status "$status" 2 "no local config"
  expect_contains "$out" "FACT installation: FAIL" "the installation row"
  expect_contains "$out" "is not a file" "the installation row: what is missing"
  assert_no_update "$dir" "no local config"
}

test_a_config_of_another_version_is_refused() {
  local dir=$work/version-1
  prepare "$dir"
  write_config "$dir" version-1
  run_check "$dir"
  expect_status "$status" 2 "a version-1 config"
  expect_contains "$out" "FACT config-before: FAIL" "the before row"
  expect_contains "$out" "states version 1" "the before row: the version"
  assert_no_update "$dir" "a version-1 config"
}

test_an_installation_naming_belong_is_refused() {
  local dir=$work/belong-flag
  prepare "$dir"
  run_check "$dir" -- --installation belong-2
  expect_status "$status" 2 "an installation naming belong"
  expect_contains "$out" "FACT belong: FAIL" "the belong row"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "a refused name runs nothing"
  assert_scratch_removed "$dir" "a refused name"
}

test_a_project_doctor_answers_as_belong_is_refused() {
  local dir=$work/belong-doctor
  prepare "$dir"
  write_doctor "$dir" "$name" belong-2
  run_check "$dir"
  expect_status "$status" 2 "doctor answering belong"
  expect_contains "$out" "FACT belong: FAIL" "the belong row"
  expect_contains "$out" "belong" "the belong row: the name"
  assert_no_update "$dir" "doctor answering belong"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- -x
  expect_status "$status" 2 "a traced shell"
  expect_contains "$out" "FACT xtrace: FAIL" "the trace guard"
  expect_contains "$out" "this shell is tracing" "the trace guard: why"
  assert_no_update "$dir" "a traced shell"
}

test_a_coding_agents_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 2 "a coding agent's session"
  expect_contains "$out" "FACT agent-session: FAIL" "the session guard"
  expect_contains "$err" "CLAUDECODE is set, so this is a coding agent's session" "the session guard: the marker"
  assert_no_update "$dir" "a coding agent's session"
}

test_a_build_without_the_crafts_vocabulary_is_refused() {
  local dir=$work/no-feature
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-feature
  expect_status "$status" 2 "a build without the crafts"
  expect_contains "$out" "FACT features: FAIL" "the feature row"
  expect_contains "$out" "has no 'fugaro craft ls'" "the feature row: what is missing"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "doctor" "a refused build reads no installation"
  assert_no_update "$dir" "a build without the crafts"
}

test_a_build_whose_update_prints_no_json_is_refused() {
  local dir=$work/no-json
  prepare "$dir"
  run_check "$dir" FAKE_MODE=no-json
  expect_status "$status" 2 "a build whose update has no --json"
  expect_contains "$out" "FACT features: FAIL" "the feature row"
  expect_contains "$out" "fugaro update --json" "the feature row: the flag it needs"
  assert_no_update "$dir" "a build whose update has no --json"
}

test_a_doctor_that_cannot_be_read_is_not_measured() {
  local dir=$work/doctor-fails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-fails
  expect_status "$status" 2 "doctor that failed"
  expect_contains "$out" "FACT installation: FAIL" "the installation row"
  expect_contains "$out" "exited 1" "the installation row: the exit"
  assert_no_update "$dir" "doctor that failed"
  assert_scratch_removed "$dir" "doctor that failed"
}

test_a_doctor_that_is_not_a_document_is_not_measured() {
  local dir=$work/doctor-unreadable
  prepare "$dir"
  run_check "$dir" FAKE_MODE=doctor-unreadable
  expect_status "$status" 2 "doctor that printed no document"
  expect_contains "$out" "FACT installation: FAIL" "the installation row"
  assert_no_update "$dir" "doctor that printed no document"
}

test_a_converge_that_fails_stops_the_run() {
  local dir=$work/update-fails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-fails
  expect_status "$status" 2 "a converge that failed"
  expect_contains "$out" "FACT update: FAIL" "the update row"
  expect_contains "$out" "exited 2, at the stage installation" "the update row: the stage"
  expect_contains "$out" "FACT no-step-left: FAIL — not reached" "the rows below a failed converge are not measured"
  assert_update_count "$dir" 1 "a converge that failed"
  assert_scratch_removed "$dir" "a converge that failed"
}

test_a_converge_that_leaves_a_step_is_a_finding() {
  local dir=$work/update-left
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-left
  expect_status "$status" 2 "a converge that left a step"
  expect_contains "$out" "FACT update: FAIL — fugaro update exited 1" "the update row"
  expect_contains "$out" "FACT no-step-left: FAIL" "THE row: a left-for-you step is a finding"
  expect_contains "$out" "left 1 step(s) for you, at the stage(s) policy" "THE row: the stage"
  expect_contains "$out" "must leave nothing for a person" "THE row: why"
  expect_contains "$out" "FACT config-after: FAIL — not reached" "nothing after a half-converged installation is measured"
  assert_config_has "$config" "claude_accounts:" "a converge that left a step: the file is as the converge left it"
}

test_a_converge_whose_own_answer_cannot_be_read_is_not_measured() {
  local dir=$work/update-unreadable
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-unreadable
  expect_status "$status" 2 "a converge whose document cannot be read"
  expect_contains "$out" "FACT update: FAIL" "the update row"
  expect_contains "$out" "printed no --json document" "the update row: what is missing"
  expect_contains "$out" "FACT no-step-left: FAIL — not reached" "nothing below a converge this check cannot read is measured"
  assert_scratch_removed "$dir" "a converge whose document cannot be read"
}

test_a_converge_that_writes_another_config_is_a_finding() {
  local dir=$work/other-config
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-other-config
  expect_status "$status" 2 "a converge that wrote another config"
  expect_contains "$out" "FACT update: FAIL — the converge exited 0 and wrote the local config" "the update row"
  expect_contains "$out" "the rows below read the file the run did not write" "the update row: why it stops"
}

test_a_converge_that_migrates_nothing_is_a_finding() {
  local dir=$work/no-migration
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-no-migration
  expect_status "$status" 1 "a converge that migrated nothing"
  expect_contains "$out" "FACT config-after: FAIL" "the after row"
  expect_contains "$out" "still holds no typed credential" "the after row: what did not happen"
  expect_absent "$out" "FACT config-after: PASS" "the after row: it is a finding and not a pass"
  assert_update_count "$dir" 2 "a converge that migrated nothing"
}

test_an_account_left_unconverted_without_a_note_is_a_finding() {
  local dir=$work/partial
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-partial
  expect_status "$status" 1 "an account left unconverted with nothing said"
  expect_contains "$out" "FACT config-after: FAIL" "the after row"
  expect_contains "$out" "the accounts $account_two hold no credential" "the after row: the account that went nowhere"
  expect_contains "$out" "printed no note about them" "the after row: what the migration owes"
}

test_an_account_left_unconverted_with_a_note_is_not_a_finding() {
  local dir=$work/partial-note
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-partial-note
  expect_status "$status" 0 "an account left unconverted and named by a note"
  expect_contains "$out" "FACT config-after: PASS" "the after row"
  expect_contains "$out" "left unconverted and named by a note of the run itself)" "the after row: the note is read as the reason"
  expect_contains "$out" "FACT second-update: PASS" "the idempotence still holds"
}

test_a_second_converge_that_moves_the_keys_is_a_finding() {
  local dir=$work/rewrites
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-rewrites
  expect_status "$status" 1 "a second converge that re-encoded the keys"
  expect_contains "$out" "FACT second-update: FAIL" "the idempotence row"
  expect_contains "$out" "is not the one the first converge wrote" "the idempotence row: what moved"
  expect_absent "$out" "FACT second-update: PASS" "the idempotence row: it is a finding and not a pass"
}

test_a_second_converge_that_changes_its_notes_is_a_finding() {
  local dir=$work/second-notes
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-second-notes
  expect_status "$status" 1 "a second converge that said something new"
  expect_contains "$out" "FACT second-update: FAIL" "the idempotence row"
  expect_contains "$out" "the first printed 0" "the idempotence row: the two note counts"
}

test_a_second_converge_that_fails_is_a_finding() {
  local dir=$work/second-fails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=second-fails
  expect_status "$status" 1 "a second converge that failed"
  expect_contains "$out" "FACT second-update: FAIL" "the idempotence row"
  expect_contains "$out" "the second fugaro update exited 2, at the stage images" "the idempotence row: the stage"
  assert_update_count "$dir" 2 "a second converge that failed"
}

test_a_second_converge_that_applies_something_is_reported() {
  local dir=$work/second-applies
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-second-applies
  expect_status "$status" 0 "a second converge that applied a change"
  expect_contains "$out" "FACT second-update: PASS" "the idempotence row: the keys did not move"
  expect_contains "$out" "the second converge's planned change was 0/0/2/0/0" "the idempotence row: what it planned is said"
}

test_a_stage_needing_you_is_a_finding() {
  local dir=$work/needs-you
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-needs-you
  expect_status "$status" 1 "a stage that needs the person while left_for_you is empty"
  expect_contains "$out" "FACT no-step-left: FAIL" "THE row"
  expect_contains "$out" "ended needs-you/blocked/failed while left_for_you is empty" "THE row: the two halves disagree"
}

test_a_crafts_list_with_problems_is_a_finding() {
  local dir=$work/craft-problems
  prepare "$dir"
  write_crafts "$dir" "" '["the coder craft names no credential"]'
  run_check "$dir"
  expect_status "$status" 1 "a crafts list with problems"
  expect_contains "$out" "FACT crafts: FAIL" "the crafts row"
  expect_contains "$out" "reports 1 problem(s) after the converge" "the crafts row: the problem"
}

test_a_craft_that_does_not_resolve_as_the_config_writes_it_is_a_finding() {
  local dir=$work/craft-mismatch
  prepare "$dir"
  write_crafts "$dir" "coder=$account_two,review=$account_one,senior-review=$account_two,security-review=$account_one"
  run_check "$dir"
  expect_status "$status" 1 "a craft that resolves to another access"
  expect_contains "$out" "FACT crafts: FAIL" "the crafts row"
  expect_contains "$out" "do not resolve as they are written" "the crafts row: what disagrees"
}

test_a_crafts_list_that_cannot_be_read_is_not_measured() {
  local dir=$work/crafts-fails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=crafts-fails
  expect_status "$status" 2 "a crafts list that cannot be read"
  expect_contains "$out" "FACT crafts: FAIL" "the crafts row"
  expect_contains "$out" "did not answer" "the crafts row: why it is not measured"
  expect_contains "$err" "could NOT measure crafts" "the run says the row was not measured"
  expect_contains "$out" "FACT config-after: PASS" "the rows before it are still measured"
}

test_a_policy_whose_craft_members_moved_is_a_finding() {
  local dir=$work/policy-rewritten
  prepare "$dir"
  write_policy "$dir" rewritten
  run_check "$dir"
  expect_status "$status" 1 "a converge that rewrote the published crafts"
  expect_contains "$out" "FACT policy: FAIL" "the policy row"
  expect_contains "$out" "craft members (crafts, craft_credentials, craft_pools) changed" "the policy row: what moved"
  expect_contains "$out" "rewrote operator-written policy" "the policy row: why it matters"
}

test_a_policy_the_converge_removed_is_a_finding() {
  local dir=$work/policy-gone
  prepare "$dir"
  write_policy "$dir" gone
  run_check "$dir"
  expect_status "$status" 1 "a converge that removed the published policy"
  expect_contains "$out" "FACT policy: FAIL" "the policy row"
  expect_contains "$out" "is gone after it" "the policy row: what happened"
}

test_a_policy_whose_generation_moved_is_not_a_finding() {
  local dir=$work/policy-migrated
  prepare "$dir"
  write_policy "$dir" migrated
  run_check "$dir"
  expect_status "$status" 0 "a policy migrated in place"
  expect_contains "$out" "FACT policy: PASS" "the policy row"
  expect_contains "$out" "its generation moved 7 → 8" "the policy row: the migration is named"
  expect_contains "$out" "craft members unchanged" "the policy row: the members did not move"
}

test_an_unpublished_node_is_not_a_finding() {
  local dir=$work/policy-unpublished
  prepare "$dir"
  write_policy "$dir" unpublished
  run_check "$dir"
  expect_status "$status" 0 "an installation with no published policy"
  expect_contains "$out" "FACT policy: PASS" "the policy row"
  expect_contains "$out" "no published policy node" "the policy row: what it found"
}

test_a_policy_that_cannot_be_read_is_not_measured() {
  local dir=$work/policy-unreadable
  prepare "$dir"
  run_check "$dir" FAKE_MODE=policy-unreadable
  expect_status "$status" 2 "a policy node that cannot be read"
  expect_contains "$out" "FACT policy: FAIL" "the policy row"
  expect_contains "$out" "could not be read before the converge" "the policy row: why"
  expect_contains "$err" "could NOT measure policy" "the run says the row was not measured"
  expect_contains "$out" "FACT config-after: PASS" "the migration is still measured without the node"
  expect_contains "$out" "FACT second-update: PASS" "and so is the idempotence"
}

test_a_token_that_cannot_be_printed_is_not_measured() {
  local dir=$work/token-fails
  prepare "$dir"
  run_check "$dir" FAKE_MODE=token-fails
  expect_status "$status" 2 "a token that cannot be printed"
  expect_contains "$out" "FACT policy: FAIL" "the policy row"
  expect_contains "$err" "could NOT measure policy" "the run says the row was not measured"
}

test_an_installation_with_no_backend_has_no_policy() {
  local dir=$work/no-rtdb
  prepare "$dir"
  write_config "$dir" no-rtdb
  run_check "$dir"
  expect_status "$status" 0 "an installation with no Realtime Database"
  expect_contains "$out" "FACT policy: INFO" "the policy row is a reading"
  expect_contains "$out" "no budget.rtdb_url" "the policy row: why there is no node"
  assert_no_node_read "$dir" "an installation with no Realtime Database"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "print-access-token" "no token is printed with no node to read"
}

test_a_converge_over_the_bound_is_not_measured() {
  local dir=$work/timeout
  prepare "$dir"
  run_check "$dir" FAKE_MODE=update-hangs CHECK422_TIMEOUT=2
  expect_status "$status" 2 "a converge that never finished"
  expect_contains "$out" "FACT update: FAIL" "the update row"
  expect_contains "$out" "did not finish within 2s" "the update row: the bound"
  expect_contains "$out" "FACT config-after: FAIL — not reached" "nothing below a converge that was killed is measured"
  assert_scratch_removed "$dir" "a converge that never finished"
}

test_a_kept_scratch_directory_is_reported() {
  local dir=$work/keep
  prepare "$dir"
  run_check "$dir" CHECK422_KEEP_SCRATCH=1
  # The kept directory is a FAILed row of its own — the check can then say the
  # directory did NOT go — so the run ends as one that failed, which is the point
  # of asking for it: KEEP is for reading what a run left, not a clean run.
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "the scratch row"
  expect_contains "$out" "still there" "the scratch row: it names the directory"
  [ -n "$scratch" ] && [ -e "$scratch" ] && rm -rf "$scratch"
}

test_an_unknown_argument_is_refused() {
  local dir=$work/args
  prepare "$dir"
  run_check "$dir" -- --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --nonsense" "the refusal"
  assert_no_update "$dir" "an unknown argument"
}

test_a_timeout_that_is_not_a_number_is_refused() {
  local dir=$work/bad-timeout
  prepare "$dir"
  run_check "$dir" CHECK422_TIMEOUT=soon
  expect_status "$status" 2 "a timeout that is not a number"
  expect_contains "$err" "must be a whole number of seconds" "the refusal"
  assert_no_update "$dir" "a timeout that is not a number"
}

# The two rows that carry the check. Each is a COPY of the script with that one
# row stubbed out to a PASS, run against the fixture the case above fails: if the
# row were not load-bearing, the copy would report the row as a pass on a
# converge that left a step for the person (or migrated nothing) and this test
# would say so.
test_the_no_step_left_row_is_load_bearing() {
  local dir=$work/mutate-left
  prepare "$dir"
  mkdir -p "$dir/copy"
  sed 's/^    fact no-step-left FAIL "/    fact no-step-left PASS "/' "$script" >"$dir/copy/check422.sh"
  grep -q 'fact no-step-left PASS "the converge left' "$dir/copy/check422.sh" || {
    fail "the mutation: the copy was not mutated (the pattern no longer matches)"
    return 0
  }
  run_copy "$dir" "$dir/copy/check422.sh" FAKE_MODE=update-left
  expect_contains "$out" "FACT no-step-left: PASS" "the mutation: the copy passes the row"
  expect_absent "$out" "FACT no-step-left: FAIL" "the mutation: the broken copy does not fail the row"
  # And the real script, on the same fixture, fails it — which is the assertion
  # the copy above shows would have been missed.
  run_check "$dir" FAKE_MODE=update-left
  expect_status "$status" 2 "the real script: the run stops under the row"
  expect_contains "$out" "FACT no-step-left: FAIL" "the real script: the row"
}

test_the_migration_row_is_load_bearing() {
  local dir=$work/mutate-migration
  prepare "$dir"
  mkdir -p "$dir/copy"
  sed 's/^    fact config-after FAIL "/    fact config-after PASS "/' "$script" >"$dir/copy/check422.sh"
  grep -q 'fact config-after PASS "the config at' "$dir/copy/check422.sh" || {
    fail "the mutation: the copy was not mutated (the pattern no longer matches)"
    return 0
  }
  run_copy "$dir" "$dir/copy/check422.sh" FAKE_MODE=update-no-migration
  expect_contains "$out" "FACT config-after: PASS" "the mutation: the copy passes the row"
  expect_absent "$out" "FACT config-after: FAIL" "the mutation: the broken copy does not fail the row"
  expect_status "$status" 0 "the mutation: the broken copy ends clean on a converge that migrated nothing"
  run_check "$dir" FAKE_MODE=update-no-migration
  expect_status "$status" 1 "the real script: the row fails"
  expect_contains "$out" "FACT config-after: FAIL" "the real script: the row"
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
t test_a_converged_installation_asserts_the_end_state
t test_a_config_of_another_installation_is_refused
t test_the_config_named_is_the_one_converged
t test_a_config_that_is_not_there_is_refused
t test_a_config_of_another_version_is_refused
t test_an_installation_naming_belong_is_refused
t test_a_project_doctor_answers_as_belong_is_refused
t test_xtrace_refused
t test_a_coding_agents_session_is_refused
t test_a_build_without_the_crafts_vocabulary_is_refused
t test_a_build_whose_update_prints_no_json_is_refused
t test_a_doctor_that_cannot_be_read_is_not_measured
t test_a_doctor_that_is_not_a_document_is_not_measured
t test_a_converge_that_fails_stops_the_run
t test_a_converge_that_leaves_a_step_is_a_finding
t test_a_converge_whose_own_answer_cannot_be_read_is_not_measured
t test_a_converge_that_writes_another_config_is_a_finding
t test_a_converge_that_migrates_nothing_is_a_finding
t test_an_account_left_unconverted_without_a_note_is_a_finding
t test_an_account_left_unconverted_with_a_note_is_not_a_finding
t test_a_second_converge_that_moves_the_keys_is_a_finding
t test_a_second_converge_that_changes_its_notes_is_a_finding
t test_a_second_converge_that_fails_is_a_finding
t test_a_second_converge_that_applies_something_is_reported
t test_a_stage_needing_you_is_a_finding
t test_a_crafts_list_with_problems_is_a_finding
t test_a_craft_that_does_not_resolve_as_the_config_writes_it_is_a_finding
t test_a_crafts_list_that_cannot_be_read_is_not_measured
t test_a_policy_whose_craft_members_moved_is_a_finding
t test_a_policy_the_converge_removed_is_a_finding
t test_a_policy_whose_generation_moved_is_not_a_finding
t test_an_unpublished_node_is_not_a_finding
t test_a_policy_that_cannot_be_read_is_not_measured
t test_a_token_that_cannot_be_printed_is_not_measured
t test_an_installation_with_no_backend_has_no_policy
t test_a_converge_over_the_bound_is_not_measured
t test_a_kept_scratch_directory_is_reported
t test_an_unknown_argument_is_refused
t test_a_timeout_that_is_not_a_number_is_refused
t test_the_no_step_left_row_is_load_bearing
t test_the_migration_row_is_load_bearing

if [ "$failures" -ne 0 ]; then
  echo "check422_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check422_test.sh: ok"
