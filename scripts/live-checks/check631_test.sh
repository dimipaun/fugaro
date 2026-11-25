#!/usr/bin/env bash
# Tests for scripts/live-checks/check631.sh, with a fake `fugaro`, a fake
# `gcloud` and (for the cases about the terminal) a real pty on PATH:
# NOTHING here reaches a cloud, an installation, a secret or a run, no credential
# is declared anywhere, nothing is published, and the fakes record every call
# they are given — in which directory it was made — so a case can assert what the
# check did and did not do.
#
# The model the fakes carry is one installation at the current release: its
# config declares a craft whose access names a pool, its credentials listing
# names each credential's serving path, its pools listing answers from the state
# the check's own pool add writes, and its record and the credentials service's
# audit line both name the credential the check declared. The cases that matter
# are the ones where that model is bent: a command that echoes the value it was
# handed, the value written into the config directory and into a shell history, a
# session that is not refused, a refusal that names no step, a pool write that
# fails, a publish that fails, a launch that fails, a run that never ends, a
# record that is another run's, an audit line that names another credential, a
# removal that is accepted while a pool lists the credential, and a craft with no
# access to take over.
#
# The two pastes are real pastes: the terminal cases start a pty (python3's
# pty.openpty) and hand the check its slave device through CHECK631_TTY, with the
# two values written into it — the check's own hidden prompt reads the first and
# the command's own hidden prompt reads the second, in the order the check makes
# them. A value never becomes an argument of any process here either.
#
# Usage: bash scripts/live-checks/check631_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
LIVE_CHECK_LIB=${LIVE_CHECK_LIB:-$here/lib}
script=$here/check631.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK631_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK631_TEST_HEAD:-2000}")" ;;
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

project=fugaro-chk631-test
installation=chk631
anthropic=chk631-anthropic
openrouter=chk631-openrouter
pool=chk631-pool
craft=coder
owner=owner@example.com
run_id=r-631
# The two values the check is handed. Neither is shaped like a provider key: the
# shape sweep of the prompt row is the one place a key-shaped string is needed,
# and that case builds it from parts (see test_a_prompt_that_echoes_a_key_shape).
value_one=chk631-not-a-real-key-one
value_two=chk631-not-a-real-key-two

# --------------------------------------------------------------------- fakes
write_fake_fugaro() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for `fugaro`, for scripts/live-checks/check631.sh's test only. What
# the check reads back — the credentials listing, the craft, the pools listing,
# the node, the run's row, its record and the audit line — comes from the state
# files the case wrote, and every call is logged with the directory it was made
# in. The two declarations are the state: a configured `accounts add` appends the
# credential it was asked for, and `accounts rm` takes it away again (unless the
# mode says the removal does not stick).
set -euo pipefail
. "$LIVE_CHECK_LIB/strict.sh"
strict_fugaro_verify "$@" || exit $?
printf 'fugaro %s (in %s)\n' "$*" "$PWD" >>"$FAKE_LOG_DIR/fugaro.calls"
mode=${FAKE_MODE:-default}
state=${FAKE_STATE:?}
doc=${FAKE_JSON_DIR:?}
anthropic=${FAKE_ANTHROPIC:?}
openrouter=${FAKE_OPENROUTER:?}
pool=${FAKE_POOL:?}
craft=${FAKE_CRAFT:?}
installation=${FAKE_INSTALLATION:?}
run_id=${FAKE_RUN_ID:?}

# The real CLI is cobra, and cobra refuses a flag its command does not declare:
# "fugaro: unknown flag: --project" on stderr, exit 1. A fake that shrugged the
# extra argument off is exactly what let this check hand `accounts add` a
# --project it does not take and still look healthy; these are the flags each
# command this check calls declares (read off the release's own --help), and a
# call carrying any other is refused in the same words.
flag_set() { # flag_set SUBCOMMAND...
  case "$1" in
  version) printf '%s' "" ;;
  doctor) printf '%s' " --all --check-bitbucket-token --check-github-app --config --dir --gcp-project --json --offline --plugin --project --region --security --strict" ;;
  craft) printf '%s' " --config --gcp-project --json --project --region" ;;
  accounts)
    case "${2:-}" in
    add) printf '%s' " --ask --claude --profile --repos --type" ;;
    rm) printf '%s' " --no-apply --profile" ;;
    ls) printf '%s' " --config --gcp-project --json --project --region --repo" ;;
    pool)
      case "${3:-}" in
      add) printf '%s' " --allow-user --layer --owner --strategy" ;;
      rm) printf '%s' " --layer" ;;
      ls) printf '%s' " --config --gcp-project --project --region" ;;
      esac
      ;;
    craft)
      case "${3:-}" in
      set) printf '%s' " --access --layer" ;;
      rm) printf '%s' " --layer" ;;
      esac
      ;;
    esac
    ;;
  project) printf '%s' " --config --delete --gcp-project --json --project --region --remove-routing --seed-repos" ;;
  run) printf '%s' " --allow-duplicate --attach --attempts --batch --change-set --code-only --commit-reports --config --context --continues --gcp-project --json --max-review-rounds --max-time --max-tokens --max-usd --mode --no-budget-check --no-compression --owner-ruled --path --plan-task --pr --project --recipe --ref --region --repo --retry --review-levels --run-id --security --spec --task-file --tier --title --token --total-timeout --used-review-rounds --used-time --used-tokens --used-usd --workflow" ;;
  runs) printf '%s' " --after --all --batch --config --debt --gcp-project --group-by --json --limit --mine --pr --project --region --repo --since --state --stream --watch" ;;
  esac
}

guard_flags() { # guard_flags — the whole call, command words first, then flags
  local words=() f
  while [ $# -gt 0 ]; do
    case "$1" in
    -*) break ;;
    *) words+=("$1"); shift ;;
    esac
  done
  local set
  set=$(flag_set "${words[@]+"${words[@]}"}")
  for f in "$@"; do
    case "$f" in
    -*) ;;
    *) continue ;;
    esac
    case "$f" in
    --*=*) f=${f%%=*} ;;
    esac
    case " $set " in
    *" $f "*) ;;
    *)
      printf 'fugaro: unknown flag: %s\n' "$f" >&2
      exit 1
      ;;
    esac
  done
}
guard_flags "$@"

serving_path() {
  case "$1" in
  anthropic) printf 'credsvc\n' ;;
  openrouter) printf 'runner\n' ;;
  *) printf 'credsvc\n' ;;
  esac
}

creds_listing() {
  printf '{"repo":"acme/sandbox","accounts":["default"],"complete":true,"credentials":['
  awk -F'\t' 'BEGIN { n = 0 } { if (n) printf ","; n++; printf "{\"name\":\"%s\",\"type\":\"%s\",\"serving_path\":\"%s\"}", $1, $2, $3 }' "$state/creds.tsv"
  printf ']}\n'
}

remove_cred() { # remove_cred NAME
  if [ "$mode" = removal-does-not-stick ]; then return 0; fi
  awk -F'\t' -v want="$1" '$1 != want' "$state/creds.tsv" >"$state/creds.new"
  mv "$state/creds.new" "$state/creds.tsv"
}

pools_listing() {
  printf 'pools for project %s (offline)\n\n' "$installation"
  printf 'NAME  STRATEGY  MEMBERS  SERVED BY\n'
  if [ -f "$state/pool" ]; then
    local line name member
    line=$(cat "$state/pool")
    name=${line%%	*}
    member=${line#*	}
    printf '%s  failover  %s  %s\n' "$name" "$member" "$(serving_path anthropic)"
  fi
}

crafts_listing() {
  local access
  access=$(cat "$state/access")
  if [ "$mode" = no-craft ]; then
    printf '{"version":1,"project":"%s","crafts":[],"problems":[]}\n' "$installation"
    return 0
  fi
  if [ "$mode" = craft-no-access ]; then access=""; fi
  printf '{"version":1,"project":"%s","crafts":[{"craft":"%s","model":"deepseek/deepseek-v4","model_from":"agent.crafts.%s.model","access":"%s","access_from":"agent.crafts.%s.access","inert":false,"model_servable":true,"model_problem":""}],"problems":[]}\n' \
    "$installation" "$craft" "$craft" "$access" "$craft"
}

accounts_add() { # accounts_add NAME TYPE
  local name=$1 type=$2 value=""
  if [ -n "${CLAUDECODE:-}" ] && [ "$mode" != session-accepts ]; then
    if [ "$mode" = session-unnamed ]; then
      printf 'fugaro: this command cannot be run here\n' >&2
      exit 1
    fi
    if [ "$mode" = session-other-words ]; then
      printf 'fugaro: a key is written for the person it belongs to and for nobody else: %s was declared, and its value was not read\n' "$name" >&2
      exit 1
    fi
    printf 'fugaro: No key was stored for credential %s: a session may write the declaration, and never the key.\nRun this in your own terminal, not in an agent session.\n' "$name" >&2
    exit 1
  fi
  if [ "$mode" = add-fails ]; then
    printf 'fugaro: the value is too short to be a key\n' >&2
    exit 1
  fi
  if [ "$type" = openrouter ] && [ "$mode" != add-openrouter-no-prompt ]; then
    printf 'Paste the value for credential-%s-key (input hidden), then press Enter: ' "$name" >&2
  fi
  IFS= read -r value || true
  if [ "$mode" = add-echoes-value ]; then
    printf 'fugaro: storing the value %s now\n' "$value"
  fi
  if [ "$mode" = add-openrouter-echoes-shape ]; then
    # The key-shaped string this case is about is built here, from parts: no
    # literal in this repository is shaped like a credential.
    printf 'fugaro: the value sk-or-v1-%s was accepted\n' "not-a-real-key" >&2
  fi
  if [ "$name" = "$anthropic" ]; then
    printf "fugaro: no job is deployed for %s: a key credential is not a run's Claude account token\n" "$name" >&2
    [ -t 0 ] || printf "note: dropped the value's trailing newline\n" >&2
  fi
  printf '%s\t%s\t%s\n' "$name" "$type" "$(serving_path "$type")" >>"$state/creds.tsv"
  printf 'fugaro: declared %s\n' "$name"
}

case "${1:-}" in
version) printf '0.28.0\n' ;;
doctor)
  case "$mode" in
  doctor-belong) printf 'fugaro 0.28.0\nproject: %s (GCP belong-sandbox, us-east5)\n' "$installation" ;;
  doctor-fail) printf 'fugaro: cannot read the installation\n' >&2; exit 1 ;;
  doctor-no-project) printf 'fugaro 0.28.0\nrepository: acme/sandbox\n' ;;
  doctor-no-region) printf 'fugaro 0.28.0\nproject: %s\n' "$installation" ;;
  *) cat "$doc/doctor.txt" ;;
  esac
  ;;
accounts)
  case "${2:-}" in
  add) accounts_add "$3" "${5:-}" ;;
  ls)
    case "$mode" in
    creds-fail) printf 'fugaro: cannot read the config\n' >&2; exit 1 ;;
    creds-garbage) printf 'not a document\n' ;;
    *) creds_listing ;;
    esac
    ;;
  rm)
    case "$mode" in
    rm-accepts) printf 'fugaro: removed %s\n' "$3"; remove_cred "$3" ;;
    rm-unnamed)
      printf 'fugaro: credentials.%s is still a member of %s: nothing was written\n' "$3" "$pool" >&2
      exit 1
      ;;
    *)
      if [ -f "$state/pool" ] && [ "$(cut -f2 "$state/pool")" = "$3" ]; then
        declaring_pool=$(cut -f1 "$state/pool")
        printf 'fugaro: credentials.%s is still a member of %s, so removing the account would leave the pool with a member no block defines: run `fugaro accounts pool rm` for that pool first, then run this again; nothing was written\n' "$3" "$declaring_pool" >&2
        exit 1
      fi
      printf 'fugaro: removed %s\n' "$3"
      remove_cred "$3"
      ;;
    esac
    ;;
  pool)
    case "${3:-}" in
    add)
      if [ "$mode" = pool-add-fails ]; then
        printf 'fugaro: pools.%s: an owner is required of a pool that is published\n' "$4" >&2
        exit 1
      fi
      printf '%s\t%s\n' "$4" "$5" >"$state/pool"
      printf 'fugaro: declared pool %s\n' "$4"
      ;;
    ls) pools_listing ;;
    rm)
      if [ "$mode" = pool-rm-fails ]; then
        printf 'fugaro: cannot remove %s\n' "$4" >&2
        exit 1
      fi
      rm -f "$state/pool"
      printf 'fugaro: removed pool %s\n' "$4"
      ;;
    *) printf 'fugaro: unexpected pool subcommand %s\n' "$3" >&2; exit 1 ;;
    esac
    ;;
  craft)
    [ "${3:-}" = "set" ] || { printf 'fugaro: unexpected accounts craft subcommand\n' >&2; exit 1; }
    if [ "$mode" = craft-set-fails ]; then
      printf 'fugaro: the access names a pool whose owner is not yours\n' >&2
      exit 1
    fi
    printf '%s\n' "$6" >"$state/access"
    printf 'fugaro: the craft %s now names %s\n' "$4" "$6"
    ;;
  *) printf 'fugaro: unexpected accounts subcommand %s\n' "${2:-}" >&2; exit 1 ;;
  esac
  ;;
craft)
  [ "${2:-}" = "ls" ] || { printf 'fugaro: unexpected craft subcommand\n' >&2; exit 1; }
  case "$mode" in
  crafts-fail) printf 'fugaro: cannot read the config\n' >&2; exit 1 ;;
  *) crafts_listing ;;
  esac
  ;;
project)
  [ "${2:-}" = "policy" ] || { printf 'fugaro: unexpected project subcommand\n' >&2; exit 1; }
  case "${3:-}" in
  publish)
    if [ "$mode" = publish-fails ]; then
      printf 'fugaro: the policy node of this installation carries no rules marker\n' >&2
      exit 1
    fi
    printf 'fugaro: published the policy node of %s\n' "$installation"
    ;;
  show)
    if [ "$mode" = node-fails ]; then
      printf 'fugaro: the node could not be read back\n' >&2
      exit 1
    fi
    printf '{"project":"%s","policy":{"version":10,"craft_credentials":[{"name":"%s","type":"anthropic"}],"craft_pools":[{"name":"%s","strategy":"failover","members":["%s"]}],"crafts":{"%s":{"model":"deepseek/deepseek-v4","access":"%s"}}}}\n' \
      "$installation" "$anthropic" "$pool" "$anthropic" "$craft" "$pool"
    ;;
  *) printf 'fugaro: unexpected policy subcommand\n' >&2; exit 1 ;;
  esac
  ;;
run)
  if [ "$mode" = launch-fails ]; then
    printf 'fugaro: the runs bucket is not reachable\n' >&2
    exit 1
  fi
  printf '{"project":"%s","repo":"acme/sandbox","run_id":"%s","status":"launched"}\n' "$installation" "$run_id"
  ;;
runs)
  [ "${2:-}" = "ls" ] || { printf 'fugaro: unexpected runs subcommand\n' >&2; exit 1; }
  n=0
  if [ -f "$state/ls-count" ]; then n=$(cat "$state/ls-count"); fi
  n=$((n + 1))
  printf '%s\n' "$n" >"$state/ls-count"
  terminal=true
  if [ "$mode" = run-never-ends ]; then terminal=false; fi
  if [ "$mode" = default ] && [ "$n" -lt 2 ]; then terminal=false; fi
  printf '{"runs":[{"run_id":"%s","status":"ready","terminal":%s,"stage":"finalize","run":"main/%s"}]}\n' "$run_id" "$terminal" "$run_id"
  ;;
*)
  printf 'fugaro: unexpected subcommand %s\n' "${1:-}" >&2
  exit 1
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
. "$LIVE_CHECK_LIB/strict.sh"
strict_gcloud_verify "$LIVE_CHECK_LIB/allow/check631.gcloud" "$@" || exit $?
# A stand-in for `gcloud`, for scripts/live-checks/check631.sh's test only: the
# account the check reads for the pool's owner, the run's own record out of the
# runs bucket, and the credentials service's audit lines. Every call is logged.
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"$FAKE_LOG_DIR/gcloud.calls"
mode=${FAKE_MODE:-default}
doc=${FAKE_JSON_DIR:?}
case "${1:-}" in
config)
  printf '%s\n' "$FAKE_ACCOUNT"
  ;;
storage)
  case "$mode" in
  record-fails) printf 'ERROR: the object does not exist\n' >&2; exit 1 ;;
  record-wrong-run) cat "$doc/record-other.json" ;;
  # The record of a run whose stages were served by another credential: the two
  # rows that read "which credential answered" then agree that it was not the
  # one this check declared.
  audit-refused) cat "$doc/record-nocred.json" ;;
  *) cat "$doc/record.json" ;;
  esac
  ;;
logging)
  case "$mode" in
  audit-fails) printf 'ERROR: logging read failed\n' >&2; exit 1 ;;
  audit-other) cat "$doc/audit-other.json" ;;
  audit-empty) printf '[]\n' ;;
  audit-refused) cat "$doc/audit-refused.json" ;;
  *) cat "$doc/audit.json" ;;
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

write_feeder() { # dir — the pty the two pastes are made at
  cat >"$1/feeder.py" <<'PY'
import os
import pty
import sys
import time

# A terminal for the check's two pastes, for its test only. The slave's path is
# printed first (the check is handed it through CHECK631_TTY), and the values are
# written into it at once: a terminal holds them in its input queue, so the
# check's own hidden prompt reads the first and the command's own hidden prompt
# reads the second, in the order the check makes them. The master is held open
# until the test kills this process, because a terminal whose master is gone
# gives its reader end-of-file.
master, slave = pty.openpty()
sys.stdout.write(os.ttyname(slave) + "\n")
sys.stdout.flush()
for value in sys.argv[1:]:
    os.write(master, (value + "\n").encode())
time.sleep(float(os.environ.get("PTY_HOLD", "120")))
PY
}

# ------------------------------------------------------------------ documents
write_record() { # json-dir NAME RUN STATUS OUTCOME CREDENTIAL
  cat >"$1/$2" <<JSON
{"run_id":"$3","status":"$4","stage":"finalize","outcome":"$5","pr":{"url":"https://example.invalid/acme/sandbox/pull/1"},"execution":"exec-631","cost":{"model_usd":0.42},"stage_accounts":{"implement":"$6"},"stages":[{"name":"plan","model":"deepseek/deepseek-v4","credential":"$6","served_by":"credsvc"},{"name":"implement","model":"deepseek/deepseek-v4","credential":"$6","served_by":"credsvc"}],"error":null}
JSON
}

write_audit() { # json-dir NAME RUN STATUS CREDENTIAL
  cat >"$1/$2" <<JSON
[{"jsonPayload":{"message":"credsvc: audit","run":"$3","repository":"acme/sandbox","job":"job-631","caller":"sa@$project.iam.gserviceaccount.com","credential":"$5","tier":"C","purpose":"forward","status":"$4"}}]
JSON
}

write_docs() { # dir
  local doc=$1/json
  mkdir -p "$doc"
  cat >"$doc/doctor.txt" <<TXT
fugaro 0.28.0
project: $installation (GCP $project, us-east5)
repository: acme/sandbox
TXT
  write_record "$doc" record.json "$run_id" ready ready "$anthropic"
  write_record "$doc" record-other.json "r-other" ready ready "$anthropic"
  write_record "$doc" record-nocred.json "$run_id" ready ready "some-other-credential"
  write_audit "$doc" audit.json "$run_id" ok "$anthropic"
  write_audit "$doc" audit-other.json "$run_id" ok "some-other-credential"
  cat >"$doc/audit-refused.json" <<JSON
[{"jsonPayload":{"message":"credsvc: audit","run":"$run_id","repository":"acme/sandbox","job":"job-631","caller":"sa@$project.iam.gserviceaccount.com","purpose":"forward","status":"halted"}}]
JSON
}

# A repository checkout: the check refuses a path that is not one, and every
# command is run from the checkout it belongs to.
write_checkout() { # dir
  mkdir -p "$1"
  printf 'project: %s\n' "$installation" >"$1/fugaro.yaml"
}

# The config directory and the shell history the check sweeps: the declaration
# the check's own credentials land beside, and the history of the shell the owner
# started the check from. Neither holds a value.
write_home() { # dir
  mkdir -p "$1/home/.config/fugaro"
  cat >"$1/home/.config/fugaro/config.json" <<JSON
{"version":10,"agent":{"crafts":{"$craft":{"model":"deepseek/deepseek-v4","access":"coder-pool"}}},"credentials":{"work":{"type":"claude-oauth"}}}
JSON
  cp "$1/home/.config/fugaro/config.json" "$1/home/.config/fugaro/config.json.bak-20261121T000000Z"
  printf 'fugaro doctor --project %s --all\n' "$installation" >"$1/home/.bash_history"
}

write_state() { # dir
  mkdir -p "$1/state"
  printf 'work\tclaude-oauth\tcredsvc\n' >"$1/state/creds.tsv"
  printf 'coder-pool\n' >"$1/state/access"
  rm -f "$1/state/pool"
  rm -f "$1/state/ls-count"
}

prepare() { # dir
  mkdir -p "$1/home" "$1/state" "$1/logs" "$1/tmp"
  write_fake_fugaro "$1"
  write_fake_gcloud "$1"
  write_docs "$1"
  write_checkout "$1/sandbox"
  write_home "$1"
  write_state "$1"
  write_feeder "$1"
}

# ------------------------------------------------------------------- the pty
FEEDER_PID=""
FEEDER_TTY=""
start_feeder() { # start_feeder DIR VALUE...
  local dir=$1
  shift
  FEEDER_PID=""
  FEEDER_TTY=""
  : >"$dir/feeder.tty"
  PTY_HOLD=120 python3 "$dir/feeder.py" "$@" >"$dir/feeder.tty" 2>"$dir/feeder.err" &
  FEEDER_PID=$!
  local i=0
  while [ "$i" -lt 100 ]; do
    FEEDER_TTY=$(head -n 1 "$dir/feeder.tty" 2>/dev/null || true)
    [ -n "$FEEDER_TTY" ] && break
    sleep 0.1
    i=$((i + 1))
  done
  if [ -z "$FEEDER_TTY" ]; then fail "the pty feeder produced no terminal: $(cat "$dir/feeder.err" 2>/dev/null)"; fi
  return 0
}
stop_feeder() {
  if [ -n "$FEEDER_PID" ]; then
    kill "$FEEDER_PID" 2>/dev/null || true
    wait "$FEEDER_PID" 2>/dev/null || true
  fi
  FEEDER_PID=""
  FEEDER_TTY=""
  return 0
}

run_check() { # run_check DIR ENV... [-- ARGS...]
  local dir=$1
  shift
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
  write_state "$dir"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/gcloud.calls"
  status=0
  local bashargs=()
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  # The agent markers are dropped from this test's own environment: the check
  # refuses a coding agent's session by name, and the suite that runs this file
  # may itself be started from one. The case that measures that guard sets the
  # marker again, and a variable given as an argument wins over `env -u`. Note
  # that the marker is NOT dropped from the fake's own environment: the check's
  # refusal act is `env CLAUDECODE=1 fugaro accounts add`, which sets it for the
  # command it runs.
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" XDG_CONFIG_HOME= TMPDIR="$dir/tmp" HISTFILE= \
    LIVE_CHECK_LIB="$LIVE_CHECK_LIB" \
    FAKE_LOG_DIR="$dir/logs" FAKE_JSON_DIR="$dir/json" FAKE_STATE="$dir/state" \
    FAKE_ANTHROPIC="$anthropic" FAKE_OPENROUTER="$openrouter" FAKE_POOL="$pool" \
    FAKE_CRAFT="$craft" FAKE_INSTALLATION="$installation" FAKE_ACCOUNT="$owner" \
    FAKE_RUN_ID="$run_id" \
    CHECK631_SANDBOX="$dir/sandbox" \
    CHECK631_TTY="${FEEDER_TTY:-/dev/null}" \
    CHECK631_POLL_SECONDS=1 CHECK631_RUN_TIMEOUT_SECONDS="${CHECK631_TEST_RUN_TIMEOUT:-20}" \
    CHECK631_CLI_TIMEOUT_SECONDS=20 CHECK631_DOCTOR_TIMEOUT_SECONDS=20 \
    CHECK631_LAUNCH_TIMEOUT_SECONDS=20 CHECK631_PUBLISH_TIMEOUT_SECONDS=20 \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" ${cli[@]+"${cli[@]}"} \
    </dev/null >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  fugaro_calls=$(cat "$dir/logs/fugaro.calls")
  gcloud_calls=$(cat "$dir/logs/gcloud.calls")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

# A run in CHECK631_VALUE_FILE mode: no terminal is needed, the value is read
# from a file the test wrote, and the command's own hidden prompt is not made.
# CHECK631_TEST_VALUE names the value when a case needs one of its own.
run_check_with_value_file() { # run_check_with_value_file DIR ENV... [-- ARGS...]
  local dir=$1
  shift
  printf '%s\n' "${CHECK631_TEST_VALUE:-$value_one}" >"$dir/value.txt"
  run_check "$dir" CHECK631_VALUE_FILE="$dir/value.txt" "$@"
}

# A run with a terminal: the check reads its own value at its hidden prompt and
# the command reads the second at its own. CHECK631_TEST_VALUE_ONE and
# CHECK631_TEST_VALUE_TWO name the two, where a case needs its own.
run_check_with_terminal() { # run_check_with_terminal DIR ENV... [-- ARGS...]
  local dir=$1
  shift
  start_feeder "$dir" "${CHECK631_TEST_VALUE_ONE:-$value_one}" "${CHECK631_TEST_VALUE_TWO:-$value_two}"
  run_check "$dir" "$@"
  stop_feeder
}

# ------------------------------------------------------------------ assertions
assert_scratch_removed() { # what
  [ -n "$scratch" ] || {
    fail "$1: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$1: the scratch directory $scratch is still there"
}

# --------------------------------------------------------------- the test cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "help"
  expect_contains "$out" "Check 631" "help: the header"
  expect_contains "$out" "hidden prompt, and nothing echoed" "help: the docblock"
  expect_contains "$out" "check631.sh --sandbox" "help: the usage line"
  expect_contains "$out" "Knobs (all CHECK631_*)" "help: the knobs"
  expect_contains "$out" "YOUR OWN TERMINAL" "help: the terminal it needs"
}

test_dry_run_needs_no_terminal() {
  local dir=$work/dry-run
  prepare "$dir"
  run_check "$dir" CHECK631_DRY_RUN=1
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "FACT dry-run: PASS" "a dry run: the row"
  expect_contains "$out" "FACT value-read: INFO" "a dry run: the planned rows are not measured"
  expect_contains "$err" "[write] fugaro accounts add $anthropic --type anthropic" "a dry run: the plan names the declaration it would make"
  expect_contains "$err" "[YOU] paste the value for $openrouter" "a dry run: the plan names the second paste"
  expect_absent "$fugaro_calls" "accounts add" "a dry run: nothing is declared"
  expect_absent "$fugaro_calls" "fugaro run" "a dry run: no run is launched"
  assert_scratch_removed "a dry run"
}

test_the_happy_path_at_a_terminal() {
  local dir=$work/happy
  prepare "$dir"
  run_check_with_terminal "$dir"
  expect_status "$status" 0 "the happy path"
  local key
  for key in fugaro-version xtrace agent-session belong installation declaration agent-refusal value-read add-anthropic nothing-echoed add-openrouter prompt-hidden no-file no-history serving-path wiring publish run-launched terminal record served-by unwiring rm-refusal rm-clears restore scratch-removed; do
    expect_contains "$out" "FACT $key: PASS" "the happy path: the $key row"
  done
  expect_absent "$out" ": FAIL" "the happy path: no row failed"
  expect_contains "$out" "CHECK 631 RESULT END (exit 0)" "the happy path: the block ends"
  expect_contains "$out" "the command's own hidden prompt line ('Paste the value for credential-$openrouter-key (input hidden), then press Enter:')" "the happy path: the prompt row names the command's own prompt"
  expect_contains "$fugaro_calls" "fugaro accounts add $anthropic --type anthropic (in $dir/sandbox)" "the happy path: the Anthropic change is declared from the checkout"
  expect_contains "$fugaro_calls" "fugaro accounts pool add $pool $anthropic --owner $owner (in $dir/sandbox)" "the happy path: the check's own pool names the credential and the owner"
  expect_contains "$fugaro_calls" "fugaro accounts craft set $craft --access $pool (in $dir/sandbox)" "the happy path: the craft is pointed at the check's pool"
  expect_contains "$fugaro_calls" "fugaro run --repo $dir/sandbox --json" "the happy path: one run is launched"
  assert_scratch_removed "the happy path"
  # What the check left behind is what the acceptance's own clause unwinds: the
  # craft's access is its own again, the pool is gone and both credentials are.
  [ "$(cat "$dir/state/access")" = "coder-pool" ] || fail "the happy path: the craft's access was not put back (it reads $(cat "$dir/state/access"))"
  [ ! -f "$dir/state/pool" ] || fail "the happy path: the check's own pool is still declared"
  if grep -q "$anthropic" "$dir/state/creds.tsv" || grep -q "$openrouter" "$dir/state/creds.tsv"; then
    fail "the happy path: a credential this check declared is still declared"
  fi
}

test_the_value_never_reaches_a_file_or_a_history() {
  local dir=$work/sweeps
  prepare "$dir"
  run_check_with_value_file "$dir"
  expect_status "$status" 0 "the sweeps"
  expect_contains "$out" "FACT no-file: PASS" "the sweeps: the config directory holds no value"
  expect_contains "$out" "is in none of the" "the sweeps: the config sweep says what it read"
  expect_contains "$out" "FACT no-history: PASS" "the sweeps: the history holds no value"
  expect_contains "$out" "$dir/home/.bash_history" "the sweeps: the history file it read is named"
  # The block names the files it looked at and never the value itself.
  expect_absent "$out" "$value_one" "the sweeps: the value is in no line of the block"
  expect_absent "$err" "$value_one" "the sweeps: the value is in no line of the progress output"
}

test_a_value_in_the_config_directory_is_a_finding() {
  local dir=$work/config-holds
  prepare "$dir"
  printf '{"note":"%s"}\n' "$value_one" >"$dir/home/.config/fugaro/cache.json"
  run_check_with_value_file "$dir"
  expect_status "$status" 1 "a value in the config directory"
  expect_contains "$out" "FACT no-file: FAIL" "a value in the config directory: the row"
  expect_contains "$out" "is in 1 file(s) under" "a value in the config directory: the row says how many"
  expect_contains "$out" "cache.json" "a value in the config directory: the file is named"
  expect_absent "$out" "$value_one" "a value in the config directory: the value itself is never printed"
}

test_a_value_in_a_shell_history_is_a_finding() {
  local dir=$work/history-holds
  prepare "$dir"
  printf 'fugaro accounts add %s --type anthropic %s\n' "$anthropic" "$value_one" >>"$dir/home/.bash_history"
  run_check_with_value_file "$dir"
  expect_status "$status" 1 "a value in a shell history"
  expect_contains "$out" "FACT no-history: FAIL" "a value in a shell history: the row"
  expect_contains "$out" ".bash_history" "a value in a shell history: the file is named"
}

test_a_command_that_echoes_the_value_is_a_finding() {
  local dir=$work/echoes
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=add-echoes-value
  expect_status "$status" 1 "a command that echoes the value"
  expect_contains "$out" "FACT nothing-echoed: FAIL" "a command that echoes the value: the row"
  expect_contains "$out" "holds the value this check handed it" "a command that echoes the value: the row says so"
  expect_absent "$out" "$value_one" "a command that echoes the value: the value is not reprinted by the check"
}

test_a_prompt_that_echoes_a_key_shape_is_a_finding() {
  local dir=$work/shape
  prepare "$dir"
  run_check_with_terminal "$dir" FAKE_MODE=add-openrouter-echoes-shape
  expect_status "$status" 1 "a prompt that echoes a key shape"
  expect_contains "$out" "FACT prompt-hidden: FAIL" "a prompt that echoes a key shape: the row"
  expect_contains "$out" "carries a line shaped like a provider key" "a prompt that echoes a key shape: the row says so"
  # The check names the stream and never the line: the shape it reports is the
  # file it found it in, not the string.
  expect_absent "$out" "sk-or-v1-not-a-real-key" "a prompt that echoes a key shape: the check does not reprint the string"
}

test_the_two_pastes_are_read_at_two_different_prompts() {
  local dir=$work/prompts
  prepare "$dir"
  run_check_with_terminal "$dir"
  expect_status "$status" 0 "the two prompts"
  expect_contains "$out" "the value was read at this check's own hidden prompt on your terminal" "the two prompts: the check's own prompt is the one its value came from"
  expect_contains "$out" "carries the command's own hidden prompt line" "the two prompts: the command's own prompt is the one its value came from"
  expect_contains "$out" "FACT add-openrouter: PASS" "the two prompts: the second type was declared"
}

test_with_no_terminal_and_no_value_file_the_check_stops() {
  local dir=$work/no-terminal
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 1 "no terminal and no value file"
  expect_contains "$out" "FACT aborted: FAIL" "no terminal and no value file: the run is aborted"
  expect_contains "$out" "this check needs your own terminal" "no terminal and no value file: the message"
  expect_absent "$fugaro_calls" "accounts add" "no terminal and no value file: nothing is declared"
}

test_a_session_that_is_refused() {
  local dir=$work/session
  prepare "$dir"
  run_check_with_value_file "$dir"
  expect_status "$status" 0 "a session that is refused"
  expect_contains "$out" "FACT agent-refusal: PASS" "a session that is refused: the row"
  expect_contains "$out" "No key was stored for credential $anthropic" "a session that is refused: the refusal's own sentence is quoted"
  expect_contains "$out" "names $anthropic no" "a session that is refused: the row says the declaration was not written either"
  expect_contains "$fugaro_calls" "fugaro accounts add $anthropic --type anthropic" "a session that is refused: the act was run"
}

test_a_session_that_is_not_refused_is_a_finding() {
  local dir=$work/session-accepts
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=session-accepts
  expect_status "$status" 1 "a session that is not refused"
  expect_contains "$out" "FACT agent-refusal: FAIL" "a session that is not refused: the row"
  expect_contains "$out" "exited 0 with CLAUDECODE set" "a session that is not refused: the row says what happened"
}

test_a_session_refusal_that_names_nothing_is_a_finding() {
  local dir=$work/session-unnamed
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=session-unnamed
  expect_status "$status" 1 "a session refusal that names nothing"
  expect_contains "$out" "FACT agent-refusal: FAIL" "a session refusal that names nothing: the row"
  expect_contains "$out" "do not name the session it refused" "a session refusal that names nothing: the row says what is missing"
}

test_the_project_flag_is_passed_through() {
  local dir=$work/with-project
  prepare "$dir"
  run_check_with_value_file "$dir" CHECK631_PROJECT="$installation"
  expect_status "$status" 0 "a named project"
  expect_contains "$fugaro_calls" "fugaro craft ls --json --project $installation" "a named project: the crafts read names it"
  expect_contains "$fugaro_calls" "fugaro accounts pool ls --project $installation" "a named project: the pools read names it"
  expect_contains "$fugaro_calls" "fugaro project policy publish --project $installation" "a named project: the publish names it"
  expect_contains "$fugaro_calls" "fugaro accounts ls --json --project $installation" "a named project: the credentials read names it"
  # `accounts add` declares no --project: the project is the checkout's own
  # fugaro.yaml, which is the directory every call of this check is made in. The
  # fake refuses an undeclared flag the way cobra does, so handing it one would
  # end the row on 'unknown flag: --project' instead of on the refusal.
  expect_contains "$fugaro_calls" "fugaro accounts add $anthropic --type anthropic (in $dir/sandbox)" "a named project: the declaration carries no flag accounts add does not take"
  expect_absent "$fugaro_calls" "accounts add $anthropic --type anthropic --project" "a named project: the declaration is handed no --project"
  expect_contains "$out" "FACT agent-refusal: PASS" "a named project: the refusal row is reached and holds"
}

# The row is a person's only account of what the CLI told a session: a refusal in
# words the check does not recognize must still be quoted, not summarized.
test_the_agent_refusal_row_reads_the_refusal_own_words() {
  local dir=$work/session-words
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=session-other-words
  expect_status "$status" 1 "a refusal in other words"
  expect_contains "$out" "FACT agent-refusal: FAIL" "a refusal in other words: the row"
  expect_contains "$out" "a key is written for the person it belongs to and for nobody else: $anthropic was declared, and its value was not read" "a refusal in other words: the row quotes the refusal's own words"
}

test_a_craft_with_no_access_is_not_measured() {
  local dir=$work/craft-no-access
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=craft-no-access
  expect_status "$status" 2 "a craft with no access"
  expect_contains "$out" "FACT declaration: FAIL" "a craft with no access: the row"
  expect_contains "$out" "declares no access" "a craft with no access: the row says what is missing"
  expect_contains "$out" "fugaro accounts craft set $craft --access" "a craft with no access: the row names the command that declares one"
  expect_contains "$err" "could NOT measure declaration, wiring, publish, run-launched" "a craft with no access: the writes stand down"
  expect_absent "$fugaro_calls" "accounts pool add" "a craft with no access: no pool is declared"
  expect_absent "$fugaro_calls" "fugaro run" "a craft with no access: no run is launched"
}

test_an_installation_with_no_craft_is_not_measured() {
  local dir=$work/no-craft
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=no-craft
  expect_status "$status" 2 "an installation with no craft"
  expect_contains "$out" "names no craft '$craft'" "an installation with no craft: the row names the craft it looked for"
  expect_absent "$fugaro_calls" "accounts pool add" "an installation with no craft: no pool is declared"
}

test_a_read_that_does_not_answer_is_not_measured() {
  local dir=$work/crafts-fail
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=crafts-fail
  expect_status "$status" 2 "a crafts read that does not answer"
  expect_contains "$err" "could NOT measure declaration" "a crafts read that does not answer: it is named as unmeasured"
  expect_absent "$fugaro_calls" "accounts pool add" "a crafts read that does not answer: no pool is declared"
}

test_a_pool_that_cannot_be_declared_is_not_measured() {
  local dir=$work/pool-add-fails
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=pool-add-fails
  expect_status "$status" 2 "a pool that cannot be declared"
  expect_contains "$out" "FACT wiring: FAIL" "a pool that cannot be declared: the row"
  expect_contains "$err" "could NOT measure wiring, publish, run-launched" "a pool that cannot be declared: nothing is published and no run is launched"
  expect_absent "$fugaro_calls" "project policy publish" "a pool that cannot be declared: nothing is published"
  expect_absent "$fugaro_calls" "fugaro run" "a pool that cannot be declared: no run is launched"
}

test_a_publish_that_fails_is_not_measured() {
  local dir=$work/publish-fails
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=publish-fails
  # The unmeasured publish row stands the run down, and the same refusal meets
  # the publish that puts the node back: a write this check cannot undo is a
  # finding, so a FAIL outranks the unmeasured row and the exit is 1.
  expect_status "$status" 1 "a publish that fails"
  expect_contains "$out" "FACT publish: FAIL" "a publish that fails: the row"
  expect_contains "$out" "exit 1" "a publish that fails: the row quotes the exit"
  expect_contains "$err" "could NOT measure publish, run-launched" "a publish that fails: the rows below it are unmeasured"
  expect_contains "$out" "FACT unwiring: FAIL" "a publish that fails: the write that could not be undone is the finding"
  expect_absent "$fugaro_calls" "fugaro run" "a publish that fails: no run is launched"
}

test_a_launch_that_fails_is_a_finding() {
  local dir=$work/launch-fails
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=launch-fails
  expect_status "$status" 1 "a launch that fails"
  expect_contains "$out" "FACT run-launched: FAIL" "a launch that fails: the row"
  expect_contains "$out" "never started" "a launch that fails: the row says so"
  expect_contains "$err" "could NOT measure terminal, record, served-by" "a launch that fails: the rows below are unmeasured"
}

test_a_run_that_never_ends_is_not_measured() {
  local dir=$work/never-ends
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=run-never-ends \
    CHECK631_RUN_TIMEOUT_SECONDS=2 CHECK631_POLL_SECONDS=1
  expect_status "$status" 2 "a run that never ends"
  expect_contains "$out" "FACT terminal: FAIL" "a run that never ends: the row"
  expect_contains "$out" "had not ended after 2s" "a run that never ends: the row names the bound"
  expect_contains "$out" "FACT rm-clears: PASS" "a run that never ends: the later rows still run and unwind what the check wrote"
}

test_a_record_that_is_another_runs_is_a_finding() {
  local dir=$work/record-other
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=record-wrong-run
  expect_status "$status" 1 "a record that is another run's"
  expect_contains "$out" "FACT record: FAIL" "a record that is another run's: the row"
  expect_contains "$out" "the record at that key is another run's" "a record that is another run's: the row says so"
  expect_contains "$err" "could NOT measure served-by" "a record that is another run's: the row below it is unmeasured"
}

test_a_record_that_cannot_be_read_is_a_finding() {
  local dir=$work/record-fails
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=record-fails
  expect_status "$status" 1 "a record that cannot be read"
  expect_contains "$out" "FACT record: FAIL" "a record that cannot be read: the row"
  expect_contains "$out" "could not be read" "a record that cannot be read: the row says so"
}

test_an_audit_line_naming_another_credential_is_a_finding() {
  local dir=$work/audit-other
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=audit-other
  expect_status "$status" 1 "an audit line naming another credential"
  expect_contains "$out" "FACT served-by: FAIL" "an audit line naming another credential: the row"
  expect_contains "$out" "some-other-credential" "an audit line naming another credential: the row names what served it"
}

test_an_audit_read_that_does_not_answer_falls_back_to_the_record() {
  local dir=$work/audit-fails
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=audit-fails
  expect_status "$status" 0 "an audit read that does not answer"
  expect_contains "$out" "FACT served-by: PASS" "an audit read that does not answer: the record's own stages carry the reading"
  expect_contains "$out" "the run's own record names $anthropic as the credential its stages were served by" "an audit read that does not answer: the row says where it read it"
}

test_an_audit_line_that_names_no_credential_is_a_finding() {
  local dir=$work/audit-refused
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=audit-refused \
    FAKE_RECORD_CREDENTIAL=none
  expect_status "$status" 1 "an audit line that names no credential"
  expect_contains "$out" "FACT served-by: FAIL" "an audit line that names no credential: the row"
}

test_a_removal_accepted_while_a_pool_lists_it_is_a_finding() {
  local dir=$work/rm-accepts
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=rm-accepts
  expect_status "$status" 1 "a removal that is accepted"
  expect_contains "$out" "FACT rm-refusal: FAIL" "a removal that is accepted: the row"
  expect_contains "$out" "exited 0 while the pool $pool listed it" "a removal that is accepted: the row says so"
}

test_a_removal_refused_without_a_next_step_is_a_finding() {
  local dir=$work/rm-unnamed
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=rm-unnamed
  expect_status "$status" 1 "a removal refused without a next step"
  expect_contains "$out" "FACT rm-refusal: FAIL" "a removal refused without a next step: the row"
  expect_contains "$out" "names no step that clears it" "a removal refused without a next step: the row says what is missing"
}

test_a_credential_still_declared_after_the_removals_is_a_finding() {
  local dir=$work/removal-sticks
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=removal-does-not-stick
  expect_status "$status" 1 "a credential still declared after the removals"
  expect_contains "$out" "FACT rm-clears: FAIL" "a credential still declared after the removals: the row"
  expect_contains "$out" "still names $anthropic" "a credential still declared after the removals: the row names it"
}

test_the_session_guard_stops_the_check() {
  local dir=$work/agent-guard
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "a coding agent's session"
  expect_contains "$out" "FACT agent-session: FAIL" "a coding agent's session: the row"
  expect_contains "$out" "CLAUDECODE is set" "a coding agent's session: the marker is named"
  expect_absent "$fugaro_calls" "accounts add" "a coding agent's session: nothing is declared"
}

test_a_doctor_read_that_names_belong_stops_the_check() {
  local dir=$work/belong-project
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=doctor-belong
  expect_status "$status" 1 "an installation naming belong"
  expect_contains "$out" "FACT aborted: FAIL" "an installation naming belong: the run is aborted"
  expect_contains "$out" "belongs to belong-sandbox" "an installation naming belong: the message names the project"
  expect_absent "$fugaro_calls" "accounts add" "an installation naming belong: nothing is declared"
}

test_a_credential_naming_belong_is_refused() {
  local dir=$work/belong-cred
  prepare "$dir"
  run_check_with_value_file "$dir" CHECK631_POOL=belong-pool
  expect_status "$status" 1 "a pool naming belong"
  expect_contains "$out" "FACT belong: FAIL" "a pool naming belong: the row"
  expect_contains "$out" "names belong: this check is never run there" "a pool naming belong: the message says so by name"
  expect_absent "$fugaro_calls" "accounts add" "a pool naming belong: nothing is declared"
}

test_a_doctor_read_that_answers_no_project_is_not_measured() {
  local dir=$work/no-project
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=doctor-no-project
  expect_status "$status" 2 "a doctor read with no project line"
  expect_contains "$out" "FACT installation: FAIL" "a doctor read with no project line: the row"
  expect_contains "$err" "could NOT measure installation, declaration" "a doctor read with no project line: the value is never asked for"
  expect_contains "$err" "value-read, add-anthropic" "a doctor read with no project line: the paste step is unmeasured too"
  expect_absent "$fugaro_calls" "accounts add" "a doctor read with no project line: nothing is declared"
}

test_a_doctor_read_that_does_not_answer_is_not_measured() {
  local dir=$work/doctor-fail
  prepare "$dir"
  run_check_with_value_file "$dir" FAKE_MODE=doctor-fail
  expect_status "$status" 2 "a doctor read that does not answer"
  expect_contains "$err" "could NOT measure installation" "a doctor read that does not answer: it is named as unmeasured"
  expect_absent "$fugaro_calls" "accounts add" "a doctor read that does not answer: nothing is declared"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check_with_value_file "$dir" -- --xtrace
  expect_status "$status" 1 "a traced shell"
  expect_contains "$out" "FACT xtrace: FAIL" "a traced shell: the row"
  expect_contains "$out" "this shell is tracing" "a traced shell: the message"
  expect_absent "$fugaro_calls" "accounts add" "a traced shell: nothing is declared"
}

test_a_missing_sandbox_is_refused() {
  local dir=$work/no-sandbox
  prepare "$dir"
  run_check "$dir" CHECK631_SANDBOX=
  expect_status "$status" 2 "a missing sandbox"
  expect_contains "$err" "Check 631 needs --sandbox" "a missing sandbox: the message"
}

test_a_path_that_is_not_a_checkout_is_refused() {
  local dir=$work/not-a-checkout
  prepare "$dir"
  rm -f "$dir/sandbox/fugaro.yaml"
  run_check_with_value_file "$dir"
  expect_status "$status" 2 "a path that is not a checkout"
  expect_contains "$err" "carries no fugaro.yaml" "a path that is not a checkout: the message"
  expect_absent "$fugaro_calls" "accounts add" "a path that is not a checkout: nothing is declared"
}

test_an_empty_value_file_is_not_measured() {
  local dir=$work/empty-value
  prepare "$dir"
  printf '\n' >"$dir/value.txt"
  run_check "$dir" CHECK631_VALUE_FILE="$dir/value.txt"
  expect_status "$status" 2 "an empty value file"
  expect_contains "$out" "FACT value-read: FAIL" "an empty value file: the row"
  expect_contains "$err" "could NOT measure value-read, add-anthropic" "an empty value file: nothing is declared"
}

test_kept_scratch_is_reported() {
  local dir=$work/kept
  prepare "$dir"
  run_check_with_value_file "$dir" CHECK631_KEEP_SCRATCH=1
  expect_status "$status" 1 "a kept scratch directory"
  expect_contains "$out" "FACT scratch-removed: FAIL" "a kept scratch directory: the row"
  expect_contains "$out" "is still there, at $scratch" "a kept scratch directory: the row names the directory"
  expect_contains "$err" "CHECK631_KEEP_SCRATCH is set" "a kept scratch directory: the reason"
  [ -d "$scratch" ] || fail "a kept scratch directory: the directory was removed anyway"
  rm -rf "$scratch"
}

test_an_unknown_argument_is_refused() {
  local dir=$work/unknown
  prepare "$dir"
  run_check "$dir" -- --wat
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument: --wat" "an unknown argument: the message"
}

# ------------------------------------------------------------- the preflight
test_preflight_passes_and_writes_nothing() {
  local dir=$work/preflight-ok
  prepare "$dir"
  run_check "$dir" -- --preflight
  expect_status "$status" 0 "a preflight with nothing blocking"
  expect_contains "$err" "preflight passed" "a preflight: the pass line"
  expect_contains "$err" "read: the installation is $installation" "a preflight: the installation it read"
  expect_contains "$err" "read: the craft $craft declares the access" "a preflight: the craft's access it read"
  expect_contains "$err" "read: the two names this check declares" "a preflight: the two names it read are free"
  expect_absent "$out" "CHECK 631 RESULT" "a preflight: no result block is printed"
  expect_absent "$fugaro_calls" "accounts add" "a preflight: no credential is declared"
  expect_absent "$fugaro_calls" "accounts pool add" "a preflight: no pool is written"
  expect_absent "$fugaro_calls" "project policy publish" "a preflight: no node is published"
  expect_absent "$fugaro_calls" "fugaro run" "a preflight: no run is launched"
  assert_scratch_removed "a preflight"
}

test_preflight_reports_every_blocker_at_once() {
  local dir=$work/preflight-blocked
  prepare "$dir"
  run_check "$dir" CHECK631_CRAFT=ghost CHECK631_ANTHROPIC=work -- --preflight
  expect_status "$status" 1 "a preflight with blockers"
  expect_contains "$err" "BLOCKED declaration: the installation names no craft ghost" "a preflight: the missing craft"
  expect_contains "$err" "BLOCKED add-anthropic: the credential work is already declared" "a preflight: the credential already declared"
  expect_contains "$err" "fix: CHECK631_CRAFT=$craft bash scripts/live-checks/check631.sh --sandbox $dir/sandbox" "a preflight: the command that names a craft"
  expect_contains "$err" "fix: fugaro accounts rm work" "a preflight: the command that clears the credential"
  expect_contains "$err" "found 2 blocker(s)" "a preflight: both blockers at once"
  expect_absent "$fugaro_calls" "accounts add" "a preflight: no credential is declared"
  expect_absent "$fugaro_calls" "fugaro run" "a preflight: no run is launched"
}

test_preflight_is_refused_for_a_coding_agents_session() {
  local dir=$work/preflight-agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1 -- --preflight
  expect_status "$status" 1 "a coding agent's preflight"
  expect_contains "$err" "a coding agent's session" "a preflight: the session refusal"
  expect_absent "$fugaro_calls" "accounts ls --json" "a preflight: no live read is made"
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
t test_dry_run_needs_no_terminal
t test_the_happy_path_at_a_terminal
t test_the_value_never_reaches_a_file_or_a_history
t test_a_value_in_the_config_directory_is_a_finding
t test_a_value_in_a_shell_history_is_a_finding
t test_a_command_that_echoes_the_value_is_a_finding
t test_a_prompt_that_echoes_a_key_shape_is_a_finding
t test_the_two_pastes_are_read_at_two_different_prompts
t test_with_no_terminal_and_no_value_file_the_check_stops
t test_a_session_that_is_refused
t test_a_session_that_is_not_refused_is_a_finding
t test_a_session_refusal_that_names_nothing_is_a_finding
t test_the_project_flag_is_passed_through
t test_the_agent_refusal_row_reads_the_refusal_own_words
t test_a_craft_with_no_access_is_not_measured
t test_an_installation_with_no_craft_is_not_measured
t test_a_read_that_does_not_answer_is_not_measured
t test_a_pool_that_cannot_be_declared_is_not_measured
t test_a_publish_that_fails_is_not_measured
t test_a_launch_that_fails_is_a_finding
t test_a_run_that_never_ends_is_not_measured
t test_a_record_that_is_another_runs_is_a_finding
t test_a_record_that_cannot_be_read_is_a_finding
t test_an_audit_line_naming_another_credential_is_a_finding
t test_an_audit_read_that_does_not_answer_falls_back_to_the_record
t test_an_audit_line_that_names_no_credential_is_a_finding
t test_a_removal_accepted_while_a_pool_lists_it_is_a_finding
t test_a_removal_refused_without_a_next_step_is_a_finding
t test_a_credential_still_declared_after_the_removals_is_a_finding
t test_the_session_guard_stops_the_check
t test_a_doctor_read_that_names_belong_stops_the_check
t test_a_credential_naming_belong_is_refused
t test_a_doctor_read_that_answers_no_project_is_not_measured
t test_a_doctor_read_that_does_not_answer_is_not_measured
t test_xtrace_refused
t test_a_missing_sandbox_is_refused
t test_a_path_that_is_not_a_checkout_is_refused
t test_an_empty_value_file_is_not_measured
t test_kept_scratch_is_reported
t test_an_unknown_argument_is_refused
t test_preflight_passes_and_writes_nothing
t test_preflight_reports_every_blocker_at_once
t test_preflight_is_refused_for_a_coding_agents_session

if [ "$failures" -ne 0 ]; then
  echo "check631_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check631_test.sh: ok"
