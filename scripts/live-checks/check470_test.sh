#!/usr/bin/env bash
# Tests for scripts/live-checks/check470.sh, against a fake `fugaro` and a fake
# `gcloud` on PATH: nothing here reaches the network, there is no cloud project
# and no bucket, no credential is read, made or printed, and no live step of any
# check is run.
#
# The two fakes model the one thing this check is about. The fake `fugaro` holds
# a project's store it either reads or cannot reach (the network knob), the three
# sources `mine` answers from (`shared`, `snapshot`, `local`), the snapshot file
# the real CLI writes on a successful read — so the offline mode's second reading
# is about a file that really is or is not there — and it writes the two cache
# files the check's Restore is about, in the two places the real CLI writes them.
# The fake `gcloud` holds the store's three prefixes and refuses them on a knob,
# with gcloud's own two shapes of failure: a permission denial (the reading U1
# asks for) and a call that did not answer (which must stop the run rather than
# be recorded as one). Both record every call, with the directory it was made in,
# before anything else, so a dry run that made no call at all is provable rather
# than argued, and so a case can say the CLI really ran in the checkout.
#
# Usage: bash scripts/live-checks/check470_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check470.sh
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK470_TEST_KEEP:-}" ]; then
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
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT (aborted|restore):' | sed 's/^/ ... last run: /')"
}
expect_file() { # path want what
  local got
  got=$(cat "$1" 2>/dev/null || true)
  [ "$got" = "$2" ] || fail "$3: expected $1 to hold '$2', it holds '$got'"
}
expect_no_file() { # path what
  [ -e "$1" ] && fail "$2: $1 should not exist"
  return 0
}

# The project and bucket a case runs against. Neither names `belong`, nor either
# of the two other installations this check refuses.
PROJECT=prj-sandbox-a
BUCKET=bkt-sandbox-a

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin" "$1/state"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake `fugaro` for check470.sh's test: the one command the check runs, in the
# four states the check has to tell apart.
#
#   * the network is ON and the store answers: the run's source is `shared` (or
#     whatever the case's `source` file says, which is how a run that answered
#     from the snapshot or from the local files alone is modelled), the snapshot
#     is written the way the real CLI writes it, and the two cache files land
#     where the real CLI puts them;
#   * the network is OFF: the snapshot stands in when it is there (the note the
#     real CLI prints, with its own as-of time) and the local files alone
#     otherwise (that note too, word for word);
#   * the command fails: an exit status the case names, and a sentence on
#     stderr.
#
# The JSON document is written the way Go's encoder writes it — two spaces of
# indent, top-level members at 2, row members at 6 — because the check reads it
# with sed by that indent, and a fake that printed another shape would test the
# reader against a document the build never prints.
set -euo pipefail
dir=${FAKE470_DIR:?FAKE470_DIR is not set}
cache=${FAKE470_CACHE:?FAKE470_CACHE is not set}
printf 'fugaro %s (cwd=%s)\n' "$*" "$PWD" >>"$dir/calls"

[ "${1:-}" = issue ] || { echo "fake fugaro: unexpected call: $*" >&2; exit 64; }
[ "${2:-}" = mine ] || { echo "fake fugaro: unexpected call: $*" >&2; exit 64; }
json=no
for arg in "$@"; do
  [ "$arg" = --json ] && json=yes
done

knob() { # NAME DEFAULT
  if [ -f "$dir/$1" ]; then cat "$dir/$1"; else printf '%s' "$2"; fi
}
network=$(knob network on)
# `network-after-first` is the owner turning the network off between the check's
# first run and the ones that follow it, which is what the offline mode's three
# calls really are: the switch is thrown while the script is waiting at its
# prompt, and the call this fake is answering is the one after it.
if [ -f "$dir/network-after-first" ] && [ "$(grep -c '^fugaro ' "$dir/calls")" -ge 2 ]; then
  network=off
fi
source=$(knob source shared)
rows=$(knob rows 2)
hidden=$(knob hidden 0)
truncated=$(knob truncated false)
format=$(knob format 1)
exit_status=$(knob exit 0)

if [ -n "${FAKE470_SLEEP:-}" ]; then sleep "$FAKE470_SLEEP"; fi
if [ "$exit_status" != 0 ]; then
  echo "fake fugaro: the listing failed (exit $exit_status)" >&2
  exit "$exit_status"
fi

note=""
if [ "$network" = off ]; then
  if [ -f "$cache/mine/inst-a.json" ]; then
    note="offline: shared state as of 2026-11-01T00:00:00Z"
    source=snapshot
  else
    note="no snapshot of the project's issues is cached, so these are this machine's own local issues only"
    source=local
  fi
fi

if [ "$source" = shared ]; then
  # Tolerated rather than fatal: one case puts something that is not a regular
  # file where the identity cache goes, and what it is about is the check's own
  # Restore rather than this fake's write.
  mkdir -p "$cache/mine" 2>/dev/null || true
  printf '{"format":1,"as_of":"2026-11-01T00:00:00Z","rows":%s}\n' "$rows" >"$cache/mine/inst-a.json" 2>/dev/null || true
  printf 'launcher@sandbox-a.example\n' >"$cache/identity.json" 2>/dev/null || true
fi

rows_out() {
  local i=1 first=yes
  while [ "$i" -le "$rows" ]; do
    if [ "$first" = yes ]; then first=no; else printf ',\n'; fi
    printf '    {\n      "number": %d,\n      "summary": "row %d"\n    }' "$((1000 + i))" "$i"
    i=$((i + 1))
  done
}

if [ "$json" = yes ]; then
  if [ -n "$note" ]; then echo "$note" >&2; fi
  printf '{\n  "format": %s,\n  "source": "%s",\n  "as_of": "2026-11-01T00:00:00Z",\n  "installed": "",\n  "hidden": %s,\n  "truncated": %s,\n  "issues": [\n' \
    "$format" "$source" "$hidden" "$truncated"
  rows_out
  printf '\n  ]\n}\n'
else
  if [ -n "$note" ]; then echo "$note"; fi
  i=1
  while [ "$i" -le "$rows" ]; do
    printf '#%04d row %d\n' "$((1000 + i))" "$i"
    i=$((i + 1))
  done
  if [ "$truncated" = true ]; then echo "the project's listing was cut short, so some issues may be missing."; fi
fi
FUGARO_EOF
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake gcloud for check470.sh's test: the store's three prefixes, listed and
# read the way the check reads them, and the two shapes of failure a case needs
# to tell apart — a permission denial (a reading) and a call that did not answer
# (a stop). Every call is recorded with its whole command line first.
set -euo pipefail
dir=${FAKE470_DIR:?FAKE470_DIR is not set}
printf 'gcloud %s\n' "$*" >>"$dir/calls"

[ "${1:-}" = storage ] || { echo "fake gcloud: unexpected call: $*" >&2; exit 64; }
verb=${2:-}
shift 2 || true
target=""
for arg in "$@"; do
  case "$arg" in gs://*) target=$arg ;; esac
done
[ -n "$target" ] || { echo "fake gcloud: no gs:// target in: $*" >&2; exit 64; }
bucket=${target#gs://}
bucket=${bucket%%/*}
rest=${target#gs://"$bucket"}
rest=${rest#/}

deny() { # permission
  printf 'ERROR: (gcloud.storage) HTTPError 403: %s does not have %s access to the Google Cloud Storage object.\n' \
    "launcher@sandbox-a.example" "$1" >&2
  exit 1
}
did_not_answer() { # what
  printf 'ERROR: (gcloud.storage) HTTPError 500: Backend Error (%s)\n' "$1" >&2
  exit 1
}
no_match() {
  printf 'ERROR: (gcloud.storage) One or more URLs matched no objects.\n' >&2
  exit 1
}
knob() { # NAME
  [ -f "$dir/$1" ]
}

case "$verb:$rest" in
ls:issues/reporters/)
  knob error-reporters && did_not_answer "listing $rest"
  knob deny-reporters && deny storage.objects.list
  ;;
ls:issues/occurrences/)
  knob error-occurrences && did_not_answer "listing $rest"
  knob deny-occurrences && deny storage.objects.list
  ;;
ls:issues/records/)
  knob deny-records && deny storage.objects.list
  ;;
cat:issues/records/*)
  knob deny-record-read && deny storage.objects.get
  ;;
*)
  echo "fake gcloud: unexpected call: $*" >&2
  exit 64
  ;;
esac

matched=$(grep -c "^$rest" "$dir/objects" || true)
if [ "${matched:-0}" -eq 0 ]; then no_match; fi
case "$verb" in
ls)
  grep "^$rest" "$dir/objects" | while read -r name; do printf 'gs://%s/%s\n' "$bucket" "$name"; done
  ;;
cat)
  printf '{"number": %s, "summary": "a record the store holds"}\n' "${rest##*/}"
  ;;
esac
GCLOUD_EOF
  chmod +x "$1/bin/fugaro" "$1/bin/gcloud"
}

# ------------------------------------------------------------------- the cases
# setup_case NAME — a fresh case: the two fakes, a checkout carrying a
# fugaro.yaml, a project's store with one reporter's index, one occurrence and
# one record, a network that is on, and a cache with neither of the two files
# the check's Restore is about.
setup_case() {
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp" "$dir/home" "$dir/cache" "$dir/checkout"
  write_fakes "$dir"
  : >"$dir/state/calls"
  : >"$dir/state/network-after-first"
  printf 'project: %s\n' "$PROJECT" >"$dir/checkout/fugaro.yaml"
  cat >"$dir/state/objects" <<'OBJECTS'
issues/reporters/aaaa/0001
issues/reporters/aaaa/0002
issues/occurrences/0001/aaaa/2026-11-01T00:00:00Z
issues/records/1001
OBJECTS
  status=0
  out=""
  RUN470_ARGS=()
  RUN470_ENV=()
  RUN470_STDIN=""
}

# run470 [ARGS...] — runs the check on the case at hand. RUN470_ARGS adds
# arguments, RUN470_ENV names environment variables (NAME=value), and
# RUN470_STDIN points the run's stdin at a file (a case about the offline mode's
# prompt answers it that way; the default is /dev/null, which is how the Go
# driver runs every script, so the prompt path is exercised with no one there).
run470() {
  local stdin=${RUN470_STDIN:-/dev/null}
  status=0
  out=$(env TMPDIR="$dir/tmp" HOME="$dir/home" XDG_CACHE_HOME="$dir/cache" \
    FAKE470_DIR="$dir/state" FAKE470_CACHE="$dir/cache/fugaro" \
    PATH="$dir/bin:$PATH" \
    ${RUN470_ENV[@]+"${RUN470_ENV[@]}"} \
    bash "$script" ${RUN470_ARGS[@]+"${RUN470_ARGS[@]}"} "$@" 2>"$dir/err" <"$stdin") || status=$?
  RUN470_ARGS=()
  RUN470_ENV=()
  RUN470_STDIN=""
}
run470_default() { run470 --checkout "$dir/checkout" --project "$PROJECT" --bucket "$BUCKET" "$@"; }

expect_block_keys() { # what mode
  local key keys
  case "$2" in
  470) keys=(mode project bucket checkout guard mine-online listing-reporters listing-occurrences record-read human-steps restore) ;;
  471) keys=(mode project bucket checkout guard online offline-snapshot cache-cleared restore) ;;
  472) keys=(mode project bucket checkout guard mine-json row-count truncated restore) ;;
  esac
  expect_contains "$out" "CHECK $2 RESULT" "$1: the block"
  for key in "${keys[@]}"; do
    expect_contains "$out" "FACT $key:" "$1: the $key row"
  done
}
expect_no_calls() { # what
  if [ -s "$dir/state/calls" ]; then
    fail "$1: a call was made: $(tr '\n' ';' <"$dir/state/calls")"
  fi
}
fact_line() { # key
  printf '%s\n' "$out" | grep -m1 "^FACT $1:" || true
}
# expect_calls WHAT — the calls this run made, in order, for a case that has to
# say what the check read and in which directory it read it.
calls() { cat "$dir/state/calls"; }
expect_scratch_gone() { # what
  local scratch
  scratch=$(sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  if [ -z "$scratch" ]; then
    fail "$1: the script did not say where its scratch directory is"
    return 0
  fi
  [ -d "$scratch" ] && fail "$1: the scratch directory $scratch survived the run"
  [ -z "$(find "$dir/tmp" -mindepth 1 2>/dev/null)" ] || fail "$1: files were left in TMPDIR: $(find "$dir/tmp" -mindepth 1)"
  return 0
}

# ------------------------------------------------------------------- the tests

test_the_shared_lookup_holds() {
  setup_case shared_holds
  run470_default --mode 470
  expect_status "$status" 0 "shared lookup"
  expect_block_keys "shared lookup" 470
  expect_contains "$out" "FACT mine-online: PASS" "shared lookup: the listing read the store"
  expect_contains "$out" "'source: shared'" "shared lookup: the source the row quotes"
  expect_contains "$out" "FACT listing-reporters: PASS" "shared lookup: reporters may be listed"
  expect_contains "$out" "FACT listing-occurrences: PASS" "shared lookup: occurrences may be listed"
  expect_contains "$out" "FACT record-read: PASS" "shared lookup: a record may be read"
  expect_contains "$out" "gs://$BUCKET/issues/records/1001" "shared lookup: the record the row names is the first row's own number"
  expect_contains "$out" "FACT restore: PASS" "shared lookup: the restore"
  expect_contains "$out" "neither cache file existed before this run and neither exists now" "shared lookup: what the restore found"
  expect_contains "$(calls)" "fugaro issue mine --json (cwd=$dir/checkout)" "shared lookup: the CLI ran in the checkout"
  expect_contains "$(calls)" "gcloud storage ls --project=$PROJECT gs://$BUCKET/issues/reporters/" "shared lookup: the reporters listing"
  expect_absent "$out" "FACT aborted:" "shared lookup: no abort row"
  expect_scratch_gone "shared lookup"
}

test_a_mine_that_answered_from_the_snapshot_is_a_fail() {
  setup_case answered_from_snapshot
  printf 'snapshot\n' >"$dir/state/source"
  run470_default --mode 470
  expect_status "$status" 1 "a listing that answered from the snapshot"
  expect_contains "$(fact_line mine-online)" "FACT mine-online: FAIL" "a listing that answered from the snapshot: the row fails"
  expect_contains "$(fact_line mine-online)" "rather than the project's store" "a listing that answered from the snapshot: and says what it read"
}

test_a_mine_that_failed_is_a_fail_and_the_store_is_still_read() {
  setup_case mine_failed
  printf '7\n' >"$dir/state/exit"
  run470_default --mode 470
  expect_status "$status" 1 "a listing that failed"
  expect_contains "$(fact_line mine-online)" "FACT mine-online: FAIL — \`fugaro issue mine --json\` exited 7" "a listing that failed: the row quotes the status"
  expect_contains "$(fact_line mine-online)" "the listing failed (exit 7)" "a listing that failed: and the command's own words"
  expect_contains "$out" "FACT listing-reporters: PASS" "a listing that failed: the store is still read, because the permission is a separate question"
}

test_a_document_that_is_not_format_one_is_a_fail() {
  setup_case format_two
  printf '2\n' >"$dir/state/format"
  run470_default --mode 470
  expect_status "$status" 1 "a document of another format"
  expect_contains "$(fact_line mine-online)" "format is '2' rather than 1" "a document of another format: the row names it"
}

test_a_refused_reporter_listing_is_the_reading() {
  setup_case refused_reporters
  : >"$dir/state/deny-reporters"
  run470_default --mode 470
  expect_status "$status" 1 "a refused reporters listing"
  expect_contains "$(fact_line listing-reporters)" "FACT listing-reporters: FAIL" "a refused reporters listing: the row fails"
  expect_contains "$(fact_line listing-reporters)" "may NOT list gs://$BUCKET/issues/reporters/" "a refused reporters listing: and names the prefix"
  expect_contains "$(fact_line listing-reporters)" "does not have storage.objects.list access" "a refused reporters listing: quoting gcloud's own words"
  expect_contains "$out" "FACT listing-occurrences: PASS" "a refused reporters listing: the other prefix is still read"
  expect_contains "$out" "FACT record-read: PASS" "a refused reporters listing: and so is the record"
}

test_a_refused_occurrence_listing_is_the_reading() {
  setup_case refused_occurrences
  : >"$dir/state/deny-occurrences"
  run470_default --mode 470
  expect_status "$status" 1 "a refused occurrences listing"
  expect_contains "$(fact_line listing-occurrences)" "FACT listing-occurrences: FAIL" "a refused occurrences listing: the row fails"
  expect_contains "$out" "FACT listing-reporters: PASS" "a refused occurrences listing: the other prefix still holds"
}

test_a_refused_record_read_is_the_reading() {
  setup_case refused_record
  : >"$dir/state/deny-record-read"
  run470_default --mode 470
  expect_status "$status" 1 "a refused record read"
  expect_contains "$(fact_line record-read)" "FACT record-read: FAIL" "a refused record read: the row fails"
  expect_contains "$(fact_line record-read)" "storage.objects.get" "a refused record read: and quotes the permission gcloud named"
  expect_contains "$out" "FACT listing-reporters: PASS" "a refused record read: the listings still hold"
}

test_a_listing_that_did_not_answer_stops_the_run() {
  setup_case listing_did_not_answer
  : >"$dir/state/error-reporters"
  run470_default --mode 470
  expect_status "$status" 2 "a listing that did not answer"
  expect_contains "$(fact_line aborted)" "FACT aborted: FAIL" "a listing that did not answer: an abort row"
  expect_contains "$(fact_line aborted)" "did not answer" "a listing that did not answer: and why"
  expect_contains "$(fact_line listing-reporters)" "FAIL — not reached" "a listing that did not answer: the row is not recorded as a refusal"
  expect_contains "$out" "FACT restore: PASS" "a listing that did not answer: the cache is still put back"
  expect_scratch_gone "a listing that did not answer"
}

test_a_store_whose_prefix_holds_nothing_is_not_a_refusal() {
  setup_case empty_prefix
  : >"$dir/state/objects"
  printf '0\n' >"$dir/state/rows"
  run470_default --mode 470
  expect_status "$status" 0 "a store whose prefixes hold nothing"
  expect_contains "$(fact_line listing-reporters)" "FACT listing-reporters: PASS" "a store whose prefixes hold nothing: the listing is permitted"
  expect_contains "$(fact_line listing-reporters)" "the prefix holds no object yet" "a store whose prefixes hold nothing: and reads as empty"
  expect_contains "$(fact_line listing-reporters)" "it is not a refusal" "a store whose prefixes hold nothing: said out loud, because an empty store is not a login that cannot list"
  expect_contains "$(fact_line record-read)" "FACT record-read: INFO" "a store whose prefixes hold nothing: the record row is a diagnostic"
  expect_contains "$(fact_line record-read)" "holds no record" "a store whose prefixes hold nothing: and says why"
}

test_the_human_half_is_printed_and_never_faked() {
  setup_case human_half
  run470_default --mode 470
  expect_status "$status" 0 "the human half"
  expect_contains "$(fact_line human-steps)" "FACT human-steps: INFO" "the human half: the row is a diagnostic and never a gate"
  expect_contains "$(fact_line human-steps)" "SECOND PERSON" "the human half: the second person is named"
  expect_contains "$(fact_line human-steps)" "machine 2" "the human half: and the second machine"
  expect_contains "$(cat "$dir/err")" "1. machine 2, same Google login" "the human half: printed as a numbered step"
  expect_contains "$(cat "$dir/err")" "2. a second person hits the same failure" "the human half: and the second step too"
  expect_contains "$(cat "$dir/err")" "Neither is measured here, and neither is faked" "the human half: said out loud"
  # Three gcloud calls and one fugaro call, and nothing else: the human half
  # makes no call of its own.
  expect_contains "$(calls | grep -c '^gcloud ' | tr -d ' ')" "3" "the human half: three store calls"
  expect_contains "$(calls | grep -c '^fugaro ' | tr -d ' ')" "1" "the human half: one fugaro call"
}

test_the_three_refused_names_are_refused_in_both_places() {
  local name
  for name in belong edge-devel-dimi edgeappinc; do
    setup_case "refused_$name"
    run470 --checkout "$dir/checkout" --project "prj-$name-x" --bucket "$BUCKET" --mode 470
    expect_status "$status" 2 "the project $name"
    expect_contains "$(fact_line guard)" "FACT guard: FAIL" "the project $name: the guard row fails"
    expect_contains "$(fact_line guard)" "$name" "the project $name: and names it"
    expect_no_calls "the project $name"
    setup_case "refused_bucket_$name"
    run470 --checkout "$dir/checkout" --project "$PROJECT" --bucket "bkt-$name-x" --mode 470
    expect_status "$status" 2 "the bucket $name"
    expect_contains "$(fact_line guard)" "FACT guard: FAIL" "the bucket $name: the guard row fails"
    expect_no_calls "the bucket $name"
  done
}

test_a_checkout_without_a_fugaro_yaml_is_refused() {
  setup_case no_manifest
  rm -f "$dir/checkout/fugaro.yaml"
  run470_default --mode 470
  expect_status "$status" 2 "a checkout without a fugaro.yaml"
  expect_contains "$(fact_line checkout)" "FACT checkout: FAIL" "a checkout without a fugaro.yaml: the row fails"
  expect_contains "$(fact_line checkout)" "no project with a runs bucket is selected" "a checkout without a fugaro.yaml: and says what the run would answer"
  expect_no_calls "a checkout without a fugaro.yaml"
}

test_a_checkout_that_is_not_a_directory_is_refused() {
  setup_case no_checkout
  run470 --checkout "$dir/nowhere" --project "$PROJECT" --bucket "$BUCKET" --mode 470
  expect_status "$status" 2 "a checkout that is not a directory"
  expect_contains "$(fact_line checkout)" "FACT checkout: FAIL" "a checkout that is not a directory: the row fails"
  expect_no_calls "a checkout that is not a directory"
}

test_a_dry_run_prints_the_plan_and_makes_no_call() {
  setup_case dry_run
  run470_default --mode 470 --dry-run
  expect_status "$status" 0 "a dry run"
  expect_no_calls "a dry run"
  expect_contains "$(fact_line dry-run)" "FACT dry-run: PASS" "a dry run: the row says what it did"
  expect_contains "$out" "FACT mode: PASS" "a dry run: the mode row"
  expect_contains "$out" "FACT guard: PASS" "a dry run: the guard still runs"
  expect_contains "$out" "FACT checkout: PASS" "a dry run: and the checkout guard too"
  expect_absent "$out" "FACT mine-online:" "a dry run: no reading row, because none was taken"
  expect_absent "$out" "FACT listing-reporters:" "a dry run: and no store row either"
  expect_contains "$(cat "$dir/err")" "dry run: NOTHING below was run" "a dry run: says so"
  expect_contains "$(cat "$dir/err")" "fugaro issue mine --json" "a dry run: the plan names the command"
  expect_contains "$(cat "$dir/err")" "gcloud storage ls --project=$PROJECT gs://$BUCKET/issues/reporters/" "a dry run: and the store calls"
  expect_contains "$(cat "$dir/err")" "the second machine and the second person" "a dry run: the human half is named in the plan and not run"
  expect_absent "$(cat "$dir/err")" "TURN THE NETWORK OFF" "a dry run: another mode's own steps are not in this mode's plan"
  expect_contains "$out" "FACT restore: PASS" "a dry run: the restore row says nothing was created"
  expect_scratch_gone "a dry run"
}

test_a_dry_run_of_the_offline_mode_names_the_network_step() {
  setup_case dry_run_offline
  run470_default --mode 471 --dry-run
  expect_status "$status" 0 "a dry run of the offline mode"
  expect_no_calls "a dry run of the offline mode"
  expect_contains "$(cat "$dir/err")" "TURN THE NETWORK OFF" "a dry run of the offline mode: the network step is in the plan"
  expect_contains "$(cat "$dir/err")" "no snapshot of the project's issues is cached" "a dry run of the offline mode: and so is the reading it must print"
}

test_a_dry_run_still_refuses_belong() {
  setup_case dry_run_belong
  run470 --checkout "$dir/checkout" --project prj-belong --bucket "$BUCKET" --mode 470 --dry-run
  expect_status "$status" 2 "a dry run naming belong"
  expect_contains "$(fact_line guard)" "FACT guard: FAIL" "a dry run naming belong: the guard still runs"
  expect_no_calls "a dry run naming belong"
}

test_the_offline_mode_holds() {
  setup_case offline_holds
  # The cache the machine already had: another installation's snapshot, and the
  # identity cache. Both must be exactly as they were once the run is over.
  mkdir -p "$dir/cache/fugaro/mine"
  printf '{"format":1,"rows":9}\n' >"$dir/cache/fugaro/mine/inst-b.json"
  printf 'the-address-this-machine-had\n' >"$dir/cache/fugaro/identity.json"
  run470_default --mode 471 --no-prompt
  expect_status "$status" 0 "the offline mode"
  expect_block_keys "the offline mode" 471
  expect_contains "$out" "FACT online: PASS" "the offline mode: the online run read the store"
  expect_contains "$out" "FACT offline-snapshot: PASS" "the offline mode: the snapshot stood in"
  expect_contains "$out" "offline: shared state as of 2026-11-01T00:00:00Z" "the offline mode: and the row quotes the line it printed"
  expect_contains "$out" "FACT cache-cleared: PASS" "the offline mode: the cleared cache read as local rows alone"
  expect_contains "$out" "no snapshot of the project's issues is cached" "the offline mode: and the row quotes that line too"
  expect_contains "$out" "FACT restore: PASS" "the offline mode: the restore"
  expect_contains "$out" "both are back byte for byte" "the offline mode: and reads both files back"
  expect_file "$dir/cache/fugaro/identity.json" "the-address-this-machine-had" "the offline mode: the identity cache is as it was"
  expect_file "$dir/cache/fugaro/mine/inst-b.json" '{"format":1,"rows":9}' "the offline mode: the machine's own snapshot is as it was"
  expect_no_file "$dir/cache/fugaro/mine/inst-a.json" "the offline mode: the snapshot this run wrote is gone"
  # The network went off between the two runs, and the third ran with the
  # snapshot cleared: three fugaro calls, no store call at all (mode 471 never
  # reads the bucket directly).
  expect_contains "$(calls | grep -c '^fugaro ' | tr -d ' ')" "3" "the offline mode: three fugaro calls"
  expect_absent "$(calls)" "gcloud " "the offline mode: no gcloud call"
  expect_scratch_gone "the offline mode"
}

test_the_offline_mode_without_a_snapshot_line_is_a_fail() {
  setup_case offline_not_off
  # The network stays ON for every call, which is what a run where the owner
  # never turned it off reads as: the store answers and no offline line appears.
  rm -f "$dir/state/network-after-first"
  run470_default --mode 471 --no-prompt
  expect_status "$status" 1 "an offline run with the network on"
  expect_contains "$(fact_line offline-snapshot)" "FACT offline-snapshot: FAIL" "an offline run with the network on: the row fails"
  expect_contains "$(fact_line offline-snapshot)" "either the network was NOT off" "an offline run with the network on: and says which of the two it was"
  expect_contains "$out" "FACT online: PASS" "an offline run with the network on: the online run still held"
}

test_the_offline_mode_that_left_no_snapshot_is_a_fail() {
  setup_case offline_no_snapshot
  printf 'local\n' >"$dir/state/source"
  run470_default --mode 471 --no-prompt
  expect_status "$status" 1 "an online run that wrote no snapshot"
  expect_contains "$(fact_line online)" "FACT online: FAIL" "an online run that wrote no snapshot: the online row fails"
  expect_contains "$(fact_line cache-cleared)" "FACT cache-cleared: FAIL" "an online run that wrote no snapshot: and the cleared-cache row says the premise is gone"
  expect_contains "$(fact_line cache-cleared)" "left no snapshot under" "an online run that wrote no snapshot: naming the file"
}

test_a_slow_offline_run_is_a_fail() {
  setup_case offline_slow
  RUN470_ENV=(FAKE470_SLEEP=1 CHECK470_OFFLINE_MAX_SECONDS=0)
  RUN470_ARGS=(--checkout "$dir/checkout" --project "$PROJECT" --bucket "$BUCKET" --mode 471 --no-prompt)
  run470
  expect_status "$status" 1 "a slow offline run"
  expect_contains "$(fact_line offline-snapshot)" "FACT offline-snapshot: FAIL" "a slow offline run: the row fails"
  expect_contains "$(fact_line offline-snapshot)" "more than the 0s this check allows" "a slow offline run: naming the bound"
}

test_the_offline_mode_answers_the_prompt_when_someone_is_there() {
  setup_case offline_prompted
  printf '\n\n' >"$dir/tmp/answers"
  RUN470_STDIN="$dir/tmp/answers"
  run470_default --mode 471
  expect_status "$status" 0 "the offline mode with someone at the prompt"
  expect_contains "$(cat "$dir/err")" "TURN THE NETWORK OFF now" "the offline mode with someone at the prompt: it asks"
  expect_contains "$(cat "$dir/err")" "press Enter when it is done" "the offline mode with someone at the prompt: and waits"
  expect_contains "$(cat "$dir/err")" "TURN THE NETWORK BACK ON now" "the offline mode with someone at the prompt: and asks for the network back"
  expect_absent "$(cat "$dir/err")" "not asking (--no-prompt)" "the offline mode with someone at the prompt: no assumption is announced"
}

test_the_listing_at_size_counts_and_times() {
  setup_case size
  printf '3\n' >"$dir/state/rows"
  printf '2\n' >"$dir/state/hidden"
  run470_default --mode 472
  expect_status "$status" 0 "the listing at size"
  expect_block_keys "the listing at size" 472
  expect_contains "$out" "FACT mine-json: PASS" "the listing at size: the document was read"
  expect_contains "$out" "FACT row-count: PASS" "the listing at size: the count was taken"
  expect_contains "$(fact_line row-count)" "3 issue row(s)" "the listing at size: the count"
  expect_contains "$(fact_line row-count)" "2 finished row(s) were hidden" "the listing at size: and the hidden count"
  expect_contains "$(fact_line truncated)" "FACT truncated: PASS — truncated: false" "the listing at size: not cut short"
  expect_contains "$out" "THE LISTING AT SIZE: 3 issue row(s)," "the listing at size: the answer line above the block"
  expect_contains "$out" "truncated: false, hidden: 2, source: shared" "the listing at size: with every number on it"
  expect_contains "$(calls | grep -c '^fugaro ' | tr -d ' ')" "1" "the listing at size: one fugaro call"
  expect_scratch_gone "the listing at size"
}

test_a_listing_that_was_cut_short_says_so() {
  setup_case size_truncated
  printf '500\n' >"$dir/state/rows"
  printf 'true\n' >"$dir/state/truncated"
  run470_default --mode 472
  expect_status "$status" 0 "a listing that was cut short"
  expect_contains "$(fact_line truncated)" "FACT truncated: PASS — truncated: true" "a listing that was cut short: the row says true"
  expect_contains "$(fact_line truncated)" "500-issue cap" "a listing that was cut short: and names the cap U2 has to answer"
  expect_contains "$out" "truncated: true" "a listing that was cut short: the answer line carries it"
}

test_a_listing_that_could_not_be_read_is_a_fail() {
  setup_case size_unreadable
  printf '1\n' >"$dir/state/exit"
  run470_default --mode 472
  expect_status "$status" 1 "a listing that could not be read"
  expect_contains "$(fact_line mine-json)" "FACT mine-json: FAIL — \`fugaro issue mine --json\` exited 1" "a listing that could not be read: the row quotes the status"
  expect_contains "$(fact_line row-count)" "FACT row-count: FAIL" "a listing that could not be read: the count is not taken"
  expect_contains "$(fact_line truncated)" "FACT truncated: FAIL" "a listing that could not be read: and neither is the truncation"
  expect_absent "$out" "THE LISTING AT SIZE" "a listing that could not be read: no answer line"
}

test_the_restore_puts_both_cache_files_back() {
  setup_case restore_back
  mkdir -p "$dir/cache/fugaro/mine"
  printf '{"format":1,"rows":9}\n' >"$dir/cache/fugaro/mine/inst-b.json"
  printf 'the-address-this-machine-had\n' >"$dir/cache/fugaro/identity.json"
  run470_default --mode 472
  expect_status "$status" 0 "a restore over files that were there"
  expect_contains "$(fact_line restore)" "FACT restore: PASS" "a restore over files that were there: the row passes"
  expect_contains "$(fact_line restore)" "both cache files were there before this run and both are back byte for byte" "a restore over files that were there: and says what it read"
  expect_contains "$(fact_line restore)" "NOT undone, and not this check's to undo" "a restore over files that were there: and says what it does not cover"
  expect_file "$dir/cache/fugaro/identity.json" "the-address-this-machine-had" "a restore over files that were there: the identity cache"
  expect_file "$dir/cache/fugaro/mine/inst-b.json" '{"format":1,"rows":9}' "a restore over files that were there: the machine's own snapshot"
  expect_no_file "$dir/cache/fugaro/mine/inst-a.json" "a restore over files that were there: the snapshot this run wrote"
}

test_the_restore_removes_the_identity_cache_the_run_created() {
  setup_case restore_removes
  run470_default --mode 472
  expect_status "$status" 0 "a machine with no cache at all"
  expect_contains "$(fact_line restore)" "neither cache file existed before this run and neither exists now" "a machine with no cache at all: the row says so"
  expect_no_file "$dir/cache/fugaro/mine" "a machine with no cache at all: no snapshot directory is left behind"
  expect_no_file "$dir/cache/fugaro/identity.json" "a machine with no cache at all: and no identity cache either"
}

test_a_restore_that_cannot_put_the_cache_back_is_a_fail() {
  setup_case restore_refused
  # Something that is not the CLI's cache file holds the name the CLI writes to:
  # the run cannot file its identity cache there and the Restore cannot put back
  # what it never had, so the row has to fail rather than read as "as it was".
  # A mode that root ignores (a read-only directory) would make this vacuous,
  # which is why the case does not use one.
  mkdir -p "$dir/cache/fugaro/identity.json"
  run470_default --mode 472
  expect_status "$status" 1 "a restore that cannot put the cache back"
  expect_contains "$(fact_line restore)" "FACT restore: FAIL" "a restore that cannot put the cache back: the row fails"
  expect_contains "$(fact_line restore)" "did not hold a regular file when this run started" "a restore that cannot put the cache back: and says what it found"
  expect_contains "$(fact_line restore)" "Put it back by hand" "a restore that cannot put the cache back: and says what to do"
  expect_contains "$out" "CHECK 472 RESULT END (exit 1)" "a restore that cannot put the cache back: the block ends on the failure"
}

test_usage_and_arguments() {
  setup_case usage
  run470 --mode 470
  expect_status "$status" 2 "no arguments"
  expect_contains "$(cat "$dir/err")" "--checkout is required" "no arguments: the checkout is named"
  run470 --checkout "$dir/checkout" --bucket "$BUCKET" --mode 470
  expect_status "$status" 2 "no project"
  expect_contains "$(cat "$dir/err")" "--project is required" "no project: it is named"
  run470 --checkout "$dir/checkout" --project "$PROJECT" --mode 470
  expect_status "$status" 2 "no bucket"
  expect_contains "$(cat "$dir/err")" "--bucket is required" "no bucket: it is named"
  run470_default --mode 469
  expect_status "$status" 2 "an unknown mode"
  expect_contains "$(cat "$dir/err")" "has to be 470, 471 or 472" "an unknown mode: the three are named"
  run470_default --mode 473
  expect_status "$status" 2 "the mode above the three"
  run470_default --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$(cat "$dir/err")" "unknown argument '--nonsense'" "an unknown argument: it is named"
  run470_default --mode
  expect_status "$status" 2 "a mode with no value"
  expect_contains "$(cat "$dir/err")" "--mode needs a number" "a mode with no value: it is named"
  run470 --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "usage: check470.sh" "--help: the usage is printed"
  expect_absent "$out" "CHECK 470 RESULT" "--help: and no block, because nothing was measured"
  expect_no_calls "the usage cases"
}

test_a_gs_prefix_and_a_trailing_slash_are_accepted() {
  setup_case gs_prefix
  run470 --checkout "$dir/checkout" --project "$PROJECT" --bucket "gs://$BUCKET/" --mode 470
  expect_status "$status" 0 "a gs:// prefix with a trailing slash"
  expect_contains "$(calls)" "gs://$BUCKET/issues/reporters/" "a gs:// prefix with a trailing slash: the call is well formed"
  expect_contains "$out" "FACT bucket: PASS — $BUCKET" "a gs:// prefix with a trailing slash: the bucket row names the bare name"
}

test_the_block_carries_every_row_whatever_happened() {
  setup_case block_shape
  printf '3\n' >"$dir/state/exit"
  : >"$dir/state/error-occurrences"
  run470_default --mode 470
  expect_status "$status" 2 "a run that stopped"
  expect_block_keys "a run that stopped" 470
  expect_contains "$(fact_line aborted)" "FACT aborted: FAIL" "a run that stopped: the abort row"
  expect_contains "$(fact_line record-read)" "FAIL — not reached" "a run that stopped: the rows past it are not reached"
  expect_contains "$out" "CHECK 470 RESULT END (exit 2)" "a run that stopped: the block ends with the status"
}

# Sharding, so the package's Go test can run the tests of this file side by
# side: CHECK_TEST_SHARD="i/n" runs every n-th test starting at i, and
# CHECK_TEST_ONLY=name runs that one test alone. Neither set: all run.
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

t test_the_shared_lookup_holds
t test_a_mine_that_answered_from_the_snapshot_is_a_fail
t test_a_mine_that_failed_is_a_fail_and_the_store_is_still_read
t test_a_document_that_is_not_format_one_is_a_fail
t test_a_refused_reporter_listing_is_the_reading
t test_a_refused_occurrence_listing_is_the_reading
t test_a_refused_record_read_is_the_reading
t test_a_listing_that_did_not_answer_stops_the_run
t test_a_store_whose_prefix_holds_nothing_is_not_a_refusal
t test_the_human_half_is_printed_and_never_faked
t test_the_three_refused_names_are_refused_in_both_places
t test_a_checkout_without_a_fugaro_yaml_is_refused
t test_a_checkout_that_is_not_a_directory_is_refused
t test_a_dry_run_prints_the_plan_and_makes_no_call
t test_a_dry_run_of_the_offline_mode_names_the_network_step
t test_a_dry_run_still_refuses_belong
t test_the_offline_mode_holds
t test_the_offline_mode_without_a_snapshot_line_is_a_fail
t test_the_offline_mode_that_left_no_snapshot_is_a_fail
t test_a_slow_offline_run_is_a_fail
t test_the_offline_mode_answers_the_prompt_when_someone_is_there
t test_the_listing_at_size_counts_and_times
t test_a_listing_that_was_cut_short_says_so
t test_a_listing_that_could_not_be_read_is_a_fail
t test_the_restore_puts_both_cache_files_back
t test_the_restore_removes_the_identity_cache_the_run_created
t test_a_restore_that_cannot_put_the_cache_back_is_a_fail
t test_usage_and_arguments
t test_a_gs_prefix_and_a_trailing_slash_are_accepted
t test_the_block_carries_every_row_whatever_happened

if [ "$failures" -ne 0 ]; then
  echo "check470_test: $failures failure(s)" >&2
  exit 1
fi
echo "check470_test: all tests passed" >&2
