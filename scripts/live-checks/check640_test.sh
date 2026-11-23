#!/usr/bin/env bash
# Tests for scripts/live-checks/check640.sh, against fake tools on PATH: nothing
# here reaches the network, no credential exists, no Fugaro installation is read
# and no pull request is touched. `gh`, `gcloud` and `fugaro` are a few lines of
# bash each, answering from a FAKE640_* knob and recording what they were asked;
# `git` fails every call, so a check that ran git of its own (or needed a
# checkout) would show up here as a failure.
#
# What the fakes model is the DAEMON's own behaviour over time: the fake
# `fugaro runs ls` counts its calls and answers a run that is still in flight
# until FAKE640_LIVE_TICKS of them have gone by, and the fake `gh pr view` reads
# the same counter — so the pull request carries fugaro:running exactly while
# the run is live and the settled labels after, which is what the real daemon
# does at its ticks (its default interval is 30s).
#
# Usage: bash scripts/live-checks/check640_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check640.sh
bash_bin=$(command -v bash)
# The PHYSICAL path, so a child's own getcwd() and the path this test uses are
# spelled the same way (macOS: /var is a symlink to /private/var).
work=$(cd "$(mktemp -d)" && pwd -P)
trap 'if [ -n "${CHECK640_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi' EXIT

failures=0
fail() { echo "FAIL: $*" >&2; failures=$((failures + 1)); }

# rows_of_block TEXT — the block's own rows, one short line each, so a failure
# names what the run recorded instead of pasting the whole block into the log.
rows_of_block() {
  printf '%s\n' "$1" | grep '^FACT ' | cut -c1-110 | tr '\n' '|' | head -c 1400
}
expect_contains() { # haystack needle what
  case "$1" in
  *"$2"*) ;;
  *) fail "$3: expected to contain '$2'; the rows read: $(rows_of_block "$1")" ;;
  esac
}
expect_absent() {
  case "$1" in
  *"$2"*) fail "$2: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1; the rows read: $(rows_of_block "${out:-}")"
}
# expect_last_line OUT WANT WHAT — the block's own last line, exactly. It is the
# line a reader takes as the answer, and the one the exit status has to agree
# with.
expect_last_line() { # out want what
  local last
  last=$(printf '%s\n' "$1" | grep -v '^[[:space:]]*$' | tail -n 1)
  [ "$last" = "$2" ] || fail "$3: the block's last line reads '$last', want '$2'"
}
expect_block_keys() { # out what
  local key count
  expect_contains "$1" "CHECK 640 RESULT" "$2: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$1" "FACT $key:" "$2: the $key row"
    count=$(printf '%s\n' "$1" | grep -c "^FACT $key:" || true)
    [ "${count:-0}" -eq 1 ] || fail "$2: the $key row appears ${count:-0} time(s), want exactly one"
  done
  count=$(printf '%s\n' "$1" | grep -c '^CHECK 640 RESULT' || true)
  [ "${count:-0}" -eq 2 ] || fail "$2: the block has ${count:-0} CHECK 640 RESULT lines, want 2 (the head and the verdict)"
}
scratch_of() { # dir — the scratch directory the run announced
  sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$1/err" | head -n 1
}
expect_scratch_removed() { # dir what
  local scratch
  scratch=$(scratch_of "$1")
  [ -n "$scratch" ] || return 0
  [ ! -e "$scratch" ] || fail "$2: the scratch directory $scratch is still there"
}
# expect_nothing_read DIR WHAT — no fake was called at all: a guard that refused
# only after a read would leave a line in one of these logs, and a guard checked
# by a file nothing writes proves nothing.
expect_nothing_read() {
  local dir=$1 what=$2 log
  for log in fugaro gh gcloud git; do
    [ ! -s "$dir/$log.log" ] || fail "$what: the check called $log before it should have: $(tr '\n' ' ' <"$dir/$log.log")"
  done
}
# expect_no_cloud_reads DIR WHAT — the rows that read GitHub and Cloud Logging
# were never reached: the run stopped at a row above them. The doctor's own read
# of the installation (and the scratch guard's one git call) are expected before
# that point, so only these two logs have to be empty — a check that went on
# would have read a pull request and the project.
expect_no_cloud_reads() {
  local dir=$1 what=$2
  [ ! -s "$dir/gh.log" ] || fail "$what: a pull request was read before the row that needed it: $(tr '\n' ' ' <"$dir/gh.log")"
  [ ! -s "$dir/gcloud.log" ] || fail "$what: the project was read before the row that needed it: $(tr '\n' ' ' <"$dir/gcloud.log")"
}
# expect_only_reads DIR WHAT — every call the check made is a read. This is the
# check's whole promise: it writes nothing on the installation, on GitHub or in
# the project.
expect_only_reads() {
  local dir=$1 what=$2 bad
  bad=$(grep -v -e '^doctor ' -e '^orchestrator ls ' -e '^orchestrator state ' -e '^runs ls ' "$dir/fugaro.log" 2>/dev/null || true)
  [ -z "$bad" ] || fail "$what: fugaro was asked to do something else: $(printf '%s' "$bad" | tr '\n' ' ')"
  bad=$(grep -v -e '^pr view ' -e '^label list ' "$dir/gh.log" 2>/dev/null || true)
  [ -z "$bad" ] || fail "$what: gh was asked to do something else: $(printf '%s' "$bad" | tr '\n' ' ')"
  bad=$(grep -v -e '^run services get-iam-policy ' -e '^run services describe ' -e '^logging read ' "$dir/gcloud.log" 2>/dev/null || true)
  [ -z "$bad" ] || fail "$what: gcloud was asked to do something else: $(printf '%s' "$bad" | tr '\n' ' ')"
  # The ONE git call the check makes is the guard on its own scratch directory
  # (`git rev-parse --show-toplevel`, to refuse a scratch inside a work tree);
  # anything else means the check needed a checkout.
  bad=$(grep -v -x 'rev-parse --show-toplevel' "$dir/git.log" 2>/dev/null || true)
  [ -z "$bad" ] || fail "$what: the check ran git of its own: $(printf '%s' "$bad" | tr '\n' ' ')"
}
expect_called() { # dir tool what
  local dir=$1 tool=$2
  [ -s "$dir/$tool.log" ] || fail "$3: the check never called $tool"
}

# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(xtrace agent-session installation belong identity daemon item run
  running settled spec-label colours audit restore)

# The installation, the item and the run the fakes answer with. The slug is
# internal/task's own value for this repository (the readable canonical name and
# the first sixteen hex of sha256("github\0acme/app")): it is written here as a
# literal on purpose, so a check that computed it differently would find no audit
# line and fail the happy path rather than agreeing with itself.
REPO='acme/app'
SLUG='acme-app-dc4d4e59e884fc44'
OTHER_SLUG='someone-else-repo-0123456789abcdef'
ORCH='fugaro-orch@fugaro-dev.iam.gserviceaccount.com'
ITEM='ck640'
RUN='20261118-010101-abcd'
RUN2='20261118-020202-ef01'
SPEC='forge-labels'

# The knobs a case sets before it calls run640; every one of them is reset after
# the run, so a case that forgets to clear one cannot leak into the next.
RUN640_ARGS=()
RUN640_DOCTOR=""
RUN640_LS=""
RUN640_STATE=""
RUN640_RUNS=""
RUN640_RUNS_STATUS=""
RUN640_LIVE_TICKS=""
RUN640_PR=""
RUN640_PR_LIVE=""
RUN640_PR_SETTLED=""
RUN640_LABELS=""
RUN640_IAM=""
RUN640_SVC=""
RUN640_AUDIT=""
RUN640_AUDIT_LAG=""
RUN640_SPEC=""
RUN640_SPEC_SET=""
RUN640_ENV=()
RUN640_SHELL=""
RUN640_PATH=""
RUN640_RUN_SECONDS=""
RUN640_SETTLE_SECONDS=""
RUN640_AUDIT_WAIT_SECONDS=""

# --------------------------------------------------------------------- the fakes
write_fakes() { # dir
  local dir=$1
  cat >"$dir/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake `fugaro`, for check640.sh's test only: it answers the four reads the
# check makes and records what it was asked. It writes nothing anywhere.
set -u
printf '%s\n' "$*" >>"${FAKE640_FUGARO_LOG:-/dev/null}"

json_string() { # TEXT — the text as the body of a JSON string: quotes, backslashes and newlines escaped
  local text=$1
  text=${text//\\/\\\\}
  text=${text//\"/\\\"}
  text=${text//$'\n'/\\n}
  printf '%s' "$text"
}

case "${1:-}" in
doctor)
  case "${FAKE640_DOCTOR:-ok}" in
  fail)
    echo "doctor: no usable credentials on this machine" >&2
    exit 2
    ;;
  esac
  name=fugaro
  gcp=fugaro-dev
  case "${FAKE640_DOCTOR:-ok}" in
  wrong-name) name=othername ;;
  wrong-gcp) gcp=other-project ;;
  belongs) name=belong ;;
  esac
  printf '{"ok":true,"project":{"name":"%s","gcp_project":"%s","region":"us-east5"}}\n' "$name" "$gcp"
  ;;
orchestrator)
  case "${2:-}" in
  ls)
    case "${FAKE640_LS:-ok}" in
    fail)
      echo "orchestrator ls: the state bucket refused the call" >&2
      exit 3
      ;;
    none) printf '{"version":1,"kind":"ls","orchestrators":[]}\n' ;;
    central)
      printf '{"version":1,"kind":"ls","orchestrators":[{"kind":"central","project":"fugaro","lease_holder":"someone@elsewhere","cache_stale":false}]}\n'
      ;;
    stale)
      printf '{"version":1,"kind":"ls","orchestrators":[{"kind":"local","project":"fugaro","lease_holder":"me@this-host","cache_stale":true}]}\n'
      ;;
    no-lease)
      printf '{"version":1,"kind":"ls","orchestrators":[{"kind":"local","project":"fugaro","lease_holder":"","cache_stale":false}]}\n'
      ;;
    *)
      printf '{"version":1,"kind":"ls","orchestrators":[{"kind":"local","project":"fugaro","lease_holder":"me@this-host","cache_stale":false}]}\n'
      ;;
    esac
    ;;
  state)
    # The document carries the capped brief (which never lists the running item
    # in the `eight` case, as the old generator did not) and the `items` array
    # the check reads.
    state_brief="work items: 8, 1 still open
  old0 [done] Task: finished — ${FAKE640_REPO}, standard, strict
(and more: this summary is capped, so read the state itself for the rest of it)"
    spec_json=""
    [ -z "${FAKE640_SPEC:-}" ] || spec_json=",\"spec\":\"${FAKE640_SPEC}\""
    live_item="{\"id\":\"${FAKE640_ITEM}\",\"phase\":\"running\",\"run_id\":\"${FAKE640_RUN}\",\"attempts\":1,\"repo\":\"${FAKE640_REPO}\"${spec_json}}"
    case "${FAKE640_STATE:-ok}" in
    fail)
      echo "orchestrator state: the node refused the call" >&2
      exit 4
      ;;
    no-item)
      items="{\"id\":\"${FAKE640_ITEM}\",\"phase\":\"${FAKE640_ENDED_PHASE:-done}\",\"run_id\":\"${FAKE640_RUN}\",\"attempts\":1,\"repo\":\"${FAKE640_REPO}\"}"
      ;;
    empty)
      items=""
      ;;
    no-run)
      items="{\"id\":\"${FAKE640_ITEM}\",\"phase\":\"running\",\"attempts\":1,\"repo\":\"${FAKE640_REPO}\"}"
      ;;
    two-items)
      items="${live_item},{\"id\":\"other\",\"phase\":\"running\",\"run_id\":\"${FAKE640_RUN2}\",\"attempts\":1,\"repo\":\"${FAKE640_REPO}\"}"
      ;;
    eight)
      items=""
      for i in 0 1 2 3 4 5 6; do
        items="${items}{\"id\":\"old${i}\",\"phase\":\"done\",\"run_id\":\"20260101-000000-000${i}\",\"attempts\":1,\"repo\":\"${FAKE640_REPO}\"},"
      done
      items="${items}${live_item}"
      ;;
    *)
      items="${live_item}"
      ;;
    esac
    printf '{"version":1,"kind":"state","id":"fugaro","brief":"%s","items":[%s]}\n' "$(json_string "$state_brief")" "$items"
    ;;
  *) echo "fugaro: unknown orchestrator call" >&2; exit 2 ;;
  esac
  ;;
runs)
  count=$(cat "${FAKE640_COUNT:-/dev/null}" 2>/dev/null || echo 0)
  count=$((count + 1))
  printf '%s' "$count" >"${FAKE640_COUNT:-/dev/null}"
  case "${FAKE640_RUNS:-ok}" in
  fail)
    echo "runs ls: the bucket refused the call" >&2
    exit 5
    ;;
  missing) printf '{"version":1,"kind":"runs","runs":[]}\n' ;;
  no-pr)
    printf '{"version":1,"kind":"runs","runs":[{"run":"%s","run_id":"%s","repo":"%s","status":"%s","stage":"finalize","terminal":true,"settled":true,"branch":"fugaro/%s","outcome":"draft","pushed":true,"pr":0,"cost":1.25,"created":"2026-11-18T01:01:01Z"}]}\n' \
      "$FAKE640_RUN" "$FAKE640_RUN" "$FAKE640_REPO" "${FAKE640_RUNS_STATUS:-succeeded}" "$FAKE640_RUN"
    ;;
  *)
    terminal=true
    [ "$count" -le "${FAKE640_LIVE_TICKS:-0}" ] && terminal=false
    status=${FAKE640_RUNS_STATUS:-succeeded}
    outcome=ready
    case "$status" in
    failed) outcome=draft ;;
    halted) outcome=draft ;;
    cancelled) outcome=cancelled ;;
    esac
    printf '{"version":1,"kind":"runs","runs":[{"run":"%s","run_id":"%s","repo":"%s","status":"%s","stage":"finalize","terminal":%s,"settled":%s,"branch":"fugaro/%s","outcome":"%s","pushed":true,"pr":41,"pr_state":"open","pr_url":"https://github.com/%s/pull/41","cost":1.25,"created":"2026-11-18T01:01:01Z"}]}\n' \
      "$FAKE640_RUN" "$FAKE640_RUN" "$FAKE640_REPO" "$status" "$terminal" "$terminal" \
      "$FAKE640_RUN" "$outcome" "$FAKE640_REPO"
    ;;
  esac
  ;;
*)
  echo "fugaro: unknown call" >&2
  exit 2
  ;;
esac
FUGARO_EOF

  cat >"$dir/bin/gh" <<'GH_EOF'
#!/usr/bin/env bash
# A fake `gh`, for check640.sh's test only: the pull request's labels follow the
# run's own state (the counter the fake fugaro keeps), so the check reads
# fugaro:running exactly while the run is live — the daemon's own behaviour. It
# writes nothing to any repository.
set -u
printf '%s\n' "$*" >>"${FAKE640_GH_LOG:-/dev/null}"

labels_of() { # SET — the label array for one named set
  local set=$1 spec=${FAKE640_SPEC_LABEL:-spec:forge-labels} list="" name first=yes
  case "$set" in
  running+spec) list="fugaro:running $spec" ;;
  running) list="fugaro:running" ;;
  none+spec) list="$spec" ;;
  none) list="" ;;
  failed+spec) list="fugaro:failed $spec" ;;
  both+spec) list="fugaro:running fugaro:failed $spec" ;;
  none+otherspec) list="spec:something-else" ;;
  *) list="$spec" ;;
  esac
  printf '['
  for name in $list; do
    [ "$first" = yes ] || printf ','
    printf '{"name":"%s"}' "$name"
    first=no
  done
  printf ']'
}

case "${1:-}" in
pr)
  case "${FAKE640_PR:-ok}" in
  fail)
    echo "gh: not logged in to any host" >&2
    exit 1
    ;;
  esac
  count=$(cat "${FAKE640_COUNT:-/dev/null}" 2>/dev/null || echo 0)
  set=${FAKE640_PR_SETTLED:-none+spec}
  if [ "${count:-0}" -le "${FAKE640_LIVE_TICKS:-0}" ]; then set=${FAKE640_PR_LIVE:-running+spec}; fi
  printf '{"number":41,"state":"OPEN","url":"https://github.com/%s/pull/41","labels":' "$FAKE640_REPO"
  labels_of "$set"
  printf '}\n'
  ;;
label)
  case "${FAKE640_LABELS:-ok}" in
  fail)
    echo "gh: label list refused the call" >&2
    exit 1
    ;;
  ok)
    printf '[{"name":"fugaro:running","color":"1D76DB"},{"name":"fugaro:failed","color":"D73A4A"},{"name":"spec:forge-labels","color":"6E7781"},{"name":"enhancement","color":"a2eeef"}]\n'
    ;;
  wrong-colour)
    printf '[{"name":"fugaro:running","color":"0052CC"},{"name":"fugaro:failed","color":"D73A4A"},{"name":"spec:forge-labels","color":"6E7781"}]\n'
    ;;
  no-spec)
    printf '[{"name":"fugaro:running","color":"1D76DB"},{"name":"fugaro:failed","color":"D73A4A"}]\n'
    ;;
  no-running)
    printf '[{"name":"fugaro:failed","color":"D73A4A"},{"name":"spec:forge-labels","color":"6E7781"}]\n'
    ;;
  *)
    printf '[{"name":"fugaro:running","color":"1D76DB"}]\n'
    ;;
  esac
  ;;
*)
  echo "gh: unknown call" >&2
  exit 2
  ;;
esac
GH_EOF

  cat >"$dir/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake `gcloud`, for check640.sh's test only: the credentials service's own
# IAM policy, its revision and the label lines it wrote. It reads no credential
# and writes nothing anywhere.
set -u
printf '%s\n' "$*" >>"${FAKE640_GCLOUD_LOG:-/dev/null}"

audit_entry() { # message repository caller status
  printf '{"jsonPayload":{"message":"%s","repository":"%s","caller":"%s","credential":"cascade","purpose":"label","status":"%s"}}' \
    "$1" "$2" "$3" "$4"
}

audit_list() { # MODE
  case "$1" in
  ok)
    printf '[{"jsonPayload":{"message":"model call","repository":"%s","caller":"job@fugaro-dev.iam.gserviceaccount.com","purpose":"forward","status":"ok"}},' "$FAKE640_SLUG"
    audit_entry "label ensure" "$FAKE640_SLUG" "$FAKE640_ORCH" ok
    printf ','
    audit_entry "label add" "$FAKE640_SLUG" "$FAKE640_ORCH" ok
    printf ','
    audit_entry "label remove" "$FAKE640_SLUG" "$FAKE640_ORCH" ok
    printf ']\n'
    ;;
  refused)
    printf '['
    audit_entry "label add" "$FAKE640_SLUG" "$FAKE640_ORCH" state_unavailable
    printf ','
    audit_entry "label list" "$FAKE640_SLUG" "$FAKE640_ORCH" ok
    printf ']\n'
    ;;
  no-repo)
    printf '['
    audit_entry "label add" "$FAKE640_OTHER_SLUG" "$FAKE640_ORCH" ok
    printf ']\n'
    ;;
  other-caller)
    printf '['
    audit_entry "label add" "$FAKE640_SLUG" "another-job@fugaro-dev.iam.gserviceaccount.com" ok
    printf ']\n'
    ;;
  esac
}

case "${1:-}" in
run)
  case "${2:-}" in
  services)
    case "${3:-}" in
    get-iam-policy)
      case "${FAKE640_IAM:-ok}" in
      fail)
        echo "ERROR: (gcloud.run.services.get-iam-policy) PERMISSION_DENIED: the caller does not have permission" >&2
        exit 1
        ;;
      no-invoker)
        printf '{"bindings":[{"role":"roles/run.invoker","members":["serviceAccount:someone-else@fugaro-dev.iam.gserviceaccount.com"]}]}\n'
        ;;
      no-binding) printf '{"bindings":[]}\n' ;;
      *)
        printf '{"bindings":[{"role":"roles/run.invoker","members":["serviceAccount:%s"]},{"role":"roles/run.developer","members":["user:owner@example.com"]}]}\n' \
          "$FAKE640_ORCH"
        ;;
      esac
      ;;
    describe)
      case "${FAKE640_SVC:-ok}" in
      fail)
        echo "ERROR: (gcloud.run.services.describe) NOT_FOUND: the service does not exist in this region" >&2
        exit 1
        ;;
      no-env)
        printf '{"spec":{"template":{"containers":[{"env":[{"name":"FUGARO_BUILD_ACCOUNTS","value":"build@fugaro-dev.iam.gserviceaccount.com"}]}]}}}\n'
        ;;
      *)
        printf '{"spec":{"template":{"containers":[{"env":[{"name":"FUGARO_ORCHESTRATOR_ACCOUNTS","value":"%s"},{"name":"FUGARO_BUILD_ACCOUNTS","value":"build@fugaro-dev.iam.gserviceaccount.com"}]}]}}}\n' \
          "$FAKE640_ORCH"
        ;;
      esac
      ;;
    *) echo "gcloud: unknown run services call" >&2; exit 2 ;;
    esac
    ;;
  *) echo "gcloud: unknown run call" >&2; exit 2 ;;
  esac
  ;;
logging)
  case "${2:-}" in
  read)
    case "${FAKE640_AUDIT:-ok}" in
    fail)
      echo "ERROR: (gcloud.logging.read) PERMISSION_DENIED: the caller does not have permission" >&2
      exit 1
      ;;
    empty) printf '[]\n' ;;
    lag)
      n=$(cat "${FAKE640_AUDIT_COUNT:-/dev/null}" 2>/dev/null || echo 0)
      n=$((n + 1))
      printf '%s' "$n" >"${FAKE640_AUDIT_COUNT:-/dev/null}"
      if [ "$n" -le "${FAKE640_AUDIT_LAG:-0}" ]; then
        printf '[]\n'
      else
        audit_list ok
      fi
      ;;
    *) audit_list "${FAKE640_AUDIT:-ok}" ;;
    esac
    ;;
  *) echo "gcloud: unknown logging call" >&2; exit 2 ;;
  esac
  ;;
*)
  echo "gcloud: unknown call" >&2
  exit 2
  ;;
esac
GCLOUD_EOF

  cat >"$dir/bin/git" <<'GIT_EOF'
#!/usr/bin/env bash
# A fake `git` that fails every call: check640.sh runs no git of its own and
# needs no checkout, and this is where that would show.
set -u
printf '%s\n' "$*" >>"${FAKE640_GIT_LOG:-/dev/null}"
echo "git: this check runs no git of its own" >&2
exit 128
GIT_EOF
  chmod +x "$dir/bin/fugaro" "$dir/bin/gh" "$dir/bin/gcloud" "$dir/bin/git"
}

# run640 DIR — runs the check once against the fakes. Sets $status, $out and
# $err. RUN640_ARGS appends arguments, RUN640_SHELL replaces the interpreter the
# check is started with (the one place a tracing shell can be arranged),
# RUN640_PATH=none takes the fakes off PATH (the case that has to hold with no
# cloud tool at all), and the RUN640_* knobs pick what each fake answers.
run640() {
  local dir=$work/$1 path spec_env f n
  mkdir -p "$dir/bin" "$dir/tmp" "$dir/notools" "$dir/home"
  # The item's spec: the default, unless a case asked for one of its own — and a
  # case that asks for NONE sets RUN640_SPEC_SET, because an empty RUN640_SPEC
  # alone cannot be told from the variable never having been set.
  spec_env=$SPEC
  if [ -n "${RUN640_SPEC_SET:-}" ]; then
    spec_env=$RUN640_SPEC
  elif [ -n "${RUN640_SPEC:-}" ]; then
    spec_env=$RUN640_SPEC
  fi
  : >"$dir/count"
  : >"$dir/audit-count"
  : >"$dir/fugaro.log"
  : >"$dir/gh.log"
  : >"$dir/gcloud.log"
  : >"$dir/git.log"
  write_fakes "$dir"
  path="$dir/bin:$PATH"
  if [ "${RUN640_PATH:-}" = none ]; then
    # "No cloud tool on PATH" means exactly that: a PATH that holds every
    # system tool the check needs and NONE of gh, gcloud, fugaro or git. An
    # empty PATH would take `tr`, `grep` and `cat` away too, and the case would
    # then be measuring the shell rather than the check's own tool guard.
    for f in /usr/bin/* /bin/* /usr/local/bin/*; do
      n=${f##*/}
      case "$n" in
      gh | gcloud | fugaro | git) continue ;;
      esac
      [ -e "$dir/notools/$n" ] || [ -L "$dir/notools/$n" ] || ln -s "$f" "$dir/notools/$n"
    done
    path="$dir/notools"
  fi
  status=0
  out=""
  err=""
  # env -i: the case's environment is the whole environment, so no marker of the
  # suite that started this file can decide the check's own agent-session guard.
  env -i PATH="$path" TMPDIR="$dir/tmp" HOME="$dir/home" \
    ${RUN640_ENV[@]+"${RUN640_ENV[@]}"} \
    CHECK640_CALL_SECONDS=20 \
    CHECK640_RUN_POLL_SECONDS=1 CHECK640_RUN_SECONDS="${RUN640_RUN_SECONDS:-6}" \
    CHECK640_SETTLE_POLL_SECONDS=1 CHECK640_SETTLE_SECONDS="${RUN640_SETTLE_SECONDS:-3}" \
    CHECK640_AUDIT_POLL_SECONDS=1 CHECK640_AUDIT_WAIT_SECONDS="${RUN640_AUDIT_WAIT_SECONDS:-3}" \
    FAKE640_FUGARO_LOG="$dir/fugaro.log" FAKE640_GH_LOG="$dir/gh.log" \
    FAKE640_GCLOUD_LOG="$dir/gcloud.log" FAKE640_GIT_LOG="$dir/git.log" \
    FAKE640_COUNT="$dir/count" FAKE640_AUDIT_COUNT="$dir/audit-count" \
    FAKE640_REPO="$REPO" FAKE640_ITEM="$ITEM" FAKE640_RUN="$RUN" FAKE640_RUN2="$RUN2" \
    FAKE640_SPEC="$spec_env" FAKE640_SLUG="$SLUG" FAKE640_OTHER_SLUG="$OTHER_SLUG" \
    FAKE640_ORCH="$ORCH" \
    FAKE640_DOCTOR="${RUN640_DOCTOR:-ok}" FAKE640_LS="${RUN640_LS:-ok}" \
    FAKE640_STATE="${RUN640_STATE:-ok}" FAKE640_RUNS="${RUN640_RUNS:-ok}" \
    FAKE640_RUNS_STATUS="${RUN640_RUNS_STATUS:-succeeded}" \
    FAKE640_LIVE_TICKS="${RUN640_LIVE_TICKS:-2}" \
    FAKE640_PR="${RUN640_PR:-ok}" FAKE640_PR_LIVE="${RUN640_PR_LIVE:-running+spec}" \
    FAKE640_PR_SETTLED="${RUN640_PR_SETTLED:-none+spec}" \
    FAKE640_LABELS="${RUN640_LABELS:-ok}" \
    FAKE640_IAM="${RUN640_IAM:-ok}" FAKE640_SVC="${RUN640_SVC:-ok}" \
    FAKE640_AUDIT="${RUN640_AUDIT:-ok}" FAKE640_AUDIT_LAG="${RUN640_AUDIT_LAG:-0}" \
    ${RUN640_SHELL:-$bash_bin} "$script" ${RUN640_ARGS[@]+"${RUN640_ARGS[@]}"} \
    >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  # A row's own text is TEXT: the check quotes and never evaluates it, so a
  # backtick (or a $( in a sentence) that the shell ran would show up here as a
  # command the check never meant to run. The check's own rows are the only
  # thing that writes to stderr, so this catches the whole class.
  case "$err" in
  *"command not found"*) fail "the check ran a command out of its own text: $(printf '%s' "$err" | grep -m1 'command not found')" ;;
  esac
  RUN640_ARGS=()
  RUN640_DOCTOR=""
  RUN640_LS=""
  RUN640_STATE=""
  RUN640_RUNS=""
  RUN640_RUNS_STATUS=""
  RUN640_LIVE_TICKS=""
  RUN640_PR=""
  RUN640_PR_LIVE=""
  RUN640_PR_SETTLED=""
  RUN640_LABELS=""
  RUN640_IAM=""
  RUN640_SVC=""
  RUN640_AUDIT=""
  RUN640_AUDIT_LAG=""
  RUN640_SPEC=""
  RUN640_SPEC_SET=""
  RUN640_ENV=()
  RUN640_SHELL=""
  RUN640_PATH=""
  RUN640_RUN_SECONDS=""
  RUN640_SETTLE_SECONDS=""
  RUN640_AUDIT_WAIT_SECONDS=""
}

# ------------------------------------------------------------------ the tests
# The all-PASS run: the daemon is ticking here, one approved task is in flight
# with a spec, the pull request carries fugaro:running while it is live and the
# spec label after, the repository holds D2437's colours and the credentials
# service's own line names the daemon as the caller of an act that succeeded.
test_happy_path() {
  run640 happy
  expect_status "$status" 0 "the happy path"
  expect_last_line "$out" "CHECK 640 RESULT: PASS (exit 0)" "the happy path"
  expect_block_keys "$out" "the happy path"
  expect_contains "$out" "FACT installation: PASS" "the installation is read"
  expect_contains "$out" "FACT belong: PASS" "belong is refused by name"
  expect_contains "$out" "FACT identity: PASS" "F19's two halves are read"
  expect_contains "$out" "${ORCH} is a member of a roles/run.invoker binding" "the identity row says which grant it read"
  expect_contains "$out" "FACT daemon: PASS" "the daemon is local and beating"
  expect_contains "$out" "FACT item: PASS" "the item in flight is read from the daemon's own state"
  expect_contains "$out" "item ${ITEM} in phase running on ${REPO}, its attempt's run is ${RUN}, its spec is ${SPEC}" "the item row names the item, its run and its spec"
  expect_contains "$out" "FACT run: PASS" "the run's own row is read"
  expect_contains "$out" "left pull request #41" "the run row names the pull request"
  expect_contains "$out" "FACT running: PASS" "the running label was seen while the run was live"
  expect_contains "$out" "U1 is answered YES for this act" "the running row says which question it settles"
  expect_contains "$out" "FACT settled: PASS" "the settled labels match the run's own ending"
  expect_contains "$out" "carries NO status label once the run has ended" "the settled row says what a succeeded run wants"
  expect_contains "$out" "FACT spec-label: PASS" "the spec label is on the pull request"
  expect_contains "$out" "carries spec:${SPEC}" "the spec row names the label"
  expect_contains "$out" "FACT colours: PASS" "the repository holds the table's colours"
  expect_contains "$out" "NAME CARRIES A COLON" "the colours row says U3 is settled"
  expect_contains "$out" "FACT audit: PASS" "the service's own line names the daemon"
  expect_contains "$out" "statuses ok:3" "the audit row says every act was ok"
  expect_contains "$out" "FACT restore: PASS" "the scratch directory is removed"
  expect_scratch_removed "$work/happy" "the happy path"
  expect_only_reads "$work/happy" "the happy path"
}

test_the_check_reads_and_writes_nothing() {
  run640 reads
  expect_status "$status" 0 "reads only"
  expect_only_reads "$work/reads" "reads only"
  expect_called "$work/reads" gh "reads only"
  expect_called "$work/reads" gcloud "reads only"
  # The three reads the check needs of the service and the repository, spelled
  # out: nothing else is asked of any tool.
  expect_contains "$(cat "$work/reads/gcloud.log")" "run services get-iam-policy fugaro-credentials" "the service's policy is read"
  expect_contains "$(cat "$work/reads/gcloud.log")" "logging read" "the service's own lines are read"
  expect_contains "$(cat "$work/reads/gh.log")" "pr view 41 --repo ${REPO} --json labels,state,number,url" "the pull request's labels are read"
  expect_contains "$(cat "$work/reads/gh.log")" "label list --repo ${REPO} --json name,color" "the repository's label table is read"
  expect_absent "$(cat "$work/reads/gh.log")" "label create" "the check creates no label"
  expect_absent "$(cat "$work/reads/gh.log")" "label edit" "the check repaints no label"
  expect_absent "$(cat "$work/reads/gh.log")" "pr edit" "the check edits no pull request"
}

test_the_check_runs_no_git_of_its_own() {
  run640 no-git
  expect_status "$status" 0 "no git"
  local calls
  calls=$(cat "$work/no-git/git.log" 2>/dev/null || true)
  [ "$calls" = "rev-parse --show-toplevel" ] || fail "no git: git was called with '$calls', want only 'rev-parse --show-toplevel' (the guard on the check's own scratch directory)"
}

test_the_audit_lines_are_waited_for() {
  RUN640_AUDIT=lag RUN640_AUDIT_LAG=2 RUN640_AUDIT_WAIT_SECONDS=5
  run640 lag
  expect_status "$status" 0 "a service whose lines arrive late"
  expect_contains "$out" "FACT audit: PASS" "the lines are waited for rather than read once"
  expect_contains "$out" "statuses ok:3" "the lines read are the ones that arrived"
  local reads
  reads=$(grep -c '^logging read ' "$work/lag/gcloud.log" || true)
  [ "${reads:-0}" -ge 3 ] || fail "a late line: logging was read ${reads:-0} time(s), want at least 3 (the two empty answers and the one that arrived)"
}

test_a_running_label_that_never_appears_is_a_failure() {
  RUN640_PR_LIVE=none
  run640 no-running-label
  expect_status "$status" 1 "a pull request that never carries the running label"
  expect_last_line "$out" "CHECK 640 RESULT: FAIL (exit 1)" "a pull request that never carries the running label"
  expect_contains "$out" "FACT running: FAIL" "the running label is the row that fails"
  expect_contains "$out" "never carried fugaro:running while the run was in flight" "the row says what it watched"
  expect_contains "$out" "FACT settled: PASS" "the settled row still reads what it can"
}

test_a_check_that_starts_after_the_run_ended_is_incomplete() {
  RUN640_LIVE_TICKS=0
  run640 late-start
  expect_status "$status" 3 "a check that started after the run ended"
  expect_last_line "$out" "CHECK 640 RESULT: INCOMPLETE (exit 3)" "a check that started after the run ended"
  expect_contains "$out" "FACT running: UNVERIFIED" "the running label is not settled after the fact"
  expect_contains "$out" "had ALREADY ended by the time this check first read its pull request" "the row says why it is not a reading"
  expect_contains "$out" "FACT settled: PASS" "the settled labels are still read"
  expect_contains "$out" "FACT spec-label: PASS" "the spec label is still read"
}

test_a_status_label_left_behind_is_a_failure() {
  RUN640_PR_SETTLED=running+spec RUN640_SETTLE_SECONDS=2
  run640 left-behind
  expect_status "$status" 1 "a running label left on a settled run"
  expect_contains "$out" "FACT settled: FAIL" "the settled row fails"
  expect_contains "$out" "still carries fugaro:running" "the row says what is still there"
  expect_contains "$out" "FACT running: PASS" "the running label was seen while it was live"
}

test_a_failed_run_wants_the_failed_label() {
  RUN640_RUNS_STATUS=failed RUN640_PR_SETTLED=failed+spec
  run640 failed-run
  expect_status "$status" 0 "a failed run carrying the failed label"
  expect_last_line "$out" "CHECK 640 RESULT: PASS (exit 0)" "a failed run carrying the failed label"
  expect_contains "$out" "FACT settled: PASS" "the failed label is what the ending wants"
  expect_contains "$out" "carries fugaro:failed once the run has ended" "the row says which label it read"
  expect_contains "$out" "folds to the item's phase failed" "the row says where the expectation comes from"
}

test_a_halted_run_reads_as_a_failure() {
  RUN640_RUNS_STATUS=halted RUN640_PR_SETTLED=failed+spec
  run640 halted-run
  expect_status "$status" 0 "a halted run carrying the failed label"
  expect_contains "$out" "FACT settled: PASS" "an ending this release cannot place wants the failed label"
}

test_a_cancelled_run_wants_no_status_label() {
  RUN640_RUNS_STATUS=cancelled RUN640_PR_SETTLED=none+spec
  run640 cancelled-run
  expect_status "$status" 0 "a cancelled run"
  expect_contains "$out" "FACT settled: PASS" "a cancelled run leaves no status label"
  expect_contains "$out" "FACT running: PASS" "the running label was seen while it was live"
}

test_a_failed_label_missing_is_a_failure() {
  RUN640_RUNS_STATUS=failed RUN640_PR_SETTLED=none+spec
  run640 failed-label-missing
  expect_status "$status" 1 "a failed run with no failed label"
  expect_contains "$out" "FACT settled: FAIL" "the missing failed label is the failure"
  expect_contains "$out" "carries NO status label and this run's ending wants fugaro:failed" "the row says what is missing"
}

test_a_failed_label_on_a_successful_run_is_a_failure() {
  RUN640_PR_SETTLED=failed+spec
  run640 failed-on-success
  expect_status "$status" 1 "a failure label on a run that succeeded"
  expect_contains "$out" "FACT settled: FAIL" "the wrong label is the failure"
  expect_contains "$out" "carries fugaro:failed and the run ended succeeded" "the row says what it read and what the run ended"
}

test_an_item_without_a_spec_is_incomplete() {
  RUN640_SPEC_SET=empty RUN640_SPEC=""
  run640 no-spec-item
  expect_status "$status" 3 "an item that carries no spec"
  expect_contains "$out" "FACT spec-label: UNVERIFIED" "a spec label nobody asked for is not measured"
  expect_contains "$out" "carries NO spec" "the row says why"
  expect_contains "$out" "FACT colours: PASS" "the colours are still read"
  expect_contains "$out" "The item carries no spec, so no spec: label was read here" "the colours row says the colon label was not required"
}

test_a_pull_request_missing_the_spec_label_is_a_failure() {
  RUN640_PR_SETTLED=none
  run640 no-spec-label
  expect_status "$status" 1 "a pull request without the spec label"
  expect_contains "$out" "FACT spec-label: FAIL" "the missing spec label is the failure"
  expect_contains "$out" "does NOT carry spec:${SPEC}" "the row names the label it wanted"
  expect_contains "$out" "FACT colours: PASS" "the repository's own table still holds the label"
}

test_a_spec_label_of_another_plan_is_a_failure() {
  RUN640_PR_SETTLED=none+otherspec
  run640 other-spec-label
  expect_status "$status" 1 "a spec label of another plan"
  expect_contains "$out" "FACT spec-label: FAIL" "the wrong spec label is the failure"
  expect_contains "$out" "does NOT carry spec:${SPEC}" "the row names the label it wanted"
}

test_the_spec_label_is_folded() {
  RUN640_SPEC='Forge-Labels'
  run640 folded-spec
  expect_status "$status" 0 "a spec whose name is not lower case"
  expect_contains "$out" "its spec is Forge-Labels" "the item's own spelling is what the row reads"
  expect_contains "$out" "carries spec:forge-labels" "the label is the folded name (R3)"
  expect_contains "$out" "the name folded to lower case and prefixed" "the row says the rule it applied"
}

test_a_colour_that_is_not_the_tables_is_a_failure() {
  RUN640_LABELS=wrong-colour
  run640 wrong-colour
  expect_status "$status" 1 "a label whose colour is not D2437's"
  expect_contains "$out" "FACT colours: FAIL" "the colour is the failure"
  expect_contains "$out" "'fugaro:running' is 0052cc where D2437's table says 1d76db" "the row names the label, its colour and the table's"
  expect_contains "$out" "never repaints one (D2440)" "the row says what a pre-existing label means"
}

test_a_repository_without_the_colon_label_is_a_failure() {
  RUN640_LABELS=no-spec
  run640 no-colon-label
  expect_status "$status" 1 "a repository without the colon label"
  expect_contains "$out" "FACT colours: FAIL" "the missing colon label is the failure"
  expect_contains "$out" "holds NO label 'spec:${SPEC}' at all" "the row names the label that is missing"
  expect_contains "$out" "gh label delete" "the row says how to have it created again"
}

test_the_repository_must_hold_the_running_label() {
  RUN640_LABELS=no-running
  run640 no-running-in-table
  expect_status "$status" 1 "a repository without fugaro:running"
  expect_contains "$out" "FACT colours: FAIL" "the missing running label is the failure"
  expect_contains "$out" "holds NO label 'fugaro:running' at all" "the row names it"
}

test_a_label_list_that_cannot_be_read_is_incomplete() {
  RUN640_LABELS=fail
  run640 labels-fail
  expect_status "$status" 3 "a repository whose label list could not be read"
  expect_contains "$out" "FACT colours: UNVERIFIED" "an unread table is not a failure"
  expect_contains "$out" "did not answer" "the row says what did not answer"
  expect_contains "$out" "gh label list --repo ${REPO} --json name,color --limit" "the row hands the owner the command"
}

test_the_audit_line_names_the_daemon() {
  run640 audit-names
  expect_status "$status" 0 "the service's own line names the daemon"
  expect_contains "$out" "name ${ORCH} as the caller of the label acts for ${SLUG}, the slug of ${REPO}" "the audit row names the caller and the repository"
  expect_contains "$out" "the slug of ${REPO}" "the row says which repository the slug is"
}

test_a_label_act_by_another_caller_is_a_failure() {
  RUN640_AUDIT=other-caller
  run640 other-caller
  expect_status "$status" 1 "a label act performed for another caller"
  expect_contains "$out" "FACT audit: FAIL" "another caller is the failure"
  expect_contains "$out" "name the caller(s) 'another-job@fugaro-dev.iam.gserviceaccount.com' and NOT ${ORCH}" "the row names both"
}

test_a_refused_act_is_carried_in_the_row() {
  RUN640_AUDIT=refused
  run640 refused
  expect_status "$status" 1 "a refused label act"
  expect_contains "$out" "FACT audit: FAIL" "a refused act is the failure"
  expect_contains "$out" "carry a REFUSED act: statuses ok:1,state_unavailable:1" "the row says which statuses it read"
  expect_contains "$out" "raise pull_requests: write on the App's settings page" "the row says what U1 would mean"
}

test_no_audit_line_for_this_repository_is_incomplete() {
  RUN640_AUDIT=no-repo
  run640 other-repo-lines
  expect_status "$status" 3 "lines that name another repository"
  expect_contains "$out" "FACT audit: UNVERIFIED" "another repository's lines are not a failure"
  expect_contains "$out" "name NO act for ${SLUG}, the slug of ${REPO}" "the row names the repository it looked for"
  expect_contains "$out" "${OTHER_SLUG}" "the row says which repositories the lines do name"
}

test_no_audit_line_at_all_is_incomplete() {
  RUN640_AUDIT=empty
  run640 no-lines
  expect_status "$status" 3 "a service that wrote no label line at all"
  expect_contains "$out" "FACT audit: UNVERIFIED" "no line at all is not a failure"
  expect_contains "$out" "wrote NO label line at all" "the row says what it read"
  expect_contains "$out" "builds none, R7" "the row names the seams a daemon without a URL has"
}

test_gcloud_refusing_the_audit_read_is_incomplete() {
  RUN640_AUDIT=fail
  run640 audit-refused
  expect_status "$status" 3 "a gcloud that refuses the audit read"
  expect_contains "$out" "FACT audit: UNVERIFIED" "a refused read is not a failure"
  expect_contains "$out" "PERMISSION_DENIED" "the row carries gcloud's own words"
  expect_contains "$out" "roles/logging.viewer" "the row says what the reading needs"
}

test_a_pull_request_gh_cannot_read_is_incomplete() {
  RUN640_PR=fail
  run640 pr-refused
  expect_status "$status" 3 "a gh that cannot read the pull request"
  expect_contains "$out" "FACT running: UNVERIFIED" "no reading of the labels is not a failure"
  expect_contains "$out" "did not answer once while the run was in flight" "the row says what did not answer"
  expect_contains "$out" "FACT settled: UNVERIFIED" "the settled labels are not measured either"
  expect_contains "$out" "FACT spec-label: UNVERIFIED" "nor is the spec label"
}

test_a_run_that_never_finishes_is_read_as_such() {
  RUN640_LIVE_TICKS=1000 RUN640_RUN_SECONDS=3 RUN640_SETTLE_SECONDS=2
  run640 never-finishes
  expect_status "$status" 3 "a run still in flight when the wait is over"
  expect_contains "$out" "FACT run: PASS" "the run's own row was read"
  expect_contains "$out" "the wait for its row timed out after 3s" "the run row says the wait timed out"
  # The running label WAS read: the pull request carried fugaro:running for every
  # tick the check watched, so that row is settled even though the run never
  # ended. What is not settled is where the labels come to rest.
  expect_contains "$out" "FACT running: PASS" "the running label was seen on a live run"
  expect_contains "$out" "FACT settled: UNVERIFIED" "the settled reading was not taken"
  expect_contains "$out" "never reached a terminal row within 3s" "the row says what it waited for"
  expect_contains "$out" "FACT spec-label:" "the spec label is still read from the pull request"
}

# The F19 premise: with the daemon's identity missing, the check stops at ONE
# named row and reads nothing below it — a check that went on would fail every
# row for a reason that is not about labels.
test_f19_stops_the_run_at_one_named_row() {
  RUN640_IAM=no-invoker
  run640 f19-no-invoker
  expect_status "$status" 2 "an installation whose orchestrator account has no invoker grant"
  expect_last_line "$out" "CHECK 640 RESULT: INCOMPLETE (exit 2)" "an installation whose orchestrator account has no invoker grant"
  expect_contains "$out" "FACT identity: FAIL" "the identity row is the failure"
  expect_contains "$out" "the daemon cannot reach the label route: F19" "the row is the one the plan names"
  expect_contains "$out" "${ORCH} is NOT a member of any roles/run.invoker binding" "the row says which half is missing"
  expect_contains "$out" "FACT daemon: FAIL — not reached" "nothing below the premise was read"
  expect_contains "$out" "FACT colours: FAIL — not reached" "nothing below the premise was read"
  expect_absent "$out" "FACT daemon: PASS" "no row below the premise passes"
  # Nothing below it was READ either: no daemon listing, no run, no pull request.
  expect_absent "$(cat "$work/f19-no-invoker/fugaro.log")" "orchestrator ls" "no daemon was read"
  [ ! -s "$work/f19-no-invoker/gh.log" ] || fail "F19: a pull request was read after the premise failed: $(tr '\n' ' ' <"$work/f19-no-invoker/gh.log")"
  expect_only_reads "$work/f19-no-invoker" "F19"

  # The other half: the grant is there and the service's own revision does not
  # name the account, so the route refuses every caller before any list is
  # consulted (F26).
  RUN640_SVC=no-env
  run640 f19-no-class
  expect_status "$status" 2 "a service that names no orchestrator class"
  expect_contains "$out" "the daemon cannot reach the label route: F19" "the second half is the same named row"
  expect_contains "$out" "names ${ORCH} in NO environment variable" "the row says which half is missing this time"
  [ ! -s "$work/f19-no-class/gh.log" ] || fail "F19: a pull request was read after the premise failed"
}

test_the_daemon_must_be_running_here() {
  RUN640_LS=none
  run640 no-daemon
  expect_status "$status" 2 "an installation with no orchestrator recorded"
  expect_contains "$out" "FACT daemon: FAIL" "the daemon row is the failure"
  expect_contains "$out" "has no orchestrator at all" "the row says what it read"
  expect_contains "$out" "FACT item: FAIL — not reached" "nothing below was measured"
  [ ! -s "$work/no-daemon/gh.log" ] || fail "no daemon: a pull request was read anyway"

  RUN640_LS=central
  run640 central-daemon
  expect_status "$status" 2 "an orchestrator this machine does not run"
  expect_contains "$out" "FACT daemon: FAIL" "a central orchestrator is not this machine's daemon"
  expect_contains "$out" "kind=central" "the row says what kind it read"
  expect_contains "$out" "fugaro orchestrator run --project fugaro" "the row hands the owner the command"

  RUN640_LS=stale
  run640 stale-daemon
  expect_status "$status" 2 "a daemon whose cached brief is stale"
  expect_contains "$out" "cache_stale=yes" "the row says the brief is stale"

  RUN640_LS=no-lease
  run640 no-lease
  expect_status "$status" 2 "a daemon that holds no lease"
  expect_contains "$out" "names NO holder" "the row says the lease is empty"

  RUN640_LS=fail
  run640 ls-fail
  expect_status "$status" 2 "a listing that could not be read"
  expect_contains "$out" "FACT daemon: FAIL" "an unread listing is a failure of the row"
  expect_contains "$out" "exited 3" "the row carries the exit status"
}

test_no_item_in_flight_is_a_failure() {
  RUN640_STATE=no-item
  run640 no-item
  expect_status "$status" 2 "a daemon with nothing in flight"
  expect_contains "$out" "FACT item: FAIL" "the item row is the failure"
  expect_contains "$out" "the item has already ended" "an ended item is named as ended"
  expect_contains "$out" "is phase done (run ${RUN})" "the row says the item's phase and run"
  expect_contains "$out" "Only the settled rows are measurable" "the row says which rows can still be read"
  expect_contains "$out" "--suffix <word>" "the row says how to get a fresh item"
  [ ! -s "$work/no-item/gh.log" ] || fail "no item: a pull request was read anyway"

  RUN640_STATE=empty
  run640 empty-state
  expect_status "$status" 2 "a daemon with no item at all"
  expect_contains "$out" "NO work item in phase running with a run id" "the row says what it looked for"
  expect_contains "$out" "Approve one task in the daemon's own backlog" "the row says what to do"

  RUN640_STATE=no-run
  run640 item-without-run
  expect_status "$status" 2 "a running item with no run id"
  expect_contains "$out" "NO work item in phase running with a run id" "an item with no run is not a subject"

  RUN640_STATE=fail
  run640 state-fail
  expect_status "$status" 2 "a state that could not be read"
  expect_contains "$out" "exited 4" "the row carries the exit status"
}

test_the_running_item_is_found_among_eight() {
  RUN640_STATE=eight
  run640 eight-items
  expect_status "$status" 0 "eight items with the running one last"
  expect_contains "$out" "FACT item: PASS" "the running item is found from the items array"
  expect_contains "$out" "its attempt's run is ${RUN}" "the row names the run"
}

test_two_items_in_flight_ask_for_run_id() {
  RUN640_STATE=two-items
  run640 two-items
  expect_status "$status" 2 "two items in flight"
  expect_contains "$out" "2 work items of the daemon" "the row says how many"
  expect_contains "$out" "Name the one to read with --run-id <id>" "the row says how to pick one"
  expect_contains "$out" "${RUN}" "the row lists the ids"
  expect_contains "$out" "${RUN2}" "the row lists the ids"
  [ ! -s "$work/two-items/gh.log" ] || fail "two items: a pull request was read anyway"

  # With the id named, the same state reads the one item.
  RUN640_STATE=two-items RUN640_ARGS=(--run-id "$RUN")
  run640 two-items-picked
  expect_status "$status" 0 "two items in flight with --run-id"
  expect_contains "$out" "FACT item: PASS" "the named item is the subject"
  expect_contains "$out" "its attempt's run is ${RUN}" "the row names the run that was asked for"
}

test_a_run_id_that_is_not_the_items_is_a_failure() {
  RUN640_ARGS=(--run-id "$RUN2")
  run640 wrong-run-id
  expect_status "$status" 2 "a run id no item of this daemon launched"
  expect_contains "$out" "FACT item: FAIL" "the item row is the failure"
  expect_contains "$out" "the run id ${RUN2}" "the row names the id it was given"
}

test_the_runs_row_must_be_in_the_listing() {
  RUN640_RUNS=missing
  run640 run-missing
  expect_status "$status" 2 "a run the listing does not hold"
  expect_contains "$out" "FACT run: FAIL" "the run row is the failure"
  expect_contains "$out" "is in NO row of fugaro runs ls" "the row says what it read"
  expect_contains "$out" "the daemon's record of what it launched (O3)" "the row says where the id came from"

  RUN640_RUNS=fail
  run640 runs-fail
  expect_status "$status" 2 "a listing that could not be read"
  expect_contains "$out" "did not answer while waiting for the run" "the row says what did not answer"
}

test_a_run_without_a_pull_request_is_a_failure() {
  RUN640_RUNS=no-pr
  run640 no-pr
  expect_status "$status" 2 "a run that opened no pull request"
  expect_contains "$out" "FACT run: FAIL" "the run row is the failure"
  expect_contains "$out" "left NO pull request (pr=0)" "the row says what it read"
  [ ! -s "$work/no-pr/gh.log" ] || fail "no pull request: gh was called anyway"
}

test_belong_is_refused() {
  RUN640_ARGS=(--installation belong)
  run640 belong-install
  expect_status "$status" 2 "belong as the installation"
  expect_contains "$out" "names 'belong', the installation this check must never touch" "the refusal says why"
  expect_nothing_read "$work/belong-install" "belong as the installation"

  RUN640_ARGS=(--gcp-project belong-dev)
  run640 belong-project
  expect_status "$status" 2 "a GCP project that names belong"
  expect_contains "$out" "names 'belong'" "any value that names belong is refused"
  expect_nothing_read "$work/belong-project" "a GCP project that names belong"

  # And the value the check READS is refused too, with its own row.
  RUN640_DOCTOR=belongs
  run640 belong-read
  expect_status "$status" 2 "a local config whose installation names belong"
  expect_contains "$out" "FACT belong: FAIL" "the read value has a row of its own"
  expect_contains "$out" "names 'belong' (installation 'belong'" "the row says what it read"
  expect_no_cloud_reads "$work/belong-read" "a local config whose installation names belong"
}

test_the_installation_and_the_gcp_project_are_held() {
  RUN640_DOCTOR=wrong-name
  run640 wrong-name
  expect_status "$status" 2 "another installation under --project fugaro"
  expect_contains "$out" "is the installation 'othername'" "the row says which installation it read"
  expect_contains "$out" "FACT belong: FAIL — not reached" "nothing was measured"

  RUN640_DOCTOR=wrong-gcp
  run640 wrong-gcp
  expect_status "$status" 2 "an installation in another GCP project"
  expect_contains "$out" "lives in GCP project 'other-project'" "the row says which project it read"
  expect_contains "$out" "reads the dogfood installation in 'fugaro-dev' only" "the row says which project it reads"

  RUN640_DOCTOR=fail
  run640 doctor-fail
  expect_status "$status" 2 "a doctor that cannot read the installation"
  expect_contains "$out" "fugaro doctor --project fugaro failed (exit 2)" "the row carries the exit status"
  expect_no_cloud_reads "$work/doctor-fail" "a doctor that failed"
}

test_the_service_must_be_readable() {
  RUN640_IAM=fail
  run640 iam-fail
  expect_status "$status" 2 "a service whose policy cannot be read"
  expect_contains "$out" "FACT identity: UNVERIFIED" "an unread policy is not a failure"
  expect_contains "$out" "PERMISSION_DENIED" "the row carries gcloud's own words"
  expect_contains "$out" "roles/run.viewer" "the row says what the reading needs"
  [ ! -s "$work/iam-fail/gh.log" ] || fail "an unread policy: a pull request was read anyway"

  RUN640_SVC=fail
  run640 svc-fail
  expect_status "$status" 2 "a service that cannot be described"
  expect_contains "$out" "FACT identity: UNVERIFIED" "an undescribed service is not a failure"
  expect_contains "$out" "NOT_FOUND" "the row carries gcloud's own words"
}

test_dry_run_touches_nothing() {
  RUN640_ARGS=(--dry-run)
  run640 dry
  expect_status "$status" 0 "--dry-run"
  expect_contains "$out" "This is the PLAN" "the plan is printed"
  expect_contains "$out" "installation   fugaro" "the plan names the installation"
  expect_contains "$out" "the daemon cannot reach the label route" "the plan says what F19 would mean"
  expect_absent "$out" "CHECK 640 RESULT" "no block is printed for a plan"
  expect_nothing_read "$work/dry" "--dry-run"

  # The plan is printed for the installation it was given, and belong is still
  # refused (the guard runs before the plan is printed).
  RUN640_ARGS=(--dry-run --installation othername)
  run640 dry-other
  expect_status "$status" 0 "--dry-run of another installation"
  expect_contains "$out" "installation   othername" "the plan names the installation it was given"
  expect_nothing_read "$work/dry-other" "--dry-run of another installation"
}

test_the_plan_and_the_usage_need_no_cloud_tools() {
  RUN640_ARGS=(--dry-run) RUN640_PATH=none
  run640 dry-nopath
  expect_status "$status" 0 "--dry-run with no cloud tool on PATH"
  expect_contains "$out" "This is the PLAN" "the plan needs no tool"
  expect_nothing_read "$work/dry-nopath" "--dry-run with no cloud tool on PATH"

  RUN640_ARGS=(--help) RUN640_PATH=none
  run640 help-nopath
  expect_status "$status" 0 "--help with no cloud tool on PATH"
  expect_contains "$out" "usage: check640.sh" "the usage is printed"
  expect_contains "$out" "--run-id ID" "the usage names the knobs"

  # A real run with no cloud tool on PATH refuses, naming the tool: it never
  # reaches a call.
  RUN640_PATH=none
  run640 run-nopath
  expect_status "$status" 2 "a real run with no cloud tool on PATH"
  expect_contains "$out" "Check 640 needs" "the refusal names a missing tool"
  expect_nothing_read "$work/run-nopath" "a real run with no cloud tool on PATH"
}

test_argument_complaints() {
  RUN640_ARGS=(--nope)
  run640 bad-flag
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$out" "unknown argument '--nope'" "the refusal names it"
  expect_contains "$out" "usage: check640.sh" "the refusal carries the usage"
  expect_nothing_read "$work/bad-flag" "an unknown argument"

  RUN640_ARGS=(--installation)
  run640 missing-value
  expect_status "$status" 2 "an argument with no value"
  expect_contains "$out" "--installation needs an installation name" "the refusal says what is missing"

  RUN640_ARGS=(--run-id 'bad id!')
  run640 bad-run-id
  expect_status "$status" 2 "a run id with a character it may not have"
  expect_contains "$out" "holds a character a run id may not have" "the refusal says why"
  expect_nothing_read "$work/bad-run-id" "a run id with a character it may not have"
}

test_xtrace_is_refused() {
  RUN640_SHELL='bash -x'
  run640 xtrace
  expect_status "$status" 2 "a shell that arrived with tracing on"
  expect_contains "$out" "FACT xtrace: FAIL" "the trace is the failure"
  expect_contains "$out" "started with tracing on" "the row says what it read"
  expect_contains "$out" "Run it without -x" "the row says what to do"
  expect_contains "$out" "FACT installation: FAIL — not reached" "nothing was measured"
  expect_no_cloud_reads "$work/xtrace" "a shell that arrived with tracing on"
}

# The check is the owner's: a coding agent's session is refused before anything
# is read, and the refusal says which marker said so.
test_a_coding_agents_session_is_refused() {
  RUN640_ENV=(CLAUDECODE=1)
  run640 agent-session
  expect_status "$status" 2 "a coding agent's session"
  expect_contains "$out" "FACT agent-session: FAIL" "the marker is the failure"
  expect_contains "$out" "CLAUDECODE is set" "the row names the marker it read"
  expect_contains "$out" "no agent runs any step of it" "the row says whose check this is"
  expect_contains "$out" "FACT installation: FAIL — not reached" "nothing was measured"
  expect_no_cloud_reads "$work/agent-session" "a coding agent's session"

  # The editor extension's marker is NOT one of those: it says this shell sits
  # in an editor's terminal, and the person is still the one typing. It is
  # dropped rather than refused, so the check runs for someone in their editor.
  RUN640_ENV=(CLAUDE_CODE_SSE_PORT=12345)
  run640 editor-terminal
  expect_status "$status" 0 "an editor's terminal, not an agent"
  expect_contains "$out" "FACT agent-session: PASS" "the editor's marker is not a refusal"
  expect_contains "$out" "dropped from this script's environment" "the row says what it did with it"
}

test_cleanup_leaves_nothing_behind() {
  run640 cleanup
  expect_status "$status" 0 "the cleanup case"
  local scratch
  scratch=$(scratch_of "$work/cleanup")
  [ -n "$scratch" ] || fail "the cleanup case: the run announced no scratch directory"
  expect_scratch_removed "$work/cleanup" "the cleanup case"
  # Nothing else is left in the scratch's own parent either.
  local left
  left=$(find "$work/cleanup/tmp" -mindepth 1 -maxdepth 1 2>/dev/null | head -n 5 | tr '\n' ' ')
  [ -z "$left" ] || fail "the cleanup case: the scratch's parent still holds $left"
}

test_the_block_is_one_block() {
  run640 one-block
  expect_status "$status" 0 "one block"
  local count
  count=$(printf '%s\n' "$out" | grep -c '^CHECK 640 RESULT' || true)
  [ "${count:-0}" -eq 2 ] || fail "one block: the output carries ${count:-0} CHECK 640 RESULT lines, want 2"
  count=$(printf '%s\n' "$out" | grep -c '^FACT ' || true)
  [ "${count:-0}" -eq "${#BLOCK_KEYS[@]}" ] || fail "one block: the block carries ${count:-0} FACT rows, want ${#BLOCK_KEYS[@]}"
  # The block is on stdout, and the progress on stderr.
  expect_absent "$out" "scratch directory:" "the block is stdout only"
  expect_contains "$err" "scratch directory:" "the progress is stderr"
}

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

test_index=0
t test_happy_path
t test_the_check_reads_and_writes_nothing
t test_the_check_runs_no_git_of_its_own
t test_the_audit_lines_are_waited_for
t test_a_running_label_that_never_appears_is_a_failure
t test_a_check_that_starts_after_the_run_ended_is_incomplete
t test_a_status_label_left_behind_is_a_failure
t test_a_failed_run_wants_the_failed_label
t test_a_halted_run_reads_as_a_failure
t test_a_cancelled_run_wants_no_status_label
t test_a_failed_label_missing_is_a_failure
t test_a_failed_label_on_a_successful_run_is_a_failure
t test_an_item_without_a_spec_is_incomplete
t test_a_pull_request_missing_the_spec_label_is_a_failure
t test_a_spec_label_of_another_plan_is_a_failure
t test_the_spec_label_is_folded
t test_a_colour_that_is_not_the_tables_is_a_failure
t test_a_repository_without_the_colon_label_is_a_failure
t test_the_repository_must_hold_the_running_label
t test_a_label_list_that_cannot_be_read_is_incomplete
t test_the_audit_line_names_the_daemon
t test_a_label_act_by_another_caller_is_a_failure
t test_a_refused_act_is_carried_in_the_row
t test_no_audit_line_for_this_repository_is_incomplete
t test_no_audit_line_at_all_is_incomplete
t test_gcloud_refusing_the_audit_read_is_incomplete
t test_a_pull_request_gh_cannot_read_is_incomplete
t test_a_run_that_never_finishes_is_read_as_such
t test_f19_stops_the_run_at_one_named_row
t test_the_daemon_must_be_running_here
t test_no_item_in_flight_is_a_failure
t test_the_running_item_is_found_among_eight
t test_two_items_in_flight_ask_for_run_id
t test_a_run_id_that_is_not_the_items_is_a_failure
t test_the_runs_row_must_be_in_the_listing
t test_a_run_without_a_pull_request_is_a_failure
t test_belong_is_refused
t test_the_installation_and_the_gcp_project_are_held
t test_the_service_must_be_readable
t test_dry_run_touches_nothing
t test_the_plan_and_the_usage_need_no_cloud_tools
t test_argument_complaints
t test_xtrace_is_refused
t test_a_coding_agents_session_is_refused
t test_cleanup_leaves_nothing_behind
t test_the_block_is_one_block

if [ "$failures" -ne 0 ]; then
  echo "check640_test: $failures failure(s)" >&2
  exit 1
fi
echo "check640_test: all tests passed" >&2
