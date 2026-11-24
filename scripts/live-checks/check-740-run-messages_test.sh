#!/usr/bin/env bash
# Tests for scripts/live-checks/check-740-run-messages.sh, against a fake fugaro
# and a fake gcloud: nothing here reaches the network, there is no cloud state,
# and no run is launched. Every document the check reads comes out of
# $FAKE740_DIR, and every call it makes is recorded there, so a case can tell a
# run that read the run's own record from one that stopped before it.
#
# The fakes answer, and never decide: the fate the check reads is the fixture's,
# the Policy Troubleshooter's answer is the fixture's, and the bucket's
# lifecycle configuration is the fixture's -- so a case that expects a row to
# FAIL writes the document that makes it fail, rather than an expectation the
# fake was built to meet.
#
# Two things every case is held to, wherever the case is about something else:
#
#  o the TEXT of a message never reaches the block. The record this suite seeds
#    carries a message's text (that is what the runner records for the owner),
#    and the block must not print it: what the check reads is a fate -- a state,
#    a stage, a flag list, the runner's reasoning -- and never a person's
#    words.
#  o no credential is read on any path: the recorded calls carry no
#    `auth print-access-token` and no token argument, because the check has no
#    use for one.
#
# Usage: bash scripts/live-checks/check-740-run-messages_test.sh
#        CHECK_TEST_ONLY=case_healthy bash scripts/live-checks/check-740-run-messages_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-740-run-messages.sh
BASH_BIN=$(command -v bash)
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK740_TEST_KEEP:-}" ]; then
    echo "kept: $work" >&2
  else
    rm -rf "$work"
  fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 900)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT ' | sed 's/^/ ... block: /')"
}
expect_fact() { # key verdict what
  expect_contains "$out" "FACT $1: $2" "$3 (FACT $1)"
}
expect_calls() { # needle what
  expect_contains "$(cat "$dir/state/calls" 2>/dev/null || true)" "$1" "$2"
}
expect_no_calls() { # needle what
  expect_absent "$(cat "$dir/state/calls" 2>/dev/null || true)" "$1" "$2"
}

INSTALLATION=sandbox740
GCP_PROJECT=prj-sandbox740
REGION=europe-west1
BUCKET=fugaro-runs-prj-sandbox740
REPO=acme/widgets
RUN_ID=run740a
RUN_REF=acme-widgets/run740a
EXECUTION=projects/prj-sandbox740/locations/europe-west1/jobs/fugaro-acme-widgets/executions/run740a
WORKFLOW_SA=fugaro-acme-widgets@prj-sandbox740.iam.gserviceaccount.com
PROJECT_SA=fugaro-project-sandbox740@prj-sandbox740.iam.gserviceaccount.com
PROJECT_JOB=fugaro-p-sandbox740-gh
# The message's own text, as the runner records it for the owner. It must never
# reach the block: the check reads fates, never words.
TEXT_SENTINEL='SENTINEL-a-persons-own-words-in-the-record'

# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(installation belong release bound run-launched message-plain message-now run-ended record iam-workflow-job iam-project-job lifecycle agent-session xtrace restore)

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check-740's test. Every answer comes out of $FAKE740_DIR and
# every call is recorded, so a case can tell a run that launched from one that
# stopped before it. `runs ls` walks rows.1.json, rows.2.json, ... and repeats
# the last one that exists, so a case can drive a run that reaches implement and
# ends, and a case can drive one that never gets there.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE740_DIR/calls"
case "${1:-}" in
doctor)
  cat "$FAKE740_DIR/doctor.json"
  [ ! -f "$FAKE740_DIR/doctor.exit" ] || exit "$(cat "$FAKE740_DIR/doctor.exit")"
  ;;
version)
  cat "$FAKE740_DIR/version.out"
  ;;
tell)
  if [ "${2:-}" = "--help" ]; then
    cat "$FAKE740_DIR/tellhelp.out"
    exit 0
  fi
  n=$(( $(cat "$FAKE740_DIR/tell.seq" 2>/dev/null || printf 0) + 1 ))
  printf '%s\n' "$n" >"$FAKE740_DIR/tell.seq"
  if [ -f "$FAKE740_DIR/tell.status.$n" ]; then
    printf 'fake fugaro: tell refused\n' >&2
    exit "$(cat "$FAKE740_DIR/tell.status.$n")"
  fi
  flags='[]'
  case " $* " in *" --now "*) flags='["now"]' ;; esac
  printf '{"project":"%s","run":"%s","seq":%s,"flags":%s}\n' \
    "$FAKE740_PROJECT" "$2" "$n" "$flags"
  ;;
run)
  [ ! -f "$FAKE740_DIR/launch.status" ] || exit "$(cat "$FAKE740_DIR/launch.status")"
  # `fugaro run`'s own contract, held here because this fake is the only thing
  # the test launches with: the task is the positional TEXT or a file named by
  # --task-file, so a PATH handed over positionally is a run whose task is the
  # literal path and whose brief never arrives. The fake therefore REFUSES a
  # launch without --task-file, reads the file the real CLI would read, and
  # keeps it for a case to read back.
  taskfile="" prev=""
  for a in "$@"; do
    if [ "$prev" = "--task-file" ]; then taskfile=$a; fi
    case "$a" in --task-file=*) taskfile=${a#--task-file=} ;; esac
    prev=$a
  done
  if [ -z "$taskfile" ]; then
    printf 'fake fugaro: run without --task-file: the positional argument is the task TEXT, so the brief has to be handed over as a file\n' >&2
    exit 64
  fi
  if [ ! -r "$taskfile" ]; then
    printf 'fake fugaro: --task-file %s is not a file this launcher can read\n' "$taskfile" >&2
    exit 64
  fi
  cp "$taskfile" "$FAKE740_DIR/task.txt"
  cat "$FAKE740_DIR/launch.json"
  ;;
runs)
  # Walk forward through rows.N.json and stay on the LAST one that exists, so a
  # case writes the row the check reaches after the one before it.
  n=$(( $(cat "$FAKE740_DIR/rows.counter" 2>/dev/null || printf 0) + 1 ))
  printf '%s\n' "$n" >"$FAKE740_DIR/rows.counter"
  f=""
  i=$n
  while [ "$i" -ge 1 ]; do
    if [ -f "$FAKE740_DIR/rows.$i.json" ]; then
      f="$FAKE740_DIR/rows.$i.json"
      break
    fi
    i=$((i - 1))
  done
  [ -n "$f" ] || { printf 'fake fugaro: the case seeded no rows fixture\n' >&2; exit 64; }
  cat "$f"
  ;;
*)
  printf 'fake fugaro: unexpected %s\n' "${1:-}" >&2
  exit 64
  ;;
esac
FUGARO_EOF

  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake gcloud for check-740's test: the run's record, the job and its account,
# the jobs of the project, the Policy Troubleshooter's answer and the bucket's
# lifecycle configuration all come out of $FAKE740_DIR, and every call is
# recorded with its arguments -- which is how a case reads WHICH principal and
# WHICH resource the check asked about.
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"$FAKE740_DIR/calls"
serve() { # serve FILE STATUSFILE
  [ ! -f "$2" ] || exit "$(cat "$2")"
  cat "$1"
}
ts_serve() { # ts_serve SURFACE RESOURCE ARGV... -- the Troubleshooter's own surface
  # The argv a real gcloud has for `policy-troubleshoot iam` is `iam` and then
  # the RESOURCE, positionally: a `--resource=` flag is refused ('unrecognized
  # arguments'), and the API refuses an object-level full resource name
  # (INVALID_ARGUMENT INVALID_RESOURCE_NAME) while accepting the bucket. Both
  # refusals are here, so a check that asks the wrong shape gets nothing.
  local surface=$1 resource=$2
  shift 2
  case " $* " in
  *" --resource="*)
    printf 'ERROR: (gcloud) unrecognized arguments: --resource=\n' >&2
    exit 2
    ;;
  esac
  case " $* " in
  *" --quiet "*) ;;
  *)
    printf 'fake gcloud: policy-troubleshoot without --quiet: This prompt could not be answered because you are not in an interactive session\n' >&2
    exit 64
    ;;
  esac
  case "$resource" in
  troubleshoot)
    printf 'fake gcloud: there is no troubleshoot verb under iam: the resource IS the positional argument (gcloud policy-troubleshoot iam RESOURCE)\n' >&2
    exit 64
    ;;
  *"/objects/"*)
    printf 'ERROR: (google.cloud.policytroubleshooter.v3.PolicyTroubleshooter) INVALID_ARGUMENT: accessTuple.fullResourceName is invalid: %s\n' "$resource" >&2
    exit 1
    ;;
  //storage.googleapis.com/projects/_/buckets/*) ;;
  *)
    printf 'fake gcloud: unexpected troubleshooter resource %s\n' "${resource:-none}" >&2
    exit 64
    ;;
  esac
  # ts.ga.status makes the GA surface answer the way a gcloud that does not know
  # the group does, so a case can drive the check onto the alpha surface. The
  # alpha surface has no such file: it is the surface the fallback reaches.
  if [ "$surface" = ga ] && [ -f "$FAKE740_DIR/ts.ga.status" ]; then
    printf "ERROR: (gcloud) Invalid choice: 'policy-troubleshoot'.\n" >&2
    exit "$(cat "$FAKE740_DIR/ts.ga.status")"
  fi
  [ ! -f "$FAKE740_DIR/ts.err" ] || cat "$FAKE740_DIR/ts.err" >&2
  serve "$FAKE740_DIR/ts.out" "$FAKE740_DIR/ts.status"
}
case "${1:-}" in
run)
  case "${2:-}" in
  jobs)
    case "${3:-}" in
    describe) serve "$FAKE740_DIR/job.json" "$FAKE740_DIR/job.status" ;;
    list) serve "$FAKE740_DIR/jobs.json" "$FAKE740_DIR/jobs.status" ;;
    *) printf 'fake gcloud: unexpected run jobs %s\n' "${3:-}" >&2; exit 64 ;;
    esac
    ;;
  *)
    printf 'fake gcloud: unexpected run %s\n' "${2:-}" >&2
    exit 64
    ;;
  esac
  ;;
storage)
  case "${2:-}" in
  cat) serve "$FAKE740_DIR/record.json" "$FAKE740_DIR/record.status" ;;
  buckets)
    [ "${3:-}" = describe ] || { printf 'fake gcloud: unexpected storage buckets %s\n' "${3:-}" >&2; exit 64; }
    serve "$FAKE740_DIR/bucket.json" "$FAKE740_DIR/bucket.status"
    ;;
  *)
    printf 'fake gcloud: unexpected storage %s\n' "${2:-}" >&2
    exit 64
    ;;
  esac
  ;;
policy-troubleshoot)
  # `gcloud policy-troubleshoot iam RESOURCE`: the resource is a POSITIONAL
  # argument -- `--resource=` is not a flag gcloud has, and a `troubleshoot` verb
  # under `iam` does not exist -- and it must be the BUCKET, because the API
  # refuses an object-level full resource name. ts_serve holds the call to
  # exactly that argv, so the fake refuses the invented verb, a --resource= flag,
  # an object-level name and a call without --quiet, each the way a real gcloud
  # or the real API does. ts.ga.status makes the GA surface answer the way a
  # gcloud that does not know the group does, so a case can drive the check onto
  # the alpha surface.
  [ "${2:-}" = iam ] || { printf 'fake gcloud: policy-troubleshoot takes iam and then the RESOURCE, got %s\n' "${2:-}" >&2; exit 64; }
  ts_serve ga "${3:-}" "$@"
  ;;
alpha)
  [ "${2:-}" = policy-troubleshoot ] || { printf 'fake gcloud: unexpected alpha %s\n' "${2:-}" >&2; exit 64; }
  [ "${3:-}" = iam ] || { printf 'fake gcloud: alpha policy-troubleshoot takes iam and then the RESOURCE, got %s\n' "${3:-}" >&2; exit 64; }
  ts_serve alpha "${4:-}" "$@"
  ;;
policy-troubleshooter)
  # Not a command group on any gcloud: the old spelling this check must never ask.
  printf "ERROR: (gcloud) Invalid choice: 'policy-troubleshooter'. Maybe you meant: gcloud policy-troubleshoot iam\n" >&2
  exit 64
  ;;
*)
  printf 'fake gcloud: unexpected %s\n' "${1:-}" >&2
  exit 64
  ;;
esac
GCLOUD_EOF

  chmod +x "$1/bin/fugaro" "$1/bin/gcloud"
}

# ------------------------------------------------------------------ fixtures
write_doctor() { # dir [json]
  cat >"$1/state/doctor.json" <<EOF
{"version":1,"project":{"name":"$INSTALLATION","gcp_project":"$GCP_PROJECT","region":"$REGION"},"checks":[]}
EOF
}

write_version() { # dir VERSION
  printf '%s\n' "$2" >"$1/state/version.out"
}

write_tellhelp() { # dir yes|no
  if [ "$2" = yes ]; then
    cat >"$1/state/tellhelp.out" <<'EOF'
Send a message to a run that is going.

Usage:
  fugaro tell RUN "text" [flags]

Flags:
      --now       stop the coder stage at the agent's next quiet point
      --wrap-up   ask the run to commit what it has and stop
      --json      print the result as JSON
EOF
  else
    cat >"$1/state/tellhelp.out" <<'EOF'
fugaro: 'tell' is not a fugaro command. See 'fugaro --help'.
EOF
  fi
}

write_launch() { # dir
  cat >"$1/state/launch.json" <<EOF
{"run_id":"$RUN_ID","repo":"$REPO","run":"$RUN_REF","status":"running"}
EOF
}

write_rows() { # dir [ended-status] [ended-stage] — rows.1: implement; rows.2: ended
  local estatus=${2:-succeeded} estage=${3:-finalize}
  cat >"$1/state/rows.1.json" <<EOF
{"runs":[{"run_id":"$RUN_ID","repo":"$REPO","run":"$RUN_REF","status":"running","stage":"implement","execution":"$EXECUTION","terminal":false}]}
EOF
  cat >"$1/state/rows.2.json" <<EOF
{"runs":[{"run_id":"$RUN_ID","repo":"$REPO","run":"$RUN_REF","status":"$estatus","stage":"$estage","execution":"$EXECUTION","terminal":true}]}
EOF
}

write_record() { # dir [plain-state] [plain-stage] [now-state] [now-stage] [now-reason] [record-stage] [record-status]
  local state=${2:-delivered} stage=${3:-implement}
  local nstate=${4:-delivered} nstage=${5:-implement} reason=${6:-}
  # The run's own ending: a run that got past implement ends at a later stage
  # and succeeded, which is the default every case that is not about the ending
  # takes. A record's `stage` and `status` are top-level fields the runner always
  # writes, so a fixture that left them out would be a record no run writes.
  local rstage=${7:-finalize} rstatus=${8:-succeeded}
  local reason_json=""
  [ -z "$reason" ] || reason_json=",\"reason\":\"$reason\""
  cat >"$1/state/record.json" <<EOF
{"version":1,"run_id":"$RUN_ID","repo":"$REPO","execution":"$EXECUTION",
 "stage":"$rstage","status":"$rstatus",
 "messages":{"accepting":true,"format":1,"flags":["now","wrap_up"],"messages":[
  {"seq":1,"state":"$state","stage":"$stage","flags":[],"object":"tokens/acme-widgets/$RUN_ID/messages/0001.msg.json","text":"$TEXT_SENTINEL"},
  {"seq":2,"state":"$nstate","stage":"$nstage","flags":["now"],"object":"tokens/acme-widgets/$RUN_ID/messages/0002.msg.json","text":"$TEXT_SENTINEL"$reason_json}]}}
EOF
}

write_job() { # dir
  cat >"$1/state/job.json" <<EOF
{"metadata":{"name":"fugaro-acme-widgets","labels":{"fugaro":"managed"}},
 "spec":{"template":{"spec":{"template":{"spec":{"serviceAccountName":"$WORKFLOW_SA"}}}}}}
EOF
}

write_jobs() { # dir yes|no — whether the installation deploys a shared project job
  if [ "$2" = yes ]; then
    cat >"$1/state/jobs.json" <<EOF
[{"metadata":{"name":"$PROJECT_JOB","labels":{"fugaro":"managed","fugaro_profile":"$INSTALLATION"}},
  "spec":{"template":{"spec":{"template":{"spec":{"serviceAccountName":"$PROJECT_SA"}}}}}}]
EOF
  else
    printf '[]\n' >"$1/state/jobs.json"
  fi
}

write_ts() { # dir OVERALL_ACCESS_STATE [variant]
  # The answer the command really prints: the v3 Explainer, whose overall
  # decision is `overallAccessState` (CAN_ACCESS / CANNOT_ACCESS / UNKNOWN_*)
  # and whose bindings sit in
  # allowPolicyExplanation.explainedPolicies[].bindingExplanations[], each with a
  # `rolePermission` mark saying whether its role carries the permission asked
  # about and a `memberships` map saying whose binding it is. The binding carries
  # the tokens/ prefix condition on the object name, which is what a question
  # asked at BUCKET level leaves the row to report.
  #
  # Variants: 'no-condition' answers with no condition at all, 'writer-role'
  # makes the role one that DOES carry storage.objects.create
  # (roles/storage.objectCreator, ROLE_PERMISSION_INCLUDED) instead of
  # objectViewer, which does not, and 'writer-other-member' makes that writer
  # binding belong to somebody who is not the account asked about.
  local variant=${3:-} role=roles/storage.objectViewer permission=ROLE_PERMISSION_NOT_INCLUDED
  case "$variant" in
  writer-role | writer-other-member)
    role=roles/storage.objectCreator
    permission=ROLE_PERMISSION_INCLUDED
    ;;
  esac
  local members
  if [ "$variant" = writer-other-member ]; then
    members=$(printf '"serviceAccount:someone-else@example.com":{"membership":"MEMBERSHIP_MATCHED"}')
  else
    members=$(printf '"serviceAccount:%s":{"membership":"MEMBERSHIP_MATCHED"},"serviceAccount:%s":{"membership":"MEMBERSHIP_MATCHED"}' "$WORKFLOW_SA" "$PROJECT_SA")
  fi
  local cond=""
  if [ "${3:-}" != no-condition ]; then
    cond=$(printf ',"condition":{"expression":"resource.name.startsWith(\\"projects/_/buckets/%s/objects/tokens/\\")"}' "$BUCKET")
  fi
  printf '{"accessTuple":{"fullResourceName":"//storage.googleapis.com/projects/_/buckets/%s","permission":"storage.objects.create"},"allowPolicyExplanation":{"explainedPolicies":[{"bindingExplanations":[{"role":"%s","rolePermission":"%s","memberships":{%s}%s}]}]},"overallAccessState":"%s","errors":[]}\n' \
    "$BUCKET" "$role" "$permission" "$members" "$cond" "$2" >"$1/state/ts.out"
}

write_bucket() { # dir yes|no-token|no-rule|not-delete
  case "$2" in
  yes)
    cat >"$1/state/bucket.json" <<'EOF'
{"name":"fugaro-runs-prj-sandbox740","lifecycleConfig":{"rule":[
 {"action":{"type":"Delete"},"condition":{"age":1,"matchesPrefix":["runs/","tokens/"],"matchesSuffix":["/budget-token"]}},
 {"action":{"type":"Delete"},"condition":{"age":30,"matchesPrefix":["tokens/"],"matchesSuffix":[".msg.json"]}}]}}
EOF
    ;;
  no-token)
    # The message rule has no suffix at all, so it reaches the sealed budget
    # token beside a message object as well: the widened rule U4 exists to stop.
    cat >"$1/state/bucket.json" <<'EOF'
{"name":"fugaro-runs-prj-sandbox740","lifecycleConfig":{"rule":[
 {"action":{"type":"Delete"},"condition":{"age":1,"matchesPrefix":["runs/","tokens/"],"matchesSuffix":["/budget-token"]}},
 {"action":{"type":"Delete"},"condition":{"age":30,"matchesPrefix":["tokens/"]}}]}}
EOF
    ;;
  no-rule)
    cat >"$1/state/bucket.json" <<'EOF'
{"name":"fugaro-runs-prj-sandbox740","lifecycleConfig":{"rule":[
 {"action":{"type":"Delete"},"condition":{"age":1,"matchesPrefix":["runs/","tokens/"],"matchesSuffix":["/budget-token"]}}]}}
EOF
    ;;
  not-delete)
    cat >"$1/state/bucket.json" <<'EOF'
{"name":"fugaro-runs-prj-sandbox740","lifecycleConfig":{"rule":[
 {"action":{"type":"SetStorageClass","storageClass":"NEARLINE"},"condition":{"age":30,"matchesPrefix":["tokens/"],"matchesSuffix":[".msg.json"]}}]}}
EOF
    ;;
  esac
}

# ------------------------------------------------------------------ the case
setup_case() { # name
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp"
  write_fakes "$dir"
  : >"$dir/state/calls"
  write_doctor "$dir"
  write_version "$dir" 0.27.0
  write_tellhelp "$dir" yes
  write_launch "$dir"
  write_rows "$dir"
  write_record "$dir"
  write_job "$dir"
  write_jobs "$dir" yes
  write_ts "$dir" CANNOT_ACCESS
  write_bucket "$dir" yes
}

# run740 [ARGS...] — runs the check on the case at hand. The knobs are the
# test's: every wait is a second or two, so a case that never reaches implement
# still ends, and the gap between the two messages is nothing.
run740() {
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT -u CLAUDE_CODE_SSE_PORT \
    TMPDIR="$dir/tmp" FAKE740_DIR="$dir/state" FAKE740_PROJECT="$GCP_PROJECT" \
    CHECK740_CALL_SECONDS=10 CHECK740_LAUNCH_SECONDS=10 CHECK740_RUN_SECONDS=3 \
    CHECK740_POLL_SECONDS=1 CHECK740_IMPLEMENT_SECONDS=3 CHECK740_SEND_GAP_SECONDS=0 \
    PATH="${RUN740_PATH:-$dir/bin:$PATH}" \
    ${RUN740_ENV[@]+"${RUN740_ENV[@]}"} \
    "$BASH_BIN" ${RUN740_BASH[@]+"${RUN740_BASH[@]}"} "$script" ${RUN740_ARGS[@]+"${RUN740_ARGS[@]}"} "$@" 2>"$dir/err") || status=$?
  RUN740_ARGS=()
  RUN740_ENV=()
  RUN740_PATH=""
  RUN740_BASH=()
  # Every case's own assertion, wherever the case is about something else: a
  # message's TEXT is in no line this run wrote, and the check read no token.
  expect_absent "$out$(cat "$dir/err" 2>/dev/null || true)" "$TEXT_SENTINEL" \
    "case $dir: a message's own text reached the block or the log"
  expect_absent "$(cat "$dir/state/calls" 2>/dev/null || true)" "print-access-token" \
    "case $dir: the check asked for an access token, which it has no use for"
}

expect_block_keys() { # what
  local key
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$out" "FACT $key:" "$1 (FACT $key must be in every block)"
  done
}

expect_clean() { # what — the scratch directory the check made is gone
  local left
  left=$(ls -A "$dir/tmp" 2>/dev/null || true)
  [ -z "$left" ] || fail "$1: the check left $dir/tmp holding: $(printf '%s' "$left" | tr '\n' ' ')"
  expect_fact restore PASS "$1 (restore)"
}

# ------------------------------------------------------------------- the cases
case_healthy() {
  setup_case healthy
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 0 "healthy: a run the channel delivered both messages on"
  expect_block_keys "healthy"
  expect_fact installation PASS "healthy"
  expect_fact belong PASS "healthy"
  expect_fact release PASS "healthy"
  expect_fact run-launched PASS "healthy"
  expect_fact message-plain PASS "healthy"
  expect_fact message-now PASS "healthy"
  expect_contains "$out" "SAME session" "healthy: the --now row must be the same-session statement"
  expect_contains "$out" "U2 and U3" "healthy: the row must name what it settles"
  expect_fact run-ended PASS "healthy"
  expect_fact record PASS "healthy"
  expect_fact lifecycle PASS "healthy"
  expect_fact agent-session PASS "healthy"
  expect_fact xtrace PASS "healthy"
  expect_contains "$out" "CHECK 740 RESULT: PASS (exit 0)" "healthy: the verdict line"
  expect_clean "healthy"

  # It launched one real run of the repository it was given, by name.
  expect_calls "fugaro run --repo $REPO" "healthy: the run is launched in the repository named"
  # The brief goes over as --task-file, never as the positional argument, which
  # is the task TEXT: a path passed that way launches a run whose task is the
  # path, which the fake refuses. The file it read back is the brief itself.
  expect_calls "run --repo $REPO --json --task-file " "healthy: the brief is handed over with --task-file, not as the positional TEXT"
  expect_contains "$(cat "$dir/state/task.txt" 2>/dev/null || true)" "# Task: check 740" "healthy: the launcher read the brief the check wrote, so the run's task is the one-small-file task and not a path"
  expect_calls "runs ls --repo $REPO" "healthy: the run is followed through the CLI"
  # It read the record of THAT run, from the runs bucket, under the slug the
  # launch named.
  expect_calls "storage cat gs://$BUCKET/runs/$RUN_REF/result.json --project=$GCP_PROJECT" \
    "healthy: the record is read at the run's own key"
  # The two messages, in the order the check promises: the plain one first, so
  # it is pending when the stop fires, and the --now one second.
  local tells first second
  tells=$(grep 'fugaro tell' "$dir/state/calls" | grep -v -- '--help' || true)
  first=$(printf '%s\n' "$tells" | sed -n '1p')
  second=$(printf '%s\n' "$tells" | sed -n '2p')
  expect_contains "$first" "tell $RUN_ID" "healthy: the first message is sent to the run"
  expect_absent "$first" "--now" "healthy: the FIRST message must carry no flag (it rides the stop the second one asks for)"
  expect_contains "$second" "--now" "healthy: the second message is the --now one"
  expect_absent "$(printf '%s\n' "$tells" | sed -n '3p')" "tell" "healthy: exactly two messages are sent"
  # D2941 is asked about the REAL job account, and about the shared project
  # job's own account: the two calls name the accounts from the documents, the
  # resource is POSITIONAL and it is the BUCKET (the API refuses an object-level
  # name), and the fence the question cannot evaluate at that level comes back as
  # the condition the answer explains.
  expect_calls "policy-troubleshoot iam //storage.googleapis.com/projects/_/buckets/$BUCKET --permission=storage.objects.create" \
    "healthy: the Policy Troubleshooter is asked about the bucket, the resource positional"
  expect_calls "--quiet" "healthy: the call carries --quiet, or gcloud stops at a prompt no session can answer"
  expect_no_calls "policy-troubleshooter " "healthy: the old 'policy-troubleshooter' group is never asked"
  expect_no_calls "alpha " "healthy: the GA surface answers, so no alpha fallback is made"
  expect_no_calls " --resource=" "healthy: the resource is positional; --resource= is not a flag gcloud has"
  expect_no_calls "/objects/" "healthy: no object-level resource name is asked"
  expect_calls "--principal-email=$WORKFLOW_SA" "healthy: the workflow job's OWN account is asked about"
  expect_calls "--principal-email=$PROJECT_SA" "healthy: the shared project job's own account is asked about"
  expect_contains "$out" "resource.name.startsWith" "healthy: the row reports the binding condition the answer explains"
  expect_contains "$out" "object-name condition is not evaluated" "healthy: the row says the object-name fence is not evaluated at bucket level"
  expect_calls "storage buckets describe gs://$BUCKET" "healthy: the applied bucket's own configuration is read"
}

case_quiet_point_missed() {
  setup_case quiet-point
  write_record "$dir" delivered implement delivered implement "no quiet point in 10m"
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 1 "quiet-point: a stop that never found a quiet point"
  expect_fact message-now FAIL "quiet-point"
  expect_contains "$out" "no quiet point in 10m" "quiet-point: the runner's own reason is quoted"
  expect_contains "$out" "U3's NEGATIVE" "quiet-point: the row must name what it settles"
  expect_contains "$out" "CHECK 740 RESULT: FAIL (exit 1)" "quiet-point: the verdict line"
}

case_session_lost() {
  setup_case session-lost
  write_record "$dir" delivered implement delivered implement "session lost at forced stop"
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 1 "session-lost: a forced stop that lost the session"
  expect_fact message-now FAIL "session-lost"
  expect_contains "$out" "U2's NEGATIVE" "session-lost: the row must name what it settles"
}

case_unknown_reason() {
  setup_case unknown-reason
  write_record "$dir" delivered implement delivered implement "something else entirely"
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 1 "unknown-reason"
  expect_fact message-now FAIL "unknown-reason"
  expect_contains "$out" "something else entirely" "unknown-reason: the reason is quoted as it stands"
}

case_now_boundary() {
  setup_case now-boundary
  # Delivered into a coder stage, with no reason -- but into a LATER one, which
  # is a boundary delivery and not the quiet point this row asks about.
  write_record "$dir" delivered implement delivered fix
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 3 "now-boundary: a row left unmeasured is INCOMPLETE, never a pass"
  expect_fact message-plain PASS "now-boundary: the plain message did reach a coder stage"
  expect_fact message-now UNVERIFIED "now-boundary"
  expect_contains "$out" "NOT settled here" "now-boundary: the row says what it did not settle"
  expect_contains "$out" "CHECK 740 RESULT: INCOMPLETE (exit 3)" "now-boundary: the verdict line"
}

case_now_ended_in_implement() {
  setup_case now-ended-in-implement
  # The fate a FAILED resume leaves. The entry is written when the stop claims
  # the message and BEFORE the turn it resumes runs, and the session-lost reason
  # is written only for a FORCED stop whose resume failed — so a quiet-point stop
  # whose resume found no session to continue (agent.ErrNoSession, which a first
  # run's implement has no recovery for) leaves delivered/implement/reason="" and
  # the run ends failed right there. The record's own ending is what tells that
  # apart from a resume that went on, and this one is NOT U2 holding.
  write_record "$dir" delivered implement delivered implement "" implement failed
  write_rows "$dir" failed implement
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 3 "now-ended-in-implement: a row left unmeasured is INCOMPLETE, never a pass"
  expect_fact message-plain PASS "now-ended-in-implement: the plain message did reach a coder stage"
  expect_fact message-now UNVERIFIED "now-ended-in-implement: a run that ended in implement cannot read as the quiet point the row asks about"
  expect_contains "$out" "U2 and U3 are NOT settled by this run" "now-ended-in-implement: the row says what it did not settle"
  expect_contains "$out" "stage=implement" "now-ended-in-implement: the row names the ending it read"
  expect_absent "$out" "U2 and U3 hold" "now-ended-in-implement: the same-session statement must not be printed"
  expect_contains "$out" "CHECK 740 RESULT: INCOMPLETE (exit 3)" "now-ended-in-implement: the verdict line"
}

case_now_ended_later() {
  setup_case now-ended-later
  # The run went on PAST implement and failed later (a review round, say). The
  # turn after the stop did run — implement is behind it — so U2 and U3 are
  # settled, and how the run ended afterwards is the run-ended row's business
  # and not this one's.
  write_record "$dir" delivered implement delivered implement "" review failed
  write_rows "$dir" failed review
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_fact run-ended PASS "now-ended-later: every fate is this run's own whatever its ending"
  expect_fact message-now PASS "now-ended-later: the run is past the stage the message reached, so the turn after the stop ran"
  expect_contains "$out" "SAME session" "now-ended-later: the same-session statement"
}

case_now_undelivered() {
  setup_case now-undelivered
  write_record "$dir" delivered implement undelivered "" "no coder stage was left when the run ended"
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 1 "now-undelivered"
  expect_fact message-now FAIL "now-undelivered"
  expect_contains "$out" "NOT settled by this run" "now-undelivered: the row must not claim U2/U3"
}

case_plain_undelivered() {
  setup_case plain-undelivered
  write_record "$dir" undelivered "" delivered implement
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 1 "plain-undelivered"
  expect_fact message-plain FAIL "plain-undelivered"
  expect_contains "$out" "no coder stage was left" "plain-undelivered: the runner's reason is quoted"
}

case_plain_reached_reviewer() {
  setup_case plain-reviewer
  # The fence itself: a message delivered into a reviewing stage is a reviewer
  # reading the person's words.
  write_record "$dir" delivered review delivered implement
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 1 "plain-reviewer"
  expect_fact message-plain FAIL "plain-reviewer"
  expect_contains "$out" "rule 1, D2942" "plain-reviewer: the row names the rule it broke"
}

case_plain_refused() {
  setup_case plain-refused
  write_record "$dir" refused "" delivered implement
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 1 "plain-refused"
  expect_fact message-plain FAIL "plain-refused"
}

case_record_of_another_run() {
  setup_case other-run
  write_record "$dir"
  # The record at that key is another run's: nothing below it can be read.
  perl -pi -e "s/\"run_id\":\"$RUN_ID\"/\"run_id\":\"run740z\"/" "$dir/state/record.json"
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 1 "other-run"
  expect_fact record FAIL "other-run"
  expect_contains "$out" "is another run's" "other-run: the row says why nothing below can be read"
}

case_record_not_accepting() {
  setup_case not-accepting
  write_record "$dir"
  perl -pi -e 's/"accepting":true/"accepting":false/' "$dir/state/record.json"
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 1 "not-accepting"
  expect_fact record FAIL "not-accepting"
  expect_contains "$out" "reads no message at all" "not-accepting: the row names what a missing channel means"
}

case_record_unreadable() {
  setup_case record-unreadable
  printf '5\n' >"$dir/state/record.status"
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 1 "record-unreadable"
  expect_fact record FAIL "record-unreadable"
  expect_fact message-plain UNVERIFIED "record-unreadable: a fate can be read only from a record"
}

case_iam_granted() {
  setup_case iam-granted
  write_ts "$dir" CAN_ACCESS
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 1 "iam-granted: a job account that may write its own inbox"
  expect_fact iam-workflow-job FAIL "iam-granted"
  expect_fact iam-project-job FAIL "iam-granted"
  expect_contains "$out" "ANOTHER repository's running run" "iam-granted: the row says what the shared job's grant would reach"
  expect_fact message-now PASS "iam-granted: the delivery rows are not what failed"
}

case_iam_unknown() {
  setup_case iam-unknown
  # An answer that is neither an access nor a denial settles nothing, and must
  # never read as a pass.
  write_ts "$dir" UNKNOWN_INFO_DENIED
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 3 "iam-unknown"
  expect_fact iam-workflow-job UNVERIFIED "iam-unknown"
  expect_fact iam-project-job UNVERIFIED "iam-unknown"
  expect_contains "$out" "UNKNOWN_INFO_DENIED" "iam-unknown: the answer is quoted as it stands"
}

case_iam_conditional_writer() {
  setup_case iam-conditional-writer
  # CANNOT_ACCESS, but the same answer explains a CONDITIONAL binding whose role
  # DOES carry storage.objects.create -- the account's grant on tokens/ become
  # objectCreator under the same condition, which is exactly the change D2941
  # exists to catch. The bucket-level answer cannot evaluate that condition, so
  # the row must not read the denial as the fence: it names the role and the
  # condition and comes out not measured.
  write_ts "$dir" CANNOT_ACCESS writer-role
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 3 "iam-conditional-writer"
  expect_fact iam-workflow-job UNVERIFIED "iam-conditional-writer: a conditional role that carries the permission is not a pass"
  expect_fact iam-project-job UNVERIFIED "iam-conditional-writer"
  expect_contains "$out" "roles/storage.objectCreator" "iam-conditional-writer: the row prints the role the answer explains"
  expect_contains "$out" "NOT measured by it" "iam-conditional-writer: the row says the fence was not measured"
}

case_iam_writer_other_member() {
  setup_case iam-writer-other-member
  # The same conditional writer role, but the answer says the binding is
  # somebody else's (its memberships name neither of the accounts asked about),
  # so it is not a statement about the job account and the denial stands as the
  # fence.
  write_ts "$dir" CANNOT_ACCESS writer-other-member
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 0 "iam-writer-other-member"
  expect_fact iam-workflow-job PASS "iam-writer-other-member: another member's writer binding is not the job account's"
  expect_fact iam-project-job PASS "iam-writer-other-member"
}

case_no_project_job() {
  setup_case no-project-job
  write_jobs "$dir" no
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 0 "no-project-job"
  expect_fact iam-project-job PASS "no-project-job: an installation with no shared job has nothing to ask about"
  expect_contains "$out" "deploys NO shared project job" "no-project-job"
  expect_no_calls "--principal-email=$PROJECT_SA" "no-project-job: no account is invented to ask about"
}

case_job_unreadable() {
  setup_case job-unreadable
  printf '7\n' >"$dir/state/job.status"
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 3 "job-unreadable"
  expect_fact iam-workflow-job UNVERIFIED "job-unreadable"
  expect_contains "$out" "roles/run.viewer" "job-unreadable: the row names the permission it needs"
}

case_lifecycle_absent() {
  setup_case lifecycle-absent
  write_bucket "$dir" no-rule
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 3 "lifecycle-absent"
  expect_fact lifecycle UNVERIFIED "lifecycle-absent"
  expect_contains "$out" "keeps it until something deletes it" "lifecycle-absent: the row says what a missing rule means"
}

case_lifecycle_selects_token() {
  setup_case lifecycle-token
  write_bucket "$dir" no-token
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 1 "lifecycle-token: a rule that reaches the sealed budget token"
  expect_fact lifecycle FAIL "lifecycle-token"
  expect_contains "$out" "budget-token" "lifecycle-token: the row names the object it would reach"
}

case_lifecycle_not_delete() {
  setup_case lifecycle-not-delete
  write_bucket "$dir" not-delete
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 1 "lifecycle-not-delete"
  expect_fact lifecycle FAIL "lifecycle-not-delete"
}

case_bucket_unreadable() {
  setup_case bucket-unreadable
  printf '5\n' >"$dir/state/bucket.status"
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 3 "bucket-unreadable"
  expect_fact lifecycle UNVERIFIED "bucket-unreadable"
}

case_release_too_old() {
  setup_case release-old
  write_version "$dir" 0.26.0
  write_tellhelp "$dir" yes
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 2 "release-old: a build that predates the channel stops the run"
  expect_fact release FAIL "release-old"
  expect_contains "$out" "fugaro update" "release-old: the row names the one command that fixes it"
  # Nothing was launched and nothing was read: the gate is before the run.
  expect_no_calls "fugaro run" "release-old: no run is launched on a build without the channel"
  expect_no_calls "storage cat" "release-old"
}

case_release_no_flags() {
  setup_case release-no-flags
  # A version number that reads as new, on a build whose help names none of the
  # channel's flags: the feature probe decides, and it says no.
  write_version "$dir" 0.27.0
  write_tellhelp "$dir" no
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 2 "release-no-flags"
  expect_fact release FAIL "release-no-flags"
  expect_contains "$out" "no run-messages channel" "release-no-flags"
}

case_release_dev_build() {
  setup_case release-dev
  write_version "$dir" dev
  run740 --installation "$INSTALLATION" --repo "$REPO"
  # A build that names no release number is decided by the feature probe, which
  # says yes here: the run goes on.
  expect_status "$status" 0 "release-dev: a dev build carrying the flags is not refused"
  expect_fact release PASS "release-dev"
}

case_belong_installation() {
  setup_case belong-installation
  run740 --installation belong --repo "$REPO"
  expect_status "$status" 2 "belong-installation"
  expect_fact belong FAIL "belong-installation"
  expect_contains "$out" "never runs against 'belong'" "belong-installation"
  # Refused before the first call: the fake's calls file is still empty.
  expect_no_calls "fugaro doctor" "belong-installation: the refusal is before any call"
  expect_no_calls "fugaro run" "belong-installation"
}

case_belong_repo() {
  setup_case belong-repo
  run740 --installation "$INSTALLATION" --repo acme/belong-sandbox
  expect_status "$status" 2 "belong-repo"
  expect_fact belong FAIL "belong-repo"
  expect_no_calls "fugaro doctor" "belong-repo: the refusal is before any call"
}

case_belong_project() {
  setup_case belong-project
  # The name typed is fine; the config in force answers a project that names
  # belong, and the check has to see the same installation twice.
  perl -pi -e "s/prj-sandbox740/prj-belong-sandbox/" "$dir/state/doctor.json"
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 2 "belong-project"
  expect_fact belong FAIL "belong-project"
  expect_no_calls "fugaro run" "belong-project: refused before the run"
}

case_name_mismatch() {
  setup_case name-mismatch
  run740 --installation other-installation --repo "$REPO"
  expect_status "$status" 2 "name-mismatch"
  expect_fact installation FAIL "name-mismatch"
  expect_contains "$out" "--installation $INSTALLATION" "name-mismatch: the row says which name to type"
}

case_doctor_refused() {
  setup_case doctor-refused
  printf '2\n' >"$dir/state/doctor.exit"
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 2 "doctor-refused"
  expect_contains "$out" "did not answer" "doctor-refused"
  expect_no_calls "fugaro run" "doctor-refused"
}

case_no_implement() {
  setup_case no-implement
  # A run that never reaches a coder stage this check can see: neither message
  # can be sent, and both rows say so rather than reading as a pass.
  cat >"$dir/state/rows.1.json" <<EOF
{"runs":[{"run_id":"$RUN_ID","repo":"$REPO","run":"$RUN_REF","status":"running","stage":"plan","execution":"$EXECUTION","terminal":false}]}
EOF
  rm -f "$dir/state/rows.2.json"
  RUN740_ENV=(CHECK740_IMPLEMENT_SECONDS=2 CHECK740_RUN_SECONDS=2 CHECK740_POLL_SECONDS=1)
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 3 "no-implement"
  expect_fact message-plain UNVERIFIED "no-implement"
  expect_fact message-now UNVERIFIED "no-implement"
  expect_no_calls "fugaro tell $RUN_ID" "no-implement: no message is sent into a stage that never ran"
}

case_send_refused() {
  setup_case send-refused
  # The runner refuses the first message (a run whose runner reads no messages,
  # say). The check sends nothing else and says so.
  printf '1\n' >"$dir/state/tell.status.1"
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 3 "send-refused"
  expect_fact message-plain UNVERIFIED "send-refused"
  expect_fact message-now UNVERIFIED "send-refused"
  expect_contains "$out" "tell refused" "send-refused: the CLI's own words are quoted"
}

case_second_send_refused() {
  setup_case second-refused
  printf '1\n' >"$dir/state/tell.status.2"
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 3 "second-refused"
  expect_fact message-plain PASS "second-refused: the plain message did land"
  expect_fact message-now UNVERIFIED "second-refused"
}

case_run_never_ended() {
  setup_case never-ended
  # The run is still going when the check's own bound runs out: its record is NOT
  # read, because a record read mid-run is whatever was there before.
  rm -f "$dir/state/rows.2.json"
  RUN740_ENV=(CHECK740_RUN_SECONDS=2 CHECK740_POLL_SECONDS=1)
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 3 "never-ended"
  expect_fact run-ended UNVERIFIED "never-ended"
  expect_no_calls "storage cat" "never-ended: no record is read while the run is going"
}

case_launch_failed() {
  setup_case launch-failed
  printf '1\n' >"$dir/state/launch.status"
  printf 'fugaro: the repository is not one a run starts on\n' >"$dir/state/launch.err"
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 1 "launch-failed"
  expect_fact run-launched FAIL "launch-failed"
  expect_fact message-plain UNVERIFIED "launch-failed"
  expect_no_calls "fugaro tell $RUN_ID" "launch-failed"
}

case_launch_nameless() {
  setup_case launch-nameless
  printf '{"status":"running"}\n' >"$dir/state/launch.json"
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 1 "launch-nameless: a launch that names no run cannot be followed"
  expect_fact run-launched FAIL "launch-nameless"
  expect_contains "$out" "named no run" "launch-nameless"
}

case_agent_session() {
  setup_case agent
  RUN740_ENV=(CLAUDECODE=1)
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 2 "agent: an agent's session is refused"
  expect_fact agent-session FAIL "agent"
  expect_contains "$out" "yours to run at your own terminal" "agent"
  expect_no_calls "fugaro doctor" "agent: refused before any call"
}

case_editor_terminal_is_allowed() {
  setup_case editor
  # The editor extension's own marker says a person is typing in an editor's
  # terminal, not that an agent is driving: it is dropped, and the check runs.
  RUN740_ENV=(CLAUDE_CODE_SSE_PORT=12345)
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 0 "editor: a person in an editor's terminal is allowed"
  expect_fact agent-session PASS "editor"
  expect_contains "$out" "CLAUDE_CODE_SSE_PORT" "editor: the row says which marker it dropped"
}

case_xtrace() {
  setup_case xtrace
  # On its own line: `RUN740_BASH=(-x) run740` would pass the string "(-x)".
  RUN740_BASH=(-x)
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 2 "xtrace: a tracing shell is refused"
  expect_fact xtrace FAIL "xtrace"
  expect_no_calls "fugaro doctor" "xtrace: refused before any call"
}

case_missing_tools() {
  setup_case missing-tools
  mkdir -p "$dir/empty"
  RUN740_PATH="$dir/empty"
  run740 --installation "$INSTALLATION" --repo "$REPO"
  # bash itself is absolute, so the first tool it looks for is the one it names.
  expect_status "$status" 2 "missing-tools"
  expect_contains "$out" "needs" "missing-tools: the row names the tool it needs"
  expect_no_calls "fugaro doctor" "missing-tools"
}

case_no_args() {
  setup_case no-args
  run740
  expect_status "$status" 2 "no-args"
  expect_contains "$out" "--installation is required" "no-args"
  expect_no_calls "fugaro doctor" "no-args: nothing is called without an installation"
}

case_repo_needs_a_slash() {
  setup_case repo-slash
  # `fugaro run --repo` takes a repository as owner/name or a path inside a
  # checkout; a bare word is neither, and the check refuses it rather than
  # launching against whatever the local config would make of it.
  run740 --installation "$INSTALLATION" --repo widgets
  expect_status "$status" 2 "repo-slash: --repo must be owner/name or a path"
  expect_contains "$out" "owner/name" "repo-slash"
  expect_no_calls "fugaro run" "repo-slash"
}

case_troubleshooter_alpha() {
  setup_case ts-alpha
  # A gcloud that does not know the GA verb answers 'Invalid choice', and the
  # check asks the alpha surface rather than giving up: the row is still a
  # measurement, not an UNVERIFIED.
  printf '2\n' >"$dir/state/ts.ga.status"
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 0 "ts-alpha"
  expect_fact iam-workflow-job PASS "ts-alpha"
  expect_calls "alpha policy-troubleshoot iam //storage.googleapis.com/projects/_/buckets/$BUCKET" "ts-alpha: the alpha surface is asked, resource positional"
  expect_calls "--quiet" "ts-alpha: the alpha call carries --quiet too"
}

case_troubleshooter_silent() {
  setup_case ts-silent
  # Neither surface answers -- the GA group is not known and the alpha surface is
  # refused too -- so both rows are UNVERIFIED, and the command each hands the
  # owner to ask by hand is the one that works: the real group, the resource
  # positional, and --quiet.
  printf '2\n' >"$dir/state/ts.ga.status"
  printf '5\n' >"$dir/state/ts.status"
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 3 "ts-silent"
  expect_fact iam-workflow-job UNVERIFIED "ts-silent"
  expect_fact iam-project-job UNVERIFIED "ts-silent"
  expect_contains "$out" "Ask it yourself: gcloud policy-troubleshoot iam //storage.googleapis.com/projects/_/buckets/$BUCKET --permission=storage.objects.create" "ts-silent: the row hands over the command that works, resource positional"
  expect_contains "$out" "--quiet" "ts-silent: the command it hands over carries --quiet"
  expect_absent "$out" "--resource=" "ts-silent: the row must not hand over the invented --resource= flag"
}

case_troubleshooter_disabled() {
  setup_case ts-disabled
  # The API was never enabled on the project: gcloud answers SERVICE_DISABLED,
  # which is not a 'the verb moved' answer, so the alpha surface is not tried --
  # and the row names the one command that fixes it rather than asking again.
  printf '1\n' >"$dir/state/ts.status"
  printf 'ERROR: (gcloud.policy-troubleshoot.iam) PERMISSION_DENIED: Policy Troubleshooter API (policytroubleshooter.googleapis.com) has not been used in project prj-sandbox740 before or it is disabled. Enable it by visiting https://console.developers.google.com/apis/api/policytroubleshooter.googleapis.com/overview?project=prj-sandbox740 then retry. reason: SERVICE_DISABLED\n' >"$dir/state/ts.err"
  run740 --installation "$INSTALLATION" --repo "$REPO"
  expect_status "$status" 3 "ts-disabled"
  expect_fact iam-workflow-job UNVERIFIED "ts-disabled"
  expect_fact iam-project-job UNVERIFIED "ts-disabled"
  expect_contains "$out" "SERVICE_DISABLED" "ts-disabled: the row quotes the refusal the API gave"
  expect_contains "$out" "gcloud services enable policytroubleshooter.googleapis.com --project=$GCP_PROJECT" "ts-disabled: the row names the one command that enables the API"
  expect_no_calls "alpha policy-troubleshoot" "ts-disabled: a disabled API is not a moved verb, so the alpha surface is not tried"
}

case_unknown_argument() {
  setup_case unknown-arg
  run740 --installation "$INSTALLATION" --repo "$REPO" --nonsense
  expect_status "$status" 2 "unknown-arg"
  expect_contains "$out" "unknown argument" "unknown-arg"
}

case_dry_run() {
  setup_case dry-run
  run740 --installation "$INSTALLATION" --repo "$REPO" --dry-run
  expect_status "$status" 0 "dry-run"
  expect_contains "$out" "This is the PLAN" "dry-run: the plan is what it prints"
  expect_contains "$out" "$INSTALLATION" "dry-run: the plan names the installation"
  expect_contains "$out" "$REPO" "dry-run"
  expect_contains "$out" "touches" "dry-run: the plan says it touches nothing"
  expect_absent "$out" "CHECK 740 RESULT" "dry-run: a plan is not a result block"
  # Nothing at all was called, no scratch directory was made, and no block was
  # printed: a plan is the whole of a dry run.
  if [ -s "$dir/state/calls" ]; then fail "dry-run: the check made a call: $(head -3 "$dir/state/calls")"; fi
  [ -z "$(ls -A "$dir/tmp" 2>/dev/null || true)" ] || fail "dry-run: a scratch directory was left behind"
}

case_help() {
  setup_case help
  run740 --help
  expect_status "$status" 0 "help"
  expect_contains "$out" "usage: check-740-run-messages.sh" "help"
  if [ -s "$dir/state/calls" ]; then fail "help: the check made a call: $(head -3 "$dir/state/calls")"; fi
}

case_knob_missing_installation() {
  setup_case knob-installation
  RUN740_ENV=(CHECK740_INSTALLATION="$INSTALLATION" CHECK740_REPO="$REPO")
  run740
  expect_status "$status" 0 "knob-installation: the environment carries the same two names"
  expect_fact message-now PASS "knob-installation"
}

# ------------------------------------------------------------------ the runner
# Cases are independent, each in its own directory, so a shard can be taken from
# either side: CHECK_TEST_SHARD="i/n" runs every n-th case starting at i, and
# CHECK_TEST_ONLY=name runs that one case alone. Neither set: all run.
case_index=0
t() {
  local i=$case_index
  case_index=$((case_index + 1))
  if [ -n "${CHECK_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t case_healthy
t case_quiet_point_missed
t case_session_lost
t case_unknown_reason
t case_now_boundary
t case_now_ended_in_implement
t case_now_ended_later
t case_now_undelivered
t case_plain_undelivered
t case_plain_reached_reviewer
t case_plain_refused
t case_record_of_another_run
t case_record_not_accepting
t case_record_unreadable
t case_iam_granted
t case_iam_unknown
t case_iam_conditional_writer
t case_iam_writer_other_member
t case_no_project_job
t case_job_unreadable
t case_lifecycle_absent
t case_lifecycle_selects_token
t case_lifecycle_not_delete
t case_bucket_unreadable
t case_release_too_old
t case_release_no_flags
t case_release_dev_build
t case_belong_installation
t case_belong_repo
t case_belong_project
t case_name_mismatch
t case_doctor_refused
t case_no_implement
t case_send_refused
t case_second_send_refused
t case_run_never_ended
t case_launch_failed
t case_launch_nameless
t case_agent_session
t case_editor_terminal_is_allowed
t case_xtrace
t case_missing_tools
t case_no_args
t case_repo_needs_a_slash
t case_troubleshooter_alpha
t case_troubleshooter_silent
t case_troubleshooter_disabled
t case_unknown_argument
t case_dry_run
t case_help
t case_knob_missing_installation

if [ "$failures" -gt 0 ]; then
  echo "$failures case(s) failed" >&2
  exit 1
fi
echo "check-740-run-messages_test.sh: all cases passed"
