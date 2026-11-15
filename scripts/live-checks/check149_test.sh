#!/usr/bin/env bash
# Tests for scripts/live-checks/check149.sh, against a fake `fugaro` alone:
# nothing here reaches the network, there is no cloud project, no credential of
# anybody's is used and no console window is opened. The fake answers the two
# commands the check reads — `config list --json` and `coordinator status
# --from-store --all --json` — out of the case's own files, and logs every call,
# so a case can say what the check read and what it did not.
#
# Usage: bash scripts/live-checks/check149_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check149.sh
# The bash this suite runs the check with, and the timeout that bounds every run
# it starts. Both are resolved here, before any case narrows PATH: the case about
# a missing tool gives the run a PATH with one tool on it, and a bound that is
# itself missing from that PATH would fail the case for the wrong reason.
bash_bin=$(command -v bash)
timeout_bin=$(command -v timeout)
# The PHYSICAL path, so a path this test compares and a path a child reports are
# spelled the same way.
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK149_TEST_KEEP:-}" ]; then
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 700)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  local hint=""
  [ "$1" = 124 ] && hint=" (the run hit this suite's own timeout)"
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$hint$(printf '%s\n' "${out:-}" | grep -E '^FACT ' | head -n 4 | sed 's/^/ ... last run: /')"
}

# The sandbox the whole suite runs against, and the GCP project the register's
# Preconditions would name for it. Never the owner's own installation.
NAME=prj-sandbox-a
GCP=my-fugaro-dev
REGION=us-east5
REPO=acme/legacy
REPO2=acme/second

# Every credential-shaped value in this suite is a sentinel that says so. The
# one the fake puts in the stored view is shaped like a Google API key, because
# that is the shape a document could carry by accident and the reader has to
# recognise it as one; the one it puts in the command's own message is shaped
# like a bearer token. Neither may reach the block, and neither may survive in a
# file the run left behind.
APIKEY_SENTINEL=AIzaSENTINEL149mustnotbeprinted000000
BEARER_SENTINEL=BEARER-SENTINEL-149-must-not-be-printed

# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(project sandbox stored-view json-keys no-credential
  console-renders-stored-view xtrace restore)

# The arguments a case gives the check, which run149 empties between runs and
# default_args fills. Declared here so `${#RUN149_ARGS[@]}` is never an unset
# array. RUN149_LIVE is deliberately NOT declared: run149 substitutes the live
# project the register names unless a case set the variable itself, and a
# variable declared empty here would be set-but-empty for every case at once.
RUN149_ARGS=()
RUN149_ENV=()
RUN149_STDIN=""

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin" "$1/state"
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check149.sh's test: it answers the check's two commands out
# of $FAKE149_DIR, logs every call, and reaches nothing. A verb it does not know
# is a failure, so a case about the read never being made is about the check and
# not about a fake that quietly answered something else.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE149_DIR/calls"
case "$1 ${2:-}" in
"config list")
  if [ -s "$FAKE149_DIR/config.err" ]; then cat "$FAKE149_DIR/config.err" >&2; fi
  if [ -f "$FAKE149_DIR/configs.json" ]; then cat "$FAKE149_DIR/configs.json"; fi
  exit "$(cat "$FAKE149_DIR/config.status" 2>/dev/null || echo 0)"
  ;;
"coordinator status")
  if [ -s "$FAKE149_DIR/read.err" ]; then cat "$FAKE149_DIR/read.err" >&2; fi
  if [ -f "$FAKE149_DIR/view.json" ]; then cat "$FAKE149_DIR/view.json"; fi
  exit "$(cat "$FAKE149_DIR/read.status" 2>/dev/null || echo 0)"
  ;;
*)
  printf 'fake fugaro: unexpected command %s\n' "$*" >&2
  exit 64
  ;;
esac
FUGARO_EOF
  chmod +x "$1/bin/fugaro"
}

# write_configs DIR NAME GCP REGION [SECOND-NAME SECOND-GCP] — this machine's own
# `fugaro config list --json`, with one installation and, where a case needs a
# second, two. Only the fields the check reads are carried.
write_configs() {
  local dir=$1 name=$2 gcp=$3 region=$4 extra=${5:-} extra_gcp=${6:-}
  {
    printf '{"installations":['
    printf '{"name":"%s","gcp_project":"%s","region":"%s","shared_merged":false,"shared_checked_at":null,"base_branch":"main","checkouts":[]}' \
      "$name" "$gcp" "$region"
    if [ -n "$extra" ]; then
      printf ',{"name":"%s","gcp_project":"%s","region":"%s","shared_merged":false,"shared_checked_at":null,"base_branch":"main","checkouts":[]}' \
        "$extra" "$extra_gcp" "$region"
    fi
    printf '],"warnings":[]}\n'
  } >"$dir/state/configs.json"
}

# write_view DIR — the stored view document the read answers with: two
# repositories the sweep has reconciled, one record with a pull request and a run
# behind it, a collision, an unread pull request and a stale marker on the first.
write_view() { # dir
  python3 - "$1/state/view.json" "$REPO" "$REPO2" <<'PY'
import json, sys
path, first, second = sys.argv[1], sys.argv[2], sys.argv[3]
doc = {
    "repos": [
        {
            "repo": first,
            "provider": "bitbucket",
            "stored": True,
            "view": {
                "repo": first,
                "records": [
                    {
                        "pr": {
                            "number": 12,
                            "url": "https://example.invalid/%s/12" % first,
                            "title": "a pull request somebody opened",
                            "branch": "feat/one",
                            "draft": False,
                            "state": "open",
                        },
                        "runs": [
                            {
                                "id": "20261113-170459-26ef",
                                "pr": 12,
                                "status": "succeeded",
                                "outcome": "ready",
                                "terminal": True,
                                "branch": "feat/one",
                                "base_branch": "main",
                                "declared_by": "someone@example.invalid",
                                "files": ["internal/cli/api.go"],
                                "files_truncated": False,
                            }
                        ],
                        "branch": "feat/one",
                        "release": "",
                        "in_flight": True,
                        "files": ["internal/cli/api.go"],
                        "services": ["cli"],
                    }
                ],
                "collisions": [
                    {
                        "a": "#12",
                        "b": "20261113-170459-26ef",
                        "files": ["internal/cli/api.go"],
                        "services": ["cli"],
                    }
                ],
                "unread": [
                    {
                        "number": 13,
                        "reason": "the host did not answer for this pull request",
                        "carried": True,
                        "failures": 2,
                        "retry_after": "2026-11-13T18:00:00Z",
                    }
                ],
                "omitted": 0,
                "omitted_collisions": 0,
            },
            "stale": {
                "since": "2026-11-13T17:00:00Z",
                "reason": "the sweep could not keep this view current",
            },
        },
        {
            "repo": second,
            "provider": "github",
            "stored": True,
            "view": {"repo": second, "records": [], "collisions": []},
        },
    ]
}
open(path, "w").write(json.dumps(doc))
PY
}

# edit_view DIR CODE — applies one small python statement to the case's stored
# view document, so a case about a broken shape writes only what it breaks.
edit_view() {
  python3 - "$1/state/view.json" "$2" <<'PY'
import json, sys
ns = {"json": json, "doc": json.load(open(sys.argv[1]))}
exec(sys.argv[2], ns)
open(sys.argv[1], "w").write(json.dumps(ns["doc"]))
PY
}

setup_case() {
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp"
  write_fakes "$dir"
  : >"$dir/state/calls"
  write_configs "$dir" "$NAME" "$GCP" "$REGION"
  write_view "$dir"
  assert_fakes_first "$dir" "the case at hand"
}

# assert_fakes_first DIR WHAT — the tool the check would READ with is this
# suite's fake. Everything here runs on a machine that has the real one, and a
# run that reached it would read a real project's database.
assert_fakes_first() {
  local dir=$1 what=$2 resolved
  resolved=$(PATH="$dir/bin:$PATH" type -P fugaro)
  [ "$resolved" = "$dir/bin/fugaro" ] \
    || fail "$what: fugaro resolves to '$resolved' rather than this case's fake — the fake has to be first on PATH"
}

# answers_for DIR — the stream the check reads the console step's answer from.
# RUN149_STDIN names a file the case wrote; otherwise stdin closes at once,
# which is the case about an unanswered prompt.
answers_for() {
  if [ -n "$RUN149_STDIN" ]; then cat "$RUN149_STDIN"; fi
}

# run149 [ARGS...] — runs the check on the case at hand. RUN149_ARGS adds
# arguments, RUN149_ENV names environment variables (NAME=value), RUN149_STDIN is
# a file of answers and RUN149_LIVE the live-test project the register's
# Preconditions would export — a case that sets it to the empty string runs with
# nothing exported at all, which the guard treats as not set. The fake is first
# on PATH.
#
# Every run is bounded by `timeout`, so a script that waited for an answer nobody
# gives ends this suite's own way (exit 124) rather than hanging it: the Go test
# that drives this file gives each shard twelve minutes and would report the hang
# as the script's, and a suite that has to be killed is worse than one that
# fails.
run149() {
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE149_DIR="$dir/state" \
    FUGARO_LIVE_GCP_PROJECT="${RUN149_LIVE-$GCP}" \
    PATH="$dir/bin:$PATH" \
    ${RUN149_ENV[@]+"${RUN149_ENV[@]}"} \
    "$timeout_bin" 60 "$bash_bin" "$script" ${RUN149_ARGS[@]+"${RUN149_ARGS[@]}"} "$@" \
    2>"$dir/err" < <(answers_for "$dir")) || status=$?
  # The block is kept as well as captured, so a case kept for inspection has the
  # whole of what the check printed and not only the first line of each failure.
  printf '%s\n' "$out" >"$dir/out"
  RUN149_ARGS=()
  RUN149_ENV=()
  RUN149_STDIN=""
  unset RUN149_LIVE
}

# default_args: the arguments every case gives the check, which are the sandbox
# installation and --console-ok. A case that is about one of them sets
# RUN149_ARGS itself before calling this, and then this leaves it alone.
default_args() {
  if [ ${#RUN149_ARGS[@]} -gt 0 ]; then return 0; fi
  RUN149_ARGS=(--project "$NAME" --console-ok)
}

# ------------------------------------------------------------- the assertions
# assert_no_secret DIR WHAT — nothing the run printed, and nothing it left in its
# own temporary directory, carries a sentinel. The fixture this TEST wrote is its
# own input file, and it is the only file a sentinel may be in.
assert_no_secret() {
  local dir=$1 what=$2 sentinel found
  for sentinel in "$APIKEY_SENTINEL" "$BEARER_SENTINEL"; do
    expect_absent "$(cat "$dir/out" "$dir/err" 2>/dev/null || true)" "$sentinel" "$what: a credential in the block"
  done
  found=$(grep -rlE 'SENTINEL-?149' "$dir/tmp" 2>/dev/null || true)
  [ -n "$found" ] && fail "$what: a credential survived in $found"
  return 0
}

expect_block_keys() { # out what
  local key
  expect_contains "$1" "CHECK 149 RESULT" "$2: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$1" "FACT $key:" "$2: the $key row"
  done
}

# expect_argv DIR LINE WHAT — the read was made with exactly this command line.
expect_argv() { # dir line what
  grep -qxF "$2" "$1/state/calls" \
    || fail "$3: the check never ran '$2': $(tr '\n' ';' <"$1/state/calls")"
}

# expect_no_read DIR WHAT — the check never made the read it exists for.
expect_no_read() { # dir what
  grep -q '^fugaro coordinator status' "$1/state/calls" \
    && fail "$2: the read was made anyway: $(tr '\n' ';' <"$1/state/calls")" || true
}

# expect_calls DIR N WHAT — the fake's log carries exactly N calls. The count is
# what says a guard got in BEFORE anything was read, and not merely that
# something went wrong later.
expect_calls() { # dir n what
  local got
  got=$(grep -c . "$1/state/calls" 2>/dev/null || true)
  [ "${got:-0}" = "$2" ] \
    || fail "$3: expected $2 call(s), got ${got:-0}: $(tr '\n' ';' <"$1/state/calls")"
}

scratch_of() { # dir — the scratch directory the run announced
  sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$1/err" | head -n 1
}

expect_scratch_gone() { # dir what
  local scratch
  scratch=$(scratch_of "$1")
  if [ -z "$scratch" ]; then
    fail "$2: the script did not say where its scratch directory is"
    return 0
  fi
  [ -d "$scratch" ] && fail "$2: the scratch directory $scratch survived the run"
  [ -z "$(find "$1/tmp" -mindepth 1 2>/dev/null)" ] || fail "$2: files were left in TMPDIR: $(find "$1/tmp" -mindepth 1)"
  return 0
}

# ------------------------------------------------------------------- the tests
test_happy_path() {
  setup_case happy
  default_args
  run149
  expect_status "$status" 0 "the happy path"
  expect_block_keys "$out" "the happy path"
  expect_contains "$out" "FACT project: PASS" "the happy path: the name"
  expect_contains "$out" "FACT sandbox: PASS" "the happy path: the sandbox"
  expect_contains "$out" "FACT stored-view: PASS" "the happy path: the stored view"
  expect_contains "$out" "FACT json-keys: PASS" "the happy path: the keys"
  expect_contains "$out" "FACT no-credential: PASS" "the happy path: no credential"
  expect_contains "$out" "FACT console-renders-stored-view: PASS" "the happy path: the console"
  expect_contains "$out" "FACT xtrace: PASS" "the happy path: the trace guard"
  expect_contains "$out" "FACT restore: PASS" "the happy path: the restore row"
  expect_contains "$out" "CHECK 149 RESULT END (exit 0)" "the happy path: the verdict"
  expect_contains "$out" "2 repositories" "the happy path: how many repositories were read"
  # The read is the register's row, word for word, and the argv is what a
  # console passes: a flag added or lost here is a check of another read.
  expect_argv "$dir" "fugaro coordinator status --from-store --all --json --project $NAME" "the happy path"
  # Two calls and no more: this machine's own configs, and the one read. Nothing
  # else is a command this check has any business making.
  expect_calls "$dir" 2 "the happy path"
  expect_contains "$(cat "$dir/state/calls")" "fugaro config list --json" "the happy path: the local read"
  assert_no_secret "$dir" "the happy path"
  expect_scratch_gone "$dir" "the happy path"
}

# The console step is the owner's, and the two answers are the two verdicts: y
# passes, n fails. Neither is a flag's business except --console-ok, which stands
# for y alone.
test_the_console_answer_is_asked_and_recorded() {
  setup_case answer
  printf 'y\n' >"$dir/stdin"
  RUN149_STDIN="$dir/stdin"
  RUN149_ARGS=(--project "$NAME")
  run149
  expect_status "$status" 0 "the typed answer"
  expect_contains "$out" "FACT console-renders-stored-view: PASS" "the typed answer"
  expect_contains "$out" "you answered y" "the typed answer: the row says where the answer came from"
  expect_contains "$(cat "$dir/err")" "type y or n" "the typed answer: the prompt"
  expect_contains "$(cat "$dir/err")" "fugaro coordinator status --from-store --all --json" "the typed answer: the source line to look for"
  expect_contains "$(cat "$dir/err")" "the stored view carries no time, so this console claims no age for it" "the typed answer: the sentence to look for"
  expect_contains "$(cat "$dir/err")" "the last stored view" "the typed answer: the label to look for"
  expect_contains "$(cat "$dir/err")" "no stored view: the runs alone" "the typed answer: the wrong label is named too"
  assert_no_secret "$dir" "the typed answer"
  expect_scratch_gone "$dir" "the typed answer"
}

test_a_console_that_shows_the_runs_alone_is_a_fail() {
  setup_case noanswer
  printf 'n\n' >"$dir/stdin"
  RUN149_STDIN="$dir/stdin"
  RUN149_ARGS=(--project "$NAME")
  run149
  expect_status "$status" 1 "the console shows the runs alone"
  expect_contains "$out" "FACT console-renders-stored-view: FAIL" "the console shows the runs alone"
  expect_contains "$out" "you answered n" "the console shows the runs alone: the row says where the answer came from"
  # The CLI half passed, so the block has to say which half failed.
  expect_contains "$out" "FACT stored-view: PASS" "the console shows the runs alone: the read was fine"
  expect_contains "$out" "CHECK 149 RESULT END (exit 1)" "the console shows the runs alone: the verdict"
}

test_an_unanswered_console_prompt_stops() {
  setup_case unanswered
  RUN149_ARGS=(--project "$NAME")
  run149
  expect_status "$status" 2 "an unanswered prompt"
  expect_contains "$out" "FACT console-renders-stored-view: FAIL — not reached" "an unanswered prompt: no verdict about a window nobody looked at"
  expect_contains "$out" "not answered" "an unanswered prompt: the reason"
  expect_contains "$out" "CHECK 149 RESULT END (exit 2)" "an unanswered prompt: the verdict"
  assert_no_secret "$dir" "an unanswered prompt"
}

# --console-ok stands for the typed answer and NOT for a reading: the row has to
# say that nobody looked at a window, or the flag becomes a way to claim a
# console fact without a console.
test_console_ok_is_your_word() {
  setup_case consoleok
  default_args
  run149
  expect_status "$status" 0 "--console-ok"
  expect_contains "$out" "FACT console-renders-stored-view: PASS" "--console-ok"
  expect_contains "$out" "NOTHING WAS LOOKED AT" "--console-ok: the row admits it is the owner's word"
  expect_absent "$(cat "$dir/err")" "type y or n" "--console-ok: no prompt was asked"
}

test_the_runs_alone_fallback_is_not_a_stored_view() {
  setup_case fallback
  edit_view "$dir" 'doc["repos"][0]["stored"] = False'
  default_args
  run149
  expect_status "$status" 2 "the runs-alone fallback"
  expect_contains "$out" "FACT stored-view: FAIL" "the runs-alone fallback"
  expect_contains "$out" "RUNS ALONE" "the runs-alone fallback: the row names what it read"
  expect_contains "$out" "1 of the 2 repositories" "the runs-alone fallback: how many were unstored"
  # The console step is not reached, so nothing is claimed about a window that
  # would have had no stored view to draw.
  expect_contains "$out" "FACT console-renders-stored-view: FAIL — not reached" "the runs-alone fallback"
  expect_calls "$dir" 2 "the runs-alone fallback"
  assert_no_secret "$dir" "the runs-alone fallback"
  expect_scratch_gone "$dir" "the runs-alone fallback"
}

test_a_document_without_the_repos_envelope_stops() {
  setup_case envelope
  edit_view "$dir" 'doc = doc["repos"][0]'
  default_args
  run149
  expect_status "$status" 2 "the one-repository document"
  expect_contains "$out" "FACT stored-view: FAIL" "the one-repository document"
  expect_contains "$out" "repos-missing" "the one-repository document: the shape problem is named"
  # The read WAS made — it is the document that is not the one the console reads.
  expect_argv "$dir" "fugaro coordinator status --from-store --all --json --project $NAME" "the one-repository document: the read"
  expect_scratch_gone "$dir" "the one-repository document"
}

test_a_missing_key_the_console_reads_is_a_fail() {
  setup_case missingkey
  edit_view "$dir" 'del doc["repos"][0]["view"]["records"][0]["runs"][0]["id"]'
  default_args
  run149
  expect_status "$status" 1 "a missing key"
  expect_contains "$out" "FACT json-keys: FAIL" "a missing key"
  expect_contains "$out" "run-id-missing" "a missing key: the key path is named"
  expect_contains "$out" "FACT stored-view: PASS" "a missing key: the read itself was the stored view"
  expect_contains "$out" "FACT console-renders-stored-view: PASS" "a missing key: the owner was still asked"
  assert_no_secret "$dir" "a missing key"
}

test_a_pr_number_below_one_is_a_fail() {
  setup_case przero
  edit_view "$dir" 'doc["repos"][0]["view"]["records"][0]["pr"]["number"] = 0'
  default_args
  run149
  expect_status "$status" 1 "a pull request number of 0"
  expect_contains "$out" "FACT json-keys: FAIL" "a pull request number of 0"
  expect_contains "$out" "record-pr-number-below-one" "a pull request number of 0: the console's own refusal"
}

test_a_flag_that_is_not_a_bool_stops() {
  setup_case notbool
  edit_view "$dir" 'doc["repos"][0]["stored"] = "yes"'
  default_args
  run149
  expect_status "$status" 2 "stored as a string"
  expect_contains "$out" "FACT stored-view: FAIL" "stored as a string"
  expect_contains "$out" "entry-stored-not-a-bool" "stored as a string: the shape problem is named"
  expect_contains "$out" "FACT console-renders-stored-view: FAIL — not reached" "stored as a string"
}

test_an_empty_repos_array_stops() {
  setup_case emptyrepos
  edit_view "$dir" 'doc["repos"] = []'
  default_args
  run149
  expect_status "$status" 2 "an empty repos array"
  expect_contains "$out" "FACT stored-view: FAIL" "an empty repos array"
  expect_contains "$out" "repos-empty" "an empty repos array: the shape §6.13 says this read does not print"
}

# A credential-shaped value in what the read printed is a STOP and never an echo:
# the row names the kind, the text never reaches the block, and the run does not
# go on to ask about a console.
test_a_credential_shaped_output_stops() {
  setup_case credential
  edit_view "$dir" "doc[\"repos\"][0][\"view\"][\"records\"][0][\"pr\"][\"title\"] = \"$APIKEY_SENTINEL\""
  default_args
  run149
  expect_status "$status" 2 "a credential in the read's own output"
  expect_contains "$out" "FACT no-credential: FAIL" "a credential in the read's own output"
  expect_contains "$out" "google-api-key" "a credential in the read's own output: the kind is named"
  expect_contains "$out" "never reaches this block" "a credential in the read's own output: the block says so"
  expect_contains "$out" "FACT console-renders-stored-view: FAIL — not reached" "a credential in the read's own output"
  expect_absent "$out" "$APIKEY_SENTINEL" "a credential in the read's own output: the block"
  expect_absent "$(cat "$dir/err")" "$APIKEY_SENTINEL" "a credential in the read's own output: the owner's own progress"
  assert_no_secret "$dir" "a credential in the read's own output"
  expect_scratch_gone "$dir" "a credential in the read's own output"
}

# The command's own message is shown to the owner ONLY when it carries nothing
# credential-shaped: the one thing this check must never do is put a secret in
# the log the owner pastes back.
test_a_credential_in_the_commands_own_message_is_not_echoed() {
  setup_case stderrcred
  printf '1\n' >"$dir/state/read.status"
  printf 'fugaro: refusing, Authorization: Bearer %s\n' "$BEARER_SENTINEL" >"$dir/state/read.err"
  default_args
  run149
  expect_status "$status" 2 "a credential in the command's own message"
  expect_contains "$(cat "$dir/err")" "NOT printed here" "a credential in the command's own message: the owner is told why"
  expect_absent "$(cat "$dir/err")" "$BEARER_SENTINEL" "a credential in the command's own message: the echo"
  expect_absent "$out" "$BEARER_SENTINEL" "a credential in the command's own message: the block"
  assert_no_secret "$dir" "a credential in the command's own message"
}

test_a_read_that_does_not_answer_stops() {
  setup_case noread
  printf '1\n' >"$dir/state/read.status"
  printf 'fugaro: the project has no database\n' >"$dir/state/read.err"
  default_args
  run149
  expect_status "$status" 2 "a read that did not answer"
  expect_contains "$out" "FACT stored-view: FAIL — not reached" "a read that did not answer: no verdict about a view nobody read"
  expect_contains "$out" "did not answer" "a read that did not answer: the reason"
  expect_contains "$(cat "$dir/err")" "the project has no database" "a read that did not answer: its own message is shown"
  expect_contains "$out" "FACT project: PASS" "a read that did not answer: the name was still settled"
}

test_belong_is_refused() {
  setup_case belong
  RUN149_ARGS=(--project belong --console-ok)
  run149
  expect_status "$status" 2 "the owner's own installation"
  expect_contains "$out" "FACT sandbox: FAIL" "the owner's own installation"
  expect_contains "$out" "belong" "the owner's own installation: the name"
  expect_calls "$dir" 0 "the owner's own installation: nothing was read at all"
}

test_a_name_carrying_belong_is_refused() {
  setup_case belongname
  RUN149_ARGS=(--project fugaro-belong --console-ok)
  run149
  expect_status "$status" 2 "a name carrying belong"
  expect_contains "$out" "FACT sandbox: FAIL" "a name carrying belong"
  expect_calls "$dir" 0 "a name carrying belong: nothing was read at all"
}

test_a_config_that_names_belong_stops() {
  setup_case configbelong
  write_configs "$dir" "$NAME" "fugaro-belong" "$REGION"
  default_args
  run149
  expect_status "$status" 2 "a config naming belong"
  expect_contains "$out" "FACT sandbox: FAIL" "a config naming belong"
  expect_contains "$out" "fugaro-belong" "a config naming belong: the project it read"
  expect_calls "$dir" 1 "a config naming belong: no read was made"
}

test_a_project_this_machine_does_not_have_stops() {
  setup_case nosuch
  RUN149_ARGS=(--project prj-nowhere --console-ok)
  run149
  expect_status "$status" 2 "an installation this machine has not got"
  expect_contains "$out" "no installation named prj-nowhere" "an installation this machine has not got"
  expect_contains "$out" "lists 1 of them" "an installation this machine has not got: how many it lists"
  expect_calls "$dir" 1 "an installation this machine has not got: no read was made"
}

test_a_project_that_is_not_the_live_test_one_stops() {
  setup_case otherlive
  RUN149_LIVE=some-other-project
  default_args
  run149
  expect_status "$status" 2 "a project the register does not name as a sandbox"
  expect_contains "$out" "FACT sandbox: FAIL" "a project the register does not name as a sandbox"
  expect_contains "$out" "some-other-project" "a project the register does not name as a sandbox: the name it read"
  expect_calls "$dir" 1 "a project the register does not name as a sandbox: no read was made"
}

test_an_unset_live_project_stops() {
  setup_case nolive
  RUN149_LIVE=""
  default_args
  run149
  expect_status "$status" 2 "no live-test project"
  expect_contains "$out" "FACT sandbox: FAIL" "no live-test project"
  expect_contains "$out" "export FUGARO_LIVE_GCP_PROJECT" "no live-test project: the one line to export"
  expect_calls "$dir" 0 "no live-test project: nothing was read at all"
}

test_a_live_project_naming_belong_is_refused() {
  setup_case livebelong
  RUN149_LIVE=fugaro-belong
  default_args
  run149
  expect_status "$status" 2 "a live-test project naming belong"
  expect_contains "$out" "FACT sandbox: FAIL" "a live-test project naming belong"
  expect_calls "$dir" 0 "a live-test project naming belong: nothing was read at all"
}

test_a_config_list_that_does_not_answer_stops() {
  setup_case noconfigs
  printf '1\n' >"$dir/state/config.status"
  printf 'fugaro: no configuration directory\n' >"$dir/state/config.err"
  default_args
  run149
  expect_status "$status" 2 "a config list that did not answer"
  expect_contains "$out" "FACT sandbox: FAIL — not reached" "a config list that did not answer"
  expect_contains "$out" "did not answer" "a config list that did not answer: the reason"
  expect_contains "$(cat "$dir/err")" "no configuration directory" "a config list that did not answer: its own message is shown"
  expect_calls "$dir" 1 "a config list that did not answer: no read was made"
}

# --dry-run prints the plan and runs nothing: not the read, not the config list,
# and not the owner's console step.
test_dry_run_reads_nothing() {
  setup_case dryrun
  RUN149_ARGS=(--project "$NAME" --console-ok --dry-run)
  run149
  expect_status "$status" 0 "--dry-run"
  expect_contains "$out" "CHECK 149 RESULT" "--dry-run: the block"
  expect_contains "$out" "FACT project: PASS" "--dry-run: the name"
  expect_contains "$out" "FACT dry-run: PASS" "--dry-run: the row"
  expect_contains "$out" "no fugaro command was run at all" "--dry-run: the row says so"
  expect_contains "$out" "CHECK 149 RESULT END (exit 0)" "--dry-run: the verdict"
  expect_absent "$out" "FACT stored-view: FAIL" "--dry-run: no verdict about a view nobody read"
  expect_contains "$(cat "$dir/err")" "fugaro coordinator status --from-store --all --json --project $NAME" "--dry-run: the plan names the read"
  expect_absent "$(cat "$dir/err")" "type y or n" "--dry-run: the console step is not asked"
  expect_calls "$dir" 0 "--dry-run: no fugaro call at all"
  expect_scratch_gone "$dir" "--dry-run"
}

test_xtrace_is_refused() {
  setup_case xtrace
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE149_DIR="$dir/state" FUGARO_LIVE_GCP_PROJECT="$GCP" \
    PATH="$dir/bin:$PATH" \
    "$timeout_bin" 60 bash -x "$script" --project "$NAME" --console-ok 2>"$dir/err") || status=$?
  expect_status "$status" 2 "xtrace"
  expect_contains "$(cat "$dir/err")" "+ " "xtrace: the trace really was on"
  expect_contains "$(cat "$dir/err")" "tracing" "xtrace: the guard names why"
  expect_absent "$(cat "$dir/err")" "a pull request somebody opened" "xtrace: the guard got in before a document was read"
  expect_no_read "$dir" "xtrace"
  expect_calls "$dir" 0 "xtrace: nothing was read at all"
}

# The arguments: --project is required, an unknown one is refused, and --help
# prints the usage without printing a block (a run that measured nothing has no
# verdict to report).
test_usage() {
  setup_case usage
  run149
  expect_status "$status" 2 "no --project"
  expect_contains "$(cat "$dir/err")" "usage" "no --project: the usage line"
  expect_contains "$(cat "$dir/err")" "--project" "no --project: the flag is named"
  expect_calls "$dir" 0 "no --project: nothing was read"

  run149 --project "$NAME" --sandbox
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$(cat "$dir/err")" "unknown argument" "an unknown argument: the refusal"
  expect_calls "$dir" 0 "an unknown argument: nothing was read"

  run149 --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "usage: check149.sh" "--help: the usage"
  expect_absent "$out" "CHECK 149 RESULT" "--help: no block for a run that measured nothing"
  expect_calls "$dir" 0 "--help: nothing was read"
  assert_no_secret "$dir" "usage"
}

test_a_missing_tool_still_prints_a_block() {
  setup_case notool
  # A PATH with bash alone on it: the check reaches its own requirements list and
  # names the first tool it cannot find, rather than failing on something else.
  mkdir -p "$dir/empty-bin"
  ln -s "$bash_bin" "$dir/empty-bin/bash"
  status=0
  out=$(env PATH="$dir/empty-bin" TMPDIR="$dir/tmp" FAKE149_DIR="$dir/state" \
    "$timeout_bin" 60 "$bash_bin" "$script" --project "$NAME" --console-ok 2>"$dir/err") || status=$?
  expect_status "$status" 2 "a missing tool"
  expect_contains "$out" "CHECK 149 RESULT" "a missing tool: the block"
  expect_contains "$out" "FACT aborted: FAIL — " "a missing tool: the abort row"
  expect_contains "$out" "needs 'mktemp'" "a missing tool: the tool it names"
  expect_contains "$out" "FACT stored-view: FAIL — not reached" "a missing tool: no verdict about a view nobody read"
  expect_absent "$out" "FACT stored-view: PASS" "a missing tool: nothing was read"
}

# The suite's own control, mirroring the check's red step: the assertions above
# are worth nothing if a script that reports the runs-alone fallback as a stored
# view goes unnoticed. The copy below is check149.sh with that one verdict
# flipped, and this suite's own expectation about the fallback has to catch it.
#
# The counter is a variable of the subshell, not the suite's failures: the FAIL
# lines this provokes on purpose are counted rather than printed, because output
# that reads 'FAIL' beside a suite that passed is output nobody trusts.
test_the_stored_view_assertion_catches_a_mutated_script() {
  local dir=$work/mutated caught
  setup_case mutated
  sed 's/    fact stored-view FAIL "the read answered the RUNS ALONE/    fact stored-view PASS "the read answered the RUNS ALONE/' \
    "$script" >"$dir/mutated.sh"
  if cmp -s "$script" "$dir/mutated.sh"; then
    fail "mutated: the sed matched nothing, so this control tested a copy of the check as it is"
    return 0
  fi
  # The mutation is run the way the fallback case runs the check, and the one
  # expectation that case makes about the verdict is the thing under test.
  local saved=$script
  edit_view "$dir" 'doc["repos"][0]["stored"] = False'
  script=$dir/mutated.sh
  default_args
  run149
  script=$saved
  caught=$(
    local caught_failures=0
    fail() { caught_failures=$((caught_failures + 1)); }
    expect_contains "$out" "FACT stored-view: FAIL" "mutated"
    printf '%s' "$caught_failures"
  )
  [ "${caught:-0}" -ge 1 ] \
    || fail "mutated: a script that reports the runs-alone fallback as a stored view raised $caught of the assertions, so they are vacuous"
}

# ------------------------------------------------------------------ the shards
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

t test_happy_path
t test_the_console_answer_is_asked_and_recorded
t test_a_console_that_shows_the_runs_alone_is_a_fail
t test_an_unanswered_console_prompt_stops
t test_console_ok_is_your_word
t test_the_runs_alone_fallback_is_not_a_stored_view
t test_a_document_without_the_repos_envelope_stops
t test_a_missing_key_the_console_reads_is_a_fail
t test_a_pr_number_below_one_is_a_fail
t test_a_flag_that_is_not_a_bool_stops
t test_an_empty_repos_array_stops
t test_a_credential_shaped_output_stops
t test_a_credential_in_the_commands_own_message_is_not_echoed
t test_a_read_that_does_not_answer_stops
t test_belong_is_refused
t test_a_name_carrying_belong_is_refused
t test_a_config_that_names_belong_stops
t test_a_project_this_machine_does_not_have_stops
t test_a_project_that_is_not_the_live_test_one_stops
t test_an_unset_live_project_stops
t test_a_live_project_naming_belong_is_refused
t test_a_config_list_that_does_not_answer_stops
t test_dry_run_reads_nothing
t test_xtrace_is_refused
t test_usage
t test_a_missing_tool_still_prints_a_block
t test_the_stored_view_assertion_catches_a_mutated_script

if [ "$failures" -ne 0 ]; then
  echo "check149_test: $failures failure(s)" >&2
  exit 1
fi
echo "check149_test: all tests passed" >&2
