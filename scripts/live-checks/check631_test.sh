#!/usr/bin/env bash
# Tests for scripts/live-checks/check631.sh, with a fake `fugaro`, a fake
# `gcloud` and (for the cases that get past the check's terminal gate) a real pty
# on PATH: NOTHING here reaches a cloud, an installation, a secret or a run, no
# credential is declared anywhere, nothing is published, and the fakes record
# every call they are given — in which directory it was made — so a case can
# assert what the check did and did not do.
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
# The fake's `accounts add --type` carries the managed gate's own door: the
# declaration runs only where the run's input AND its output are a real terminal
# and no coding agent's marker is set (accountsAddOwnTerminal,
# internal/cli/accounts_managed.go) — a pipe, a redirect or a session is refused
# before a byte is written — and it then reads its value at its OWN hidden prompt,
# the sentence the acceptance names.
#
# The two values are typed for real: the cases that get past the check's terminal gate
# start a pty (python3's pty.openpty) and hand the check its slave device
# through CHECK631_TTY, with the two values written into it — each command's own
# hidden prompt reads its own, in the order the check runs them. The check never
# holds either value: not an argument, not a variable, not a file. A machine that
# can make no pty (a sandbox or a coding agent's session) runs every case that
# needs no terminal and skips the rest with one line each.
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

# accounts_add is the managed gate's own door, in the words the release prints:
# a session is refused the credential entry and told the narrower step it can
# take (accountsAddRefusal), a caller with anything but a real terminal on both
# streams is refused before a byte is written, and the owner's own run reads the
# value at the command's own hidden prompt on stderr.
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
    printf 'fugaro: This creates the credential credentials.%s, and a managed project'\''s owner is the one who creates credentials.\nDeclare the account without --type, or run this in your own terminal.\n' "$name" >&2
    exit 1
  fi
  if [ "$mode" != session-accepts ] && { [ ! -t 0 ] || [ ! -t 1 ]; }; then
    printf 'fugaro: This creates the credential credentials.%s, and a managed project'\''s owner is the one who creates credentials.\nRun this in your own terminal.\n' "$name" >&2
    exit 1
  fi
  if [ "$mode" = add-fails ]; then
    printf 'fugaro: the value is shorter than 16 bytes, too short to redact from logs\n' >&2
    exit 1
  fi
  if [ "$mode" = add-outlives ]; then
    # The command outstays its bound on purpose, by seconds, and writes a marker
    # when it does: the bound a case gives it is one second, so a marker says the
    # ending that row reports did not reach this command.
    printf 'Paste the value for credential-%s-key (input hidden), then press Enter: ' "$name" >&2
    sleep 5
    printf '%s\n' "$name" >>"$FAKE_LOG_DIR/outlived"
    printf 'fugaro: declared %s\n' "$name"
    return 0
  fi
  if [ "$mode" = add-interrupts ]; then
    # The command's own hidden prompt, and the owner's own interrupt. The echo
    # goes off the way the release takes it off the terminal it reads, and the pid
    # is written before the prompt so the case can say afterwards whether this
    # command is gone.
    printf '%s\n' "$$" >"$FAKE_LOG_DIR/prompt.pid"
    stty -echo
  fi
  if [ "$mode" != add-no-prompt ]; then
    printf 'Paste the value for credential-%s-key (input hidden), then press Enter: ' "$name" >&2
  fi
  if [ "$mode" = add-interrupts ]; then
    # The signal a person pressing Ctrl-C at this prompt sends the check. This
    # command is a grandchild of the check (its parent is the runner), so the pid
    # is walked up rather than searched for: the case signals the check that is
    # reading this prompt and not another shard's.
    local check_pid
    check_pid=$(ps -o ppid= -p "$(ps -o ppid= -p "$$" 2>/dev/null | tr -d ' ')" 2>/dev/null | tr -d ' ')
    [ -n "$check_pid" ] || printf 'fugaro: no check process was found to interrupt\n' >&2
    kill -INT "$check_pid" 2>/dev/null || true
  fi
  # The value this read answers is typed at this declaration's own announcement.
  # At a terminal, end-of-file reads nothing at all — the terminal's other end is
  # gone — and the declaration it would otherwise report is one about a value
  # nothing read: that is a failure, not an empty declaration. A run with no
  # terminal on its input (the refusal act's) has nothing to read by design and
  # is measured by the gate above instead.
  if [ -t 0 ]; then
    if ! IFS= read -r value; then
      printf 'fugaro: nothing was read at the prompt for credential-%s-key: the terminal gave end-of-file\n' "$name" >&2
      exit 1
    fi
  else
    IFS= read -r value || true
  fi
  if [ "$mode" = add-interrupts ]; then
    # The read returned, so the ending that ends a declaration is not what ended
    # this one: the echo goes back, the way the release puts it back itself.
    stty echo
  fi
  if [ "$mode" = add-reads-ahead ]; then
    # The read this case is about: the command takes its own line and then reads
    # again, for whatever else the terminal holds. A value written into the
    # terminal ahead of the declaration it belongs to is what that second read
    # takes, and the declaration it belongs to is then left with nothing.
    IFS= read -r -t 1 _ahead || true
  fi
  if [ "$mode" = add-echoes-shape ]; then
    # The key-shaped string this case is about is built here, from parts: no
    # literal in this repository is shaped like a credential.
    printf 'fugaro: storing the value sk-ant-%s now\n' "not-a-real-key" >&2
  fi
  if [ "$mode" = add-openrouter-echoes-shape ]; then
    printf 'fugaro: the value sk-or-v1-%s was accepted\n' "not-a-real-key" >&2
  fi
  if [ "$name" = "$anthropic" ]; then
    printf "fugaro: no job is deployed for %s: a key credential is not a run's Claude account token\n" "$name" >&2
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
    node_access=$(cat "$state/access")
    if [ "$mode" = craft-no-access ]; then node_access=""; fi
    printf '{"project":"%s","policy":{"version":10,"craft_credentials":[{"name":"%s","type":"anthropic"}],"craft_pools":[{"name":"%s","strategy":"failover","members":["%s"]}],"crafts":{"%s":{"model":"deepseek/deepseek-v4","access":"%s"}}}}\n' \
      "$installation" "$anthropic" "$pool" "$anthropic" "$craft" "$node_access"
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

write_feeder() { # dir — the pty the two declarations run at
  cat >"$1/feeder.py" <<'PY'
import os
import pty
import sys
import time

# A terminal for the two declarations, for this test only. The slave's path is
# printed first (the check is handed it through CHECK631_TTY), and each value is
# written into it only once the declaration it answers has announced itself on
# the check's stderr. A value is therefore typed at its own declaration's
# announcement and never earlier: a command that reads past its own line finds
# nothing else to take, and the declaration that follows still finds its value.
# The master is held open until the test kills this process, because a terminal
# whose master is gone gives its reader end-of-file.
err_path = sys.argv[1]
answers = list(zip(sys.argv[2::2], sys.argv[3::2]))

master, slave = pty.openpty()
sys.stdout.write(os.ttyname(slave) + "\n")
sys.stdout.flush()

deadline = time.time() + float(os.environ.get("PTY_HOLD", "120"))
for needle, value in answers:
    while time.time() < deadline:
        try:
            with open(err_path, errors="replace") as handle:
                seen = handle.read()
        except OSError:
            seen = ""
        if needle in seen:
            break
        time.sleep(0.02)
    os.write(master, (value + "\n").encode())
time.sleep(max(0.0, deadline - time.time()))
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
# Whether this machine can make a pty at all: every case that gets past the
# check's terminal gate needs one, and a sandbox or a coding agent's session
# cannot make one — python3's pty.openpty raises "out of pty devices". Probed
# once.
PTY_AVAILABLE=""
pty_available() {
  if [ -z "$PTY_AVAILABLE" ]; then
    if python3 -c 'import os, pty
m, s = pty.openpty()
os.close(m)
os.close(s)' >/dev/null 2>&1; then
      PTY_AVAILABLE=yes
    else
      PTY_AVAILABLE=no
    fi
  fi
  [ "$PTY_AVAILABLE" = yes ]
}

# need_pty: a case that needs a terminal for the two declarations skips itself,
# with one line, where this machine has no pty, and runs where it has one.
need_pty() {
  pty_available && return 0
  echo "skip: ${case_name:-this case}: no pty on this machine (a sandbox or an agent session has none), so the two credentials are not declared"
  return 1
}

# pty_echo DEV — what the terminal at DEV says about its echo, yes or no. The
# terminal the declarations run at is this test's own pty, so what a command did
# to it is still readable here after the check has gone: that is how a case says
# the terminal was given back.
pty_echo() {
  python3 - "$1" <<'PY'
import sys
import termios

with open(sys.argv[1], "rb") as handle:
    attrs = termios.tcgetattr(handle)
print("yes" if attrs[3] & termios.ECHO else "no")
PY
}

FEEDER_PID=""
FEEDER_TTY=""
start_feeder() { # start_feeder DIR [NEEDLE VALUE]... — each value is typed once its NEEDLE is on the check's stderr
  local dir=$1
  shift
  FEEDER_PID=""
  FEEDER_TTY=""
  : >"$dir/feeder.tty"
  PTY_HOLD=120 python3 "$dir/feeder.py" "$dir/err" "$@" >"$dir/feeder.tty" 2>"$dir/feeder.err" &
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
    CHECK631_DECLARE_TIMEOUT_SECONDS=20 \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" ${cli[@]+"${cli[@]}"} \
    </dev/null >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  fugaro_calls=$(cat "$dir/logs/fugaro.calls")
  gcloud_calls=$(cat "$dir/logs/gcloud.calls")
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
}

# A run at a terminal: each declaration reads its own value at its own hidden
# prompt, so every case that gets past the check's terminal gate makes the pty
# below and types each value at the announcement of the declaration it answers —
# at no other time, so a command that reads past its own line cannot take the
# value the other declaration reads. CHECK631_TEST_VALUE_ONE and
# CHECK631_TEST_VALUE_TWO name the two, where a case needs its own.
run_check_with_terminal() { # run_check_with_terminal DIR ENV... [-- ARGS...]
  local dir=$1
  shift
  start_feeder "$dir" \
    "declaring the Anthropic-key credential" "${CHECK631_TEST_VALUE_ONE:-$value_one}" \
    "declaring the OpenRouter credential" "${CHECK631_TEST_VALUE_TWO:-$value_two}"
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
  expect_contains "$out" "FACT add-anthropic: INFO" "a dry run: the planned rows are not measured"
  expect_contains "$err" "[YOU, write] fugaro accounts add $anthropic --type anthropic at your own terminal" "a dry run: the plan names the declaration it would make"
  expect_contains "$err" "$openrouter --type openrouter at your own terminal" "a dry run: the plan names the second declaration"
  expect_absent "$fugaro_calls" "accounts add" "a dry run: nothing is declared"
  expect_absent "$fugaro_calls" "fugaro run" "a dry run: no run is launched"
  assert_scratch_removed "a dry run"
}

test_the_happy_path_at_a_terminal() {
  local dir=$work/happy
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir"
  expect_status "$status" 0 "the happy path"
  local key
  for key in fugaro-version xtrace agent-session belong installation declaration agent-refusal add-anthropic add-openrouter prompt-hidden nothing-echoed no-file no-history serving-path wiring publish run-launched terminal record served-by unwiring rm-refusal rm-clears restore scratch-removed; do
    expect_contains "$out" "FACT $key: PASS" "the happy path: the $key row"
  done
  expect_absent "$out" ": FAIL" "the happy path: no row failed"
  expect_contains "$out" "CHECK 631 RESULT END (exit 0)" "the happy path: the block ends"
  expect_contains "$out" "credential-$anthropic-key, credential-$openrouter-key" "the happy path: the prompt row names both commands' own prompts"
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

test_no_key_shape_reaches_a_file_or_a_history() {
  local dir=$work/sweeps
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir"
  expect_status "$status" 0 "the sweeps"
  expect_contains "$out" "FACT no-file: PASS" "the sweeps: the config directory holds no key shape"
  expect_contains "$out" "no line of the" "the sweeps: the config sweep says what it read"
  expect_contains "$out" "FACT no-history: PASS" "the sweeps: the history holds no key shape"
  expect_contains "$out" "$dir/home/.bash_history" "the sweeps: the history file it read is named"
  # The block names the files it looked at and never a value: this check holds
  # none, and the two the commands read at their own prompts are printed nowhere.
  expect_absent "$out" "$value_one" "the sweeps: the first value is in no line of the block"
  expect_absent "$err" "$value_one" "the sweeps: the first value is in no line of the progress output"
  expect_absent "$out" "$value_two" "the sweeps: the second value is in no line of the block"
  expect_absent "$err" "$value_two" "the sweeps: the second value is in no line of the progress output"
}

test_a_key_shape_in_the_config_directory_is_a_finding() {
  local dir=$work/config-holds
  prepare "$dir"
  # The key-shaped line this case is about is built here, from parts: no literal
  # in this repository is shaped like a credential.
  printf '{"note":"sk-ant-%s"}\n' "not-a-real-key" >"$dir/home/.config/fugaro/cache.json"
  need_pty || return 0
  run_check_with_terminal "$dir"
  expect_status "$status" 1 "a key shape in the config directory"
  expect_contains "$out" "FACT no-file: FAIL" "a key shape in the config directory: the row"
  expect_contains "$out" "is in 1 file(s) under" "a key shape in the config directory: the row says how many"
  expect_contains "$out" "cache.json" "a key shape in the config directory: the file is named"
  expect_absent "$out" "sk-ant-not-a-real-key" "a key shape in the config directory: the line itself is never printed"
}

test_a_key_shape_in_a_shell_history_is_a_finding() {
  local dir=$work/history-holds
  prepare "$dir"
  printf 'fugaro accounts add %s --type anthropic sk-ant-%s\n' "$anthropic" "not-a-real-key" >>"$dir/home/.bash_history"
  need_pty || return 0
  run_check_with_terminal "$dir"
  expect_status "$status" 1 "a key shape in a shell history"
  expect_contains "$out" "FACT no-history: FAIL" "a key shape in a shell history: the row"
  expect_contains "$out" ".bash_history" "a key shape in a shell history: the file is named"
}

test_a_command_that_echoes_a_key_shape_is_a_finding() {
  local dir=$work/echoes
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=add-echoes-shape
  expect_status "$status" 1 "a command that echoes a key shape"
  expect_contains "$out" "FACT nothing-echoed: FAIL" "a command that echoes a key shape: the row"
  expect_contains "$out" "carries a line shaped like a provider key" "a command that echoes a key shape: the row says so"
  # The check names the stream and never the line.
  expect_absent "$out" "sk-ant-not-a-real-key" "a command that echoes a key shape: the check does not reprint the string"
  # The check relays each declaration's output to its own stderr, which is the
  # screen the person at the terminal is reading: a key shape it relayed there
  # would be in that screen and in the transcript of the run that printed it.
  expect_absent "$err" "sk-ant-not-a-real-key" "a command that echoes a key shape: the check does not reprint the string on the screen it relays the command's output to"
}

test_the_second_command_echoing_a_key_shape_is_a_finding() {
  local dir=$work/shape
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=add-openrouter-echoes-shape
  expect_status "$status" 1 "the second command echoing a key shape"
  expect_contains "$out" "FACT nothing-echoed: FAIL" "the second command echoing a key shape: the row"
  expect_contains "$out" "credential-$openrouter-key" "the second command echoing a key shape: the row names which command"
  expect_absent "$out" "sk-or-v1-not-a-real-key" "the second command echoing a key shape: the check does not reprint the string"
  expect_absent "$err" "sk-or-v1-not-a-real-key" "the second command echoing a key shape: the check does not reprint the string on the screen it relays the command's output to"
}

test_a_command_that_prints_no_prompt_is_a_finding() {
  local dir=$work/no-prompt
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=add-no-prompt
  expect_status "$status" 1 "a command that prints no prompt"
  expect_contains "$out" "FACT prompt-hidden: FAIL" "a command that prints no prompt: the row"
  expect_contains "$out" "does not carry the command's own hidden prompt" "a command that prints no prompt: the row says so"
  expect_contains "$out" "FACT add-anthropic: PASS" "a command that prints no prompt: the declaration itself still worked"
}

test_both_commands_read_at_their_own_prompts() {
  local dir=$work/prompts
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir"
  expect_status "$status" 0 "the two prompts"
  expect_contains "$out" "each declaration carries the command's own hidden prompt" "the two prompts: each command's own prompt is the one its value came from"
  expect_contains "$out" "credential-$anthropic-key, credential-$openrouter-key" "the two prompts: both prompts are named"
  expect_contains "$out" "FACT add-openrouter: PASS" "the two prompts: the second type was declared"
}

test_with_no_terminal_the_check_stops() {
  local dir=$work/no-terminal
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 1 "no terminal"
  expect_contains "$out" "FACT aborted: FAIL" "no terminal: the run is aborted"
  expect_contains "$out" "this check needs your own terminal" "no terminal: the message"
  expect_absent "$fugaro_calls" "accounts add" "no terminal: nothing is declared"
}

# The two declarations are measured against their OWN bound, and this case is
# what says so: a terminal is held open with nothing written into it, so the
# first declaration has no value to read and its row can only end on a bound.
# The bound it ends on is the declaration's, injected at a second here — which
# is the whole point of the knob beside the pacing one, since a tight bound on a
# command that reads at a terminal is a verdict about the machine rather than
# about the command. A number nothing reads would put that verdict back.
test_a_declaration_is_measured_against_its_own_bound() {
  local dir=$work/declare-bound
  prepare "$dir"
  need_pty || return 0
  start_feeder "$dir"
  run_check "$dir" CHECK631_DECLARE_TIMEOUT_SECONDS=1
  stop_feeder
  expect_status "$status" 2 "a declaration nothing answers"
  expect_contains "$out" "FACT add-anthropic: FAIL — fugaro accounts add $anthropic --type anthropic did not finish inside 1s" "a declaration nothing answers: the row ends on the declaration's own bound"
  expect_absent "$out" "FACT add-anthropic: PASS" "a declaration nothing answers: no value was read"
}

# A value is typed at the declaration it belongs to, and at no other time. The
# command that reads one declaration's value is not the only reader of the
# terminal this check holds: a command that reads again after its own line — a
# reader that takes whatever the terminal holds — must not be able to take the
# value the OTHER declaration reads. The fake below reads its own line and then
# reads again; with both values written into the terminal ahead of the check, it
# takes the second one and the second declaration is left with nothing to read,
# so its row can only end on its own bound.
test_a_declaration_that_reads_past_its_line_does_not_take_the_other_value() {
  local dir=$work/reads-ahead
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=add-reads-ahead CHECK631_DECLARE_TIMEOUT_SECONDS=5
  expect_status "$status" 0 "a declaration that reads past its line"
  expect_contains "$out" "FACT add-anthropic: PASS" "a declaration that reads past its line: the first declaration read its value"
  expect_contains "$out" "FACT add-openrouter: PASS" "a declaration that reads past its line: the second declaration still read its own"
}

# The owner's own interrupt, at the command's own hidden prompt. A declaration
# reads the owner's terminal with the echo off, and a person interrupting has to
# get both back: the runner is the process that can end the command (it leads a
# session of its own, so no signal this check sends reaches it) and the one that
# knows what the terminal looked like. The fake below stands at that prompt, takes
# the echo off the way the release does, and sends the check the interrupt; the
# case measures what is here once the check has gone — the command gone, the echo
# back.
test_an_interrupt_at_the_prompt_ends_the_command_and_puts_the_terminal_back() {
  local dir=$work/interrupted
  prepare "$dir"
  need_pty || return 0
  start_feeder "$dir"
  run_check "$dir" FAKE_MODE=add-interrupts
  expect_status "$status" 130 "an interrupt at the prompt"
  local command_pid
  command_pid=$(cat "$dir/logs/prompt.pid" 2>/dev/null || true)
  [ -n "$command_pid" ] || fail "an interrupt at the prompt: the command never reached its prompt"
  if [ -n "$command_pid" ] && kill -0 "$command_pid" 2>/dev/null; then
    fail "an interrupt at the prompt: the command (pid $command_pid) is still running with the terminal held"
  fi
  [ "$(pty_echo "$FEEDER_TTY")" = yes ] || fail "an interrupt at the prompt: the terminal's echo is off after the check has gone"
  stop_feeder
}

# The ending a bound asks for is the command's own. bound_loop signals the
# runner this check starts, and the command runs in a session of its own, so a
# runner that died on that signal alone would leave the command running with the
# owner's terminal still on its input — and the row would say "was ended" about a
# command nothing ended. The fake below outlives its one-second bound by seconds
# and writes a marker when it does; the owner's terminal stays held open for the
# whole of it, which is what a surviving command would be holding.
test_a_command_a_bound_ended_is_not_still_running() {
  local dir=$work/declare-ended
  prepare "$dir"
  need_pty || return 0
  start_feeder "$dir" \
    "declaring the Anthropic-key credential" "${CHECK631_TEST_VALUE_ONE:-$value_one}" \
    "declaring the OpenRouter credential" "${CHECK631_TEST_VALUE_TWO:-$value_two}"
  run_check "$dir" CHECK631_DECLARE_TIMEOUT_SECONDS=1 FAKE_MODE=add-outlives
  sleep 6
  [ ! -e "$dir/logs/outlived" ] || fail "a command a bound ended is still running: $(cat "$dir/logs/outlived") outlived the bound that ended it, with the terminal still held open"
  stop_feeder
  expect_status "$status" 2 "a command a bound ended"
}

test_a_session_that_is_refused() {
  local dir=$work/session
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir"
  expect_status "$status" 0 "a session that is refused"
  expect_contains "$out" "FACT agent-refusal: PASS" "a session that is refused: the row"
  expect_contains "$out" "This creates the credential credentials.$anthropic" "a session that is refused: the refusal's own sentence is quoted"
  expect_contains "$out" "names $anthropic no" "a session that is refused: the row says the declaration was not written either"
  expect_contains "$fugaro_calls" "fugaro accounts add $anthropic --type anthropic" "a session that is refused: the act was run"
}

test_a_session_that_is_not_refused_is_a_finding() {
  local dir=$work/session-accepts
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=session-accepts
  expect_status "$status" 1 "a session that is not refused"
  expect_contains "$out" "FACT agent-refusal: FAIL" "a session that is not refused: the row"
  expect_contains "$out" "exited 0 with CLAUDECODE set" "a session that is not refused: the row says what happened"
}

test_a_session_refusal_that_names_nothing_is_a_finding() {
  local dir=$work/session-unnamed
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=session-unnamed
  expect_status "$status" 1 "a session refusal that names nothing"
  expect_contains "$out" "FACT agent-refusal: FAIL" "a session refusal that names nothing: the row"
  expect_contains "$out" "does not name the step it may not take" "a session refusal that names nothing: the row says what is missing"
}

test_the_project_flag_is_passed_through() {
  local dir=$work/with-project
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" CHECK631_PROJECT="$installation"
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
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=session-other-words
  expect_status "$status" 1 "a refusal in other words"
  expect_contains "$out" "FACT agent-refusal: FAIL" "a refusal in other words: the row"
  expect_contains "$out" "a key is written for the person it belongs to and for nobody else: $anthropic was declared, and its value was not read" "a refusal in other words: the row quotes the refusal's own words"
}

test_a_craft_with_no_access_is_not_measured() {
  local dir=$work/craft-no-access
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=craft-no-access
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
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=no-craft
  expect_status "$status" 2 "an installation with no craft"
  expect_contains "$out" "names no craft '$craft'" "an installation with no craft: the row names the craft it looked for"
  expect_absent "$fugaro_calls" "accounts pool add" "an installation with no craft: no pool is declared"
}

test_a_read_that_does_not_answer_is_not_measured() {
  local dir=$work/crafts-fail
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=crafts-fail
  expect_status "$status" 2 "a crafts read that does not answer"
  expect_contains "$err" "could NOT measure declaration" "a crafts read that does not answer: it is named as unmeasured"
  expect_absent "$fugaro_calls" "accounts pool add" "a crafts read that does not answer: no pool is declared"
}

test_a_pool_that_cannot_be_declared_is_not_measured() {
  local dir=$work/pool-add-fails
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=pool-add-fails
  expect_status "$status" 2 "a pool that cannot be declared"
  expect_contains "$out" "FACT wiring: FAIL" "a pool that cannot be declared: the row"
  expect_contains "$err" "could NOT measure wiring, publish, run-launched" "a pool that cannot be declared: nothing is published and no run is launched"
  expect_absent "$fugaro_calls" "project policy publish" "a pool that cannot be declared: nothing is published"
  expect_absent "$fugaro_calls" "fugaro run" "a pool that cannot be declared: no run is launched"
}

test_a_publish_that_fails_is_not_measured() {
  local dir=$work/publish-fails
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=publish-fails
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
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=launch-fails
  expect_status "$status" 1 "a launch that fails"
  expect_contains "$out" "FACT run-launched: FAIL" "a launch that fails: the row"
  expect_contains "$out" "never started" "a launch that fails: the row says so"
  expect_contains "$err" "could NOT measure terminal, record, served-by" "a launch that fails: the rows below are unmeasured"
}

test_a_run_that_never_ends_is_not_measured() {
  local dir=$work/never-ends
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=run-never-ends \
    CHECK631_RUN_TIMEOUT_SECONDS=2 CHECK631_POLL_SECONDS=1
  expect_status "$status" 2 "a run that never ends"
  expect_contains "$out" "FACT terminal: FAIL" "a run that never ends: the row"
  expect_contains "$out" "had not ended after 2s" "a run that never ends: the row names the bound"
  expect_contains "$out" "FACT rm-clears: PASS" "a run that never ends: the later rows still run and unwind what the check wrote"
}

test_a_record_that_is_another_runs_is_a_finding() {
  local dir=$work/record-other
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=record-wrong-run
  expect_status "$status" 1 "a record that is another run's"
  expect_contains "$out" "FACT record: FAIL" "a record that is another run's: the row"
  expect_contains "$out" "the record at that key is another run's" "a record that is another run's: the row says so"
  expect_contains "$err" "could NOT measure served-by" "a record that is another run's: the row below it is unmeasured"
}

test_a_record_that_cannot_be_read_is_a_finding() {
  local dir=$work/record-fails
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=record-fails
  expect_status "$status" 1 "a record that cannot be read"
  expect_contains "$out" "FACT record: FAIL" "a record that cannot be read: the row"
  expect_contains "$out" "could not be read" "a record that cannot be read: the row says so"
}

test_an_audit_line_naming_another_credential_is_a_finding() {
  local dir=$work/audit-other
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=audit-other
  expect_status "$status" 1 "an audit line naming another credential"
  expect_contains "$out" "FACT served-by: FAIL" "an audit line naming another credential: the row"
  expect_contains "$out" "some-other-credential" "an audit line naming another credential: the row names what served it"
}

test_an_audit_read_that_does_not_answer_falls_back_to_the_record() {
  local dir=$work/audit-fails
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=audit-fails
  expect_status "$status" 0 "an audit read that does not answer"
  expect_contains "$out" "FACT served-by: PASS" "an audit read that does not answer: the record's own stages carry the reading"
  expect_contains "$out" "the run's own record names $anthropic as the credential its stages were served by" "an audit read that does not answer: the row says where it read it"
}

test_an_audit_line_that_names_no_credential_is_a_finding() {
  local dir=$work/audit-refused
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=audit-refused \
    FAKE_RECORD_CREDENTIAL=none
  expect_status "$status" 1 "an audit line that names no credential"
  expect_contains "$out" "FACT served-by: FAIL" "an audit line that names no credential: the row"
}

test_a_removal_accepted_while_a_pool_lists_it_is_a_finding() {
  local dir=$work/rm-accepts
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=rm-accepts
  expect_status "$status" 1 "a removal that is accepted"
  expect_contains "$out" "FACT rm-refusal: FAIL" "a removal that is accepted: the row"
  expect_contains "$out" "exited 0 while the pool $pool listed it" "a removal that is accepted: the row says so"
}

test_a_removal_refused_without_a_next_step_is_a_finding() {
  local dir=$work/rm-unnamed
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=rm-unnamed
  expect_status "$status" 1 "a removal refused without a next step"
  expect_contains "$out" "FACT rm-refusal: FAIL" "a removal refused without a next step: the row"
  expect_contains "$out" "names no step that clears it" "a removal refused without a next step: the row says what is missing"
}

test_a_credential_still_declared_after_the_removals_is_a_finding() {
  local dir=$work/removal-sticks
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=removal-does-not-stick
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
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=doctor-belong
  expect_status "$status" 1 "an installation naming belong"
  expect_contains "$out" "FACT aborted: FAIL" "an installation naming belong: the run is aborted"
  expect_contains "$out" "belongs to belong-sandbox" "an installation naming belong: the message names the project"
  expect_absent "$fugaro_calls" "accounts add" "an installation naming belong: nothing is declared"
}

test_a_credential_naming_belong_is_refused() {
  local dir=$work/belong-cred
  prepare "$dir"
  run_check "$dir" CHECK631_POOL=belong-pool
  expect_status "$status" 1 "a pool naming belong"
  expect_contains "$out" "FACT belong: FAIL" "a pool naming belong: the row"
  expect_contains "$out" "names belong: this check is never run there" "a pool naming belong: the message says so by name"
  expect_absent "$fugaro_calls" "accounts add" "a pool naming belong: nothing is declared"
}

test_a_doctor_read_that_answers_no_project_is_not_measured() {
  local dir=$work/no-project
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=doctor-no-project
  expect_status "$status" 2 "a doctor read with no project line"
  expect_contains "$out" "FACT installation: FAIL" "a doctor read with no project line: the row"
  expect_contains "$err" "could NOT measure installation, declaration" "a doctor read with no project line: the values are never asked for"
  expect_contains "$err" "add-anthropic, add-openrouter, prompt-hidden, nothing-echoed" "a doctor read with no project line: the declaration steps are unmeasured too"
  expect_absent "$fugaro_calls" "accounts add" "a doctor read with no project line: nothing is declared"
}

test_a_doctor_read_that_does_not_answer_is_not_measured() {
  local dir=$work/doctor-fail
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" FAKE_MODE=doctor-fail
  expect_status "$status" 2 "a doctor read that does not answer"
  expect_contains "$err" "could NOT measure installation" "a doctor read that does not answer: it is named as unmeasured"
  expect_absent "$fugaro_calls" "accounts add" "a doctor read that does not answer: nothing is declared"
}

test_xtrace_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- --xtrace
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
  run_check "$dir"
  expect_status "$status" 2 "a path that is not a checkout"
  expect_contains "$err" "carries no fugaro.yaml" "a path that is not a checkout: the message"
  expect_absent "$fugaro_calls" "accounts add" "a path that is not a checkout: nothing is declared"
}

test_kept_scratch_is_reported() {
  local dir=$work/kept
  prepare "$dir"
  need_pty || return 0
  run_check_with_terminal "$dir" CHECK631_KEEP_SCRATCH=1
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
  case_name=$1
  "$1"
}

t test_help
t test_dry_run_needs_no_terminal
t test_the_happy_path_at_a_terminal
t test_no_key_shape_reaches_a_file_or_a_history
t test_a_key_shape_in_the_config_directory_is_a_finding
t test_a_key_shape_in_a_shell_history_is_a_finding
t test_a_command_that_echoes_a_key_shape_is_a_finding
t test_the_second_command_echoing_a_key_shape_is_a_finding
t test_a_command_that_prints_no_prompt_is_a_finding
t test_both_commands_read_at_their_own_prompts
t test_with_no_terminal_the_check_stops
t test_a_declaration_is_measured_against_its_own_bound
t test_a_declaration_that_reads_past_its_line_does_not_take_the_other_value
t test_an_interrupt_at_the_prompt_ends_the_command_and_puts_the_terminal_back
t test_a_command_a_bound_ended_is_not_still_running
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
