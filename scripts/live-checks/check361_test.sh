#!/usr/bin/env bash
# Tests for scripts/live-checks/check361.sh, against a fake gcloud and a fake
# curl on PATH: nothing here reaches the network, there is no cloud project and
# no credential, and no step of the live check is run.
#
# The fake gcloud mints a token whose value is a sentinel, and the fake curl
# answers the check's ONE read from files this test writes — the status code, and
# the Identity Platform document — while recording the request there: the URL,
# the headers on it, and the Authorization header of the `--config` file the
# check handed over. That record is what lets a case say the token went into a
# 0600 file rather than onto a command line, and the sentinel is what lets the
# other cases say it never reaches the run's output at all.
#
# Usage: bash scripts/live-checks/check361_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check361.sh
# The PHYSICAL path, so a path this test compares and a path a child reports are
# spelled the same way.
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK361_TEST_KEEP:-}" ]; then
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 600)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT (aborted|project|belong|xtrace|token|config|pin):' | sed 's/^/ ... last run: /')"
}

# The project every case reads: a workspace's own, and never the owner's.
PROJECT=workspace-alpha-1234
# The token the fake gcloud mints. No credential is handled anywhere in this
# test: this is a sentinel, and the cases below assert it never reaches the run's
# output — that is the whole point of its being here.
TOKEN_SENTINEL='SENTINEL-361-access-token-not-a-credential'

# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(project belong xtrace token config password google anonymous phone
  other-providers public-signup custom pin)

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake gcloud for check361.sh's test: `auth print-access-token` mints the
# sentinel this test's cases look for, and every other call fails, so a case can
# say what the check asked of a cloud tool. The token itself comes from a file,
# so what the run's output must not contain is written in exactly one place.
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"$FAKE361_DIR/calls"
if [ "${1:-}" = auth ] && [ "${2:-}" = print-access-token ]; then
  if [ -s "$FAKE361_DIR/token.status" ]; then
    printf 'fake gcloud: no credential is available\n' >&2
    exit "$(cat "$FAKE361_DIR/token.status")"
  fi
  cat "$FAKE361_DIR/token.txt"
  exit 0
fi
printf 'fake gcloud: unexpected %s\n' "$*" >&2
exit 64
GCLOUD_EOF

  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A fake curl for check361.sh's test: the one read is answered from $FAKE361_DIR
# (`http_code`, `body.json`), and the request is recorded there — the URL, the
# `-H` headers on the command line, and the Authorization header of the
# `--config` file. The check's own output is never consulted here: what a case
# asserts about the token is read from these records.
set -euo pipefail
printf 'curl %s\n' "$*" >>"$FAKE361_DIR/calls"
conf=""
out=""
url=""
while [ $# -gt 0 ]; do
  case "$1" in
  --config)
    conf=${2:-}
    shift 2
    ;;
  -o)
    out=${2:-}
    shift 2
    ;;
  -w)
    shift 2
    ;;
  --max-time)
    shift 2
    ;;
  -H)
    printf 'header %s\n' "${2:-}" >>"$FAKE361_DIR/calls"
    shift 2
    ;;
  -*) shift ;;
  *)
    url=$1
    shift
    ;;
  esac
done
printf 'url %s\n' "$url" >>"$FAKE361_DIR/calls"
# The token of the config file, verbatim, in its own record: this is how a case
# says the check carried its credential the way it says it does.
if [ -n "$conf" ]; then
  sed -n 's/^header = "Authorization: Bearer \(.*\)"$/\1/p' "$conf" >"$FAKE361_DIR/auth" 2>/dev/null || true
  # The file's mode, so a case can say it is 0600 and not merely present. Read
  # with python3 rather than stat, whose -c is GNU's: the owner runs this check
  # on their own machine, and this test on both.
  python3 -c 'import os, sys; print("%o" % (os.stat(sys.argv[1]).st_mode & 0o777))' "$conf" \
    >"$FAKE361_DIR/auth.mode" 2>/dev/null || printf 'unknown\n' >"$FAKE361_DIR/auth.mode"
fi
if [ -s "$FAKE361_DIR/curl.status" ]; then
  printf 'fake curl: the request did not answer\n' >&2
  exit "$(cat "$FAKE361_DIR/curl.status")"
fi
if [ -n "$out" ]; then cp "$FAKE361_DIR/body.json" "$out"; fi
cat "$FAKE361_DIR/http_code"
CURL_EOF

  chmod +x "$1/bin/gcloud" "$1/bin/curl"
}

# ---------------------------------------------------------------- the fixtures
# write_config DIR PASSWORD GOOGLE ANONYMOUS PHONE SIGNUP [EXTRA...] — the
# Identity Platform document the read answers with. PASSWORD, ANONYMOUS, PHONE
# and SIGNUP are `on`/`off`; GOOGLE is `on`, `off` (the entry is there and
# disabled) or `absent` (no entry at all), because "no google.com in the
# document" and "google.com switched off" are two different readings; and each
# EXTRA is `field:provider`, an enabled entry that the pin does not admit, added
# to the field it names (`defaultSupportedIdpConfigs` or `oauthIdpConfigs`).
write_config() {
  local dir=$1
  shift
  local password=$1 google=$2 anonymous=$3 phone=$4 signup=$5
  shift 5
  python3 - "$dir/state/body.json" "$password" "$google" "$anonymous" "$phone" "$signup" "$@" <<'PY'
import json
import sys

out, password, google, anonymous, phone, signup = sys.argv[1:7]
extra = sys.argv[7:]


def flag(word):
    return word == "on"


idps = []
if google != "absent":
    idps.append({"name": "projects/workspace-alpha-1234/defaultSupportedIdpConfigs/google.com",
                 "enabled": flag(google)})
oauth = []
for spec in extra:
    field, _, provider = spec.partition(":")
    entry = {"name": "projects/workspace-alpha-1234/%s/%s" % (field, provider), "enabled": True}
    (idps if field == "defaultSupportedIdpConfigs" else oauth).append(entry)
doc = {
    "signIn": {"email": {"enabled": flag(password)},
               "anonymous": {"enabled": flag(anonymous)},
               "phoneNumber": {"enabled": flag(phone)}},
    "defaultSupportedIdpConfigs": idps,
    "oauthIdpConfigs": oauth,
    "client": {"permissions": {"disabledUserSignup": flag(signup)}},
}
with open(out, "w", encoding="utf-8") as fh:
    json.dump(doc, fh)
PY
}

# setup_case NAME — a fresh case: the fakes, a project that carries exactly the
# pinned provider set, and the read answering HTTP 200. A case changes what it is
# about.
setup_case() {
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp"
  write_fakes "$dir"
  : >"$dir/state/calls"
  printf '%s\n' "$TOKEN_SENTINEL" >"$dir/state/token.txt"
  printf '200' >"$dir/state/http_code"
  : >"$dir/state/curl.status"
  : >"$dir/state/token.status"
  write_config "$dir" on on off off off
}

# run361 [ARGS...] — runs the check on the case at hand. RUN361_ARGS adds
# arguments, RUN361_ENV names environment variables (NAME=value). Sets $status,
# $out and $dir/err.
run361() {
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE361_DIR="$dir/state" \
    PATH="$dir/bin:$PATH" \
    ${RUN361_ENV[@]+"${RUN361_ENV[@]}"} \
    bash ${RUN361_ARGS[@]+"${RUN361_ARGS[@]}"} "$script" "$@" 2>"$dir/err") || status=$?
  RUN361_ARGS=()
  RUN361_ENV=()
}

RUN361_ARGS=()
RUN361_ENV=()

expect_block_keys() { # what
  expect_contains "$out" "CHECK 361 RESULT" "$1: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$out" "FACT $key:" "$1: the $key row"
  done
  expect_contains "$out" "CHECK 361 RESULT END (exit $status)" "$1: the block's own ending"
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

expect_no_cloud_call() { # what
  [ -s "$dir/state/calls" ] && fail "$1: a cloud tool was called: $(tr '\n' ';' <"$dir/state/calls")"
  return 0
}

# The token never reaches the log or a command line. Asserted on its own, so a
# case whose run stopped before anything was minted can make it too.
expect_the_token_never_leaks() { # what
  expect_absent "$out" "$TOKEN_SENTINEL" "$1: the minted token in the run's stdout"
  expect_absent "$(cat "$dir/err" 2>/dev/null || true)" "$TOKEN_SENTINEL" "$1: the minted token in the run's stderr"
  expect_absent "$(cat "$dir/state/calls" 2>/dev/null || true)" "$TOKEN_SENTINEL" "$1: the minted token on a command line"
}

# And it travelled the way the check says it does: through the 0600 file curl
# reads with --config, which the fake curl recorded.
expect_the_token_travelled_by_file() { # what
  expect_contains "$(cat "$dir/state/auth" 2>/dev/null || true)" "$TOKEN_SENTINEL" "$1: the token the config file carried"
  expect_contains "$(cat "$dir/state/auth.mode" 2>/dev/null || true)" "600" "$1: the config file's mode"
}

# ------------------------------------------------------------------- the cases
test_the_pinned_set_passes() {
  setup_case pinned
  run361 --project "$PROJECT"
  expect_status "$status" 0 "the pinned set"
  expect_block_keys "the pinned set"
  for key in "${BLOCK_KEYS[@]}"; do
    case "$key" in project | belong | xtrace | token | config | password | google | anonymous | phone | other-providers | public-signup | pin)
      expect_contains "$out" "FACT $key: PASS" "the pinned set: the $key row"
      ;;
    esac
  done
  expect_contains "$out" "FACT custom: INFO" "the pinned set: the custom row is an INFO, never a criterion"
  expect_contains "$(cat "$dir/state/calls")" "url https://identitytoolkit.googleapis.com/admin/v2/projects/$PROJECT/config" "the pinned set: the document the read is addressed to"
  expect_contains "$(cat "$dir/state/calls")" "header x-goog-user-project: $PROJECT" "the pinned set: the quota header internal/infra's own client sets"
  expect_the_token_never_leaks "the pinned set"
  expect_the_token_travelled_by_file "the pinned set"
  expect_contains "$out" "FACT auth-conf: PASS" "the pinned set: the credential file is gone"
  expect_scratch_gone "$dir" "the pinned set"
}

test_a_password_provider_that_is_off_fails() {
  setup_case password-off
  write_config "$dir" off on off off off
  run361 --project "$PROJECT"
  expect_status "$status" 1 "a project without the password provider"
  expect_contains "$out" "FACT password: FAIL" "a project without the password provider"
  expect_contains "$out" "FACT google: PASS" "a project without the password provider: the other rows are still read"
  expect_contains "$out" "FACT pin: FAIL" "a project without the password provider"
  expect_contains "$out" "password" "the pin's own sentence names the row that failed"
}

test_a_google_provider_that_is_off_fails() {
  setup_case google-off
  write_config "$dir" on off off off off
  run361 --project "$PROJECT"
  expect_status "$status" 1 "a project with google.com switched off"
  expect_contains "$out" "FACT google: FAIL" "a project with google.com switched off"
  expect_contains "$out" "FACT password: PASS" "a project with google.com switched off"
}

test_a_google_provider_that_is_absent_from_the_document_fails() {
  setup_case google-absent
  write_config "$dir" on absent off off off
  run361 --project "$PROJECT"
  expect_status "$status" 1 "a document with no google.com entry at all"
  expect_contains "$out" "FACT google: FAIL" "a document with no google.com entry at all"
  expect_contains "$out" "FACT pin: FAIL" "a document with no google.com entry at all"
}

test_anonymous_sign_in_is_refused_by_the_pin() {
  setup_case anonymous
  write_config "$dir" on on on off off
  run361 --project "$PROJECT"
  expect_status "$status" 1 "a project that turned anonymous sign-in on"
  expect_contains "$out" "FACT anonymous: FAIL" "a project that turned anonymous sign-in on"
  expect_contains "$out" "FACT pin: FAIL" "a project that turned anonymous sign-in on"
  expect_the_token_never_leaks "a project that turned anonymous sign-in on"
}

test_phone_sign_in_is_refused_by_the_pin() {
  setup_case phone
  write_config "$dir" on on off on off
  run361 --project "$PROJECT"
  expect_status "$status" 1 "a project that turned phone sign-in on"
  expect_contains "$out" "FACT phone: FAIL" "a project that turned phone sign-in on"
}

test_another_provider_is_refused_by_the_pin() {
  setup_case extra-providers
  write_config "$dir" on on off off off \
    defaultSupportedIdpConfigs:facebook.com oauthIdpConfigs:oidc.mycorp
  run361 --project "$PROJECT"
  expect_status "$status" 1 "a project that enabled two providers the pin does not admit"
  expect_contains "$out" "FACT other-providers: FAIL" "a project that enabled two providers the pin does not admit"
  expect_contains "$out" "facebook.com" "the row names the first provider"
  expect_contains "$out" "oidc.mycorp" "the row names the second provider, which is an oauthIdpConfigs entry"
  expect_contains "$out" "FACT google: PASS" "the google.com entry is still judged on its own"
}

test_public_sign_up_being_disabled_fails() {
  setup_case signup
  write_config "$dir" on on off off on
  run361 --project "$PROJECT"
  expect_status "$status" 1 "a project whose public sign-up is disabled"
  expect_contains "$out" "FACT public-signup: FAIL" "a project whose public sign-up is disabled"
  expect_contains "$out" "the workspace's Firebase is the one that does not refuse public sign-up" "the row quotes the sentence it rests on"
}

test_an_uninitialized_project_fails_with_one() {
  setup_case uninitialized
  printf '400' >"$dir/state/http_code"
  printf '{"error": {"message": "CONFIGURATION_NOT_FOUND"}}\n' >"$dir/state/body.json"
  run361 --project "$PROJECT"
  expect_status "$status" 1 "a project with no Identity Platform"
  expect_contains "$out" "FACT config: FAIL" "a project with no Identity Platform"
  expect_contains "$out" "CONFIGURATION_NOT_FOUND" "the row says which reading it is"
  expect_contains "$out" "FACT pin: FAIL" "a project with no Identity Platform is a pin that does not hold"
  expect_scratch_gone "$dir" "a project with no Identity Platform"
}

test_a_refused_read_is_a_could_not_tell() {
  setup_case refused
  printf '403' >"$dir/state/http_code"
  printf '{"error": {"message": "The caller does not have permission"}}\n' >"$dir/state/body.json"
  run361 --project "$PROJECT"
  expect_status "$status" 2 "a read the project refused"
  expect_contains "$out" "FACT config: FAIL" "a read the project refused"
  expect_contains "$out" "FACT aborted: FAIL" "a read the project refused ends the run as an abort"
  expect_contains "$out" "FACT pin: FAIL — not reached" "a refused read measured nothing, so the pin row is not reached"
}

test_a_document_that_cannot_be_parsed_stops() {
  setup_case unparsable
  printf 'this is not an Identity Platform document\n' >"$dir/state/body.json"
  run361 --project "$PROJECT"
  expect_status "$status" 2 "a document that cannot be parsed"
  expect_contains "$out" "FACT providers: FAIL" "the parse failure is recorded"
  expect_contains "$out" "FACT aborted: FAIL" "a document nobody could parse ends the run as an abort"
}

test_a_read_that_does_not_answer_stops() {
  setup_case no-answer
  printf '7' >"$dir/state/curl.status"
  run361 --project "$PROJECT"
  expect_status "$status" 2 "a read that did not answer"
  expect_contains "$out" "FACT config: FAIL" "a read that did not answer"
  expect_contains "$out" "did not answer" "the row says the call failed rather than the project drifting"
}

test_no_token_that_can_be_minted_stops() {
  setup_case no-token
  printf '1' >"$dir/state/token.status"
  run361 --project "$PROJECT"
  expect_status "$status" 2 "a run that could not mint a token"
  expect_contains "$out" "FACT token: FAIL" "a run that could not mint a token"
  expect_absent "$(cat "$dir/state/calls")" "url " "nothing was read from the project"
  expect_scratch_gone "$dir" "a run that could not mint a token"
}

test_belong_is_refused() {
  setup_case belong
  run361 --project belong-sandbox
  expect_status "$status" 2 "a project naming the owner's installation"
  expect_contains "$out" "FACT belong: FAIL" "a project naming the owner's installation"
  expect_contains "$out" "FACT project: PASS — belong-sandbox" "the row says which value it refused"
  expect_no_cloud_call "belong: nothing was read from the project"
}

test_a_project_that_is_not_a_project_id_is_refused() {
  setup_case bad-id
  run361 --project 'Bad_Id!'
  expect_status "$status" 2 "an id that is not a project id"
  expect_contains "$out" "FACT aborted: FAIL" "an id that is not a project id"
  expect_no_cloud_call "a mistyped id: nothing was read"

  run361 --project ab
  expect_status "$status" 2 "an id that is too short"
  expect_no_cloud_call "a short id: nothing was read"

  run361 --project 'a-plan-That-Is-Mixed-Case'
  expect_status "$status" 2 "an id with capitals"
  expect_no_cloud_call "a mixed-case id: nothing was read"
}

test_a_dry_run_makes_no_call() {
  setup_case dry-run
  run361 --project "$PROJECT" --dry-run
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "FACT dry-run: PASS" "a dry run"
  expect_contains "$out" "FACT project: PASS — $PROJECT" "a dry run still reads its argument"
  expect_contains "$out" "FACT belong: PASS" "a dry run still runs the guards"
  expect_contains "$(cat "$dir/err")" "https://identitytoolkit.googleapis.com/admin/v2/projects/$PROJECT/config" "the plan names the URL a real run would read"
  expect_no_cloud_call "a dry run: no gcloud and no curl call at all"
  expect_contains "$out" "FACT auth-conf: PASS" "a dry run mints nothing, so its credential file is trivially gone"
}

test_xtrace_is_refused() {
  setup_case xtrace
  RUN361_ARGS=(-x)
  run361 --project "$PROJECT"
  expect_status "$status" 2 "a shell started with tracing on"
  expect_contains "$out" "FACT xtrace: FAIL" "a shell started with tracing on"
  expect_the_token_never_leaks "a traced shell"
  expect_contains "$out" "FACT auth-conf: PASS" "a traced shell minted nothing"
  expect_absent "$(cat "$dir/err" 2>/dev/null || true)" "print-access-token --quiet" "the trace is off before anything is read: no traced command reaches the log"
}

test_usage() {
  setup_case usage
  run361
  expect_status "$status" 2 "no arguments"
  expect_contains "$out" "FACT aborted: FAIL" "no arguments"
  expect_contains "$out" "--project is required" "the block says which argument is missing"

  run361 --project
  expect_status "$status" 2 "--project with no value"
  expect_contains "$out" "--project needs a project ID" "--project with no value"

  run361 --bucket x
  expect_status "$status" 2 "an argument this check does not take"
  expect_contains "$out" "unknown argument '--bucket'" "an argument this check does not take"
  expect_no_cloud_call "usage errors: nothing was read"

  run361 --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "usage: check361.sh --project" "--help prints the usage"
  expect_absent "$out" "CHECK 361 RESULT" "--help is not a run, so it prints no block"
  expect_no_cloud_call "--help: nothing was read"
}

# The block is what the owner pastes back, so the assertion that the token never
# reaches it has to be one that would CATCH a token in it: a check whose own block
# carried a live credential is the leak this whole part of the script exists to
# prevent. This case runs a copy of the check with one line added to the read,
# and asserts the assertion fails on it — the same self-test Check 146 makes of
# its document assertions.
test_the_token_assertion_catches_an_echoing_script() {
  setup_case echoing
  local copy=$dir/check361-echoing.sh
  python3 - "$script" "$copy" <<'PY'
import sys

src, dst = sys.argv[1], sys.argv[2]
lines = open(src).read().splitlines(keepends=True)
out = []
hit = 0
for line in lines:
    if line.lstrip().startswith('fact token PASS "'):
        out.append('    fact token PASS "leaked: $(cat "$FAKE361_DIR/token.txt")"  # a self-test copy\n')
        hit += 1
    else:
        out.append(line)
if hit != 1:
    raise SystemExit("the line this case edits is not in the check exactly once (%d): it has moved" % hit)
open(dst, "w").write("".join(out))
PY
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE361_DIR="$dir/state" PATH="$dir/bin:$PATH" \
    bash "$copy" --project "$PROJECT" 2>"$dir/err") || status=$?
  expect_contains "$out" "$TOKEN_SENTINEL" "the echoing copy prints a token, which is what this self-test needs"
  local caught
  caught=$(
    local caught_failures=0
    fail() { caught_failures=$((caught_failures + 1)); }
    expect_the_token_never_leaks "detector"
    printf '%s' "$caught_failures"
  )
  [ "${caught:-0}" -eq 1 ] \
    || fail "detector: an echoing script raised $caught of the one assertion that holds the token out of the block, so it is vacuous"
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

t test_the_pinned_set_passes
t test_a_password_provider_that_is_off_fails
t test_a_google_provider_that_is_off_fails
t test_a_google_provider_that_is_absent_from_the_document_fails
t test_anonymous_sign_in_is_refused_by_the_pin
t test_phone_sign_in_is_refused_by_the_pin
t test_another_provider_is_refused_by_the_pin
t test_public_sign_up_being_disabled_fails
t test_an_uninitialized_project_fails_with_one
t test_a_refused_read_is_a_could_not_tell
t test_a_document_that_cannot_be_parsed_stops
t test_a_read_that_does_not_answer_stops
t test_no_token_that_can_be_minted_stops
t test_belong_is_refused
t test_a_project_that_is_not_a_project_id_is_refused
t test_a_dry_run_makes_no_call
t test_xtrace_is_refused
t test_usage
t test_the_token_assertion_catches_an_echoing_script

if [ "$failures" -ne 0 ]; then
  echo "check361_test: $failures failure(s)" >&2
  exit 1
fi
echo "check361_test: all tests passed" >&2
