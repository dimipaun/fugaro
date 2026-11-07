#!/usr/bin/env bash
# Tests for scripts/live-checks/check111.sh, against a fake gcloud and a fake
# fugaro on PATH inside a sandbox of this test's own: nothing here reaches the
# network, there is no cloud project, and no credential is touched. The ADC file
# the fake gcloud writes holds two keys and no token, which is what the check
# reads it for (its quota project, and only that).
#
# The wizard the fakes stand in for is the real one's shape, not its source: each
# mode is answered out of the environment the check set up for it — the quota
# project it pointed (P, Q or S), whether the maimed gcloud is first on PATH and
# answered, whether the run has a terminal — so a case says what a mode did by
# saying what that mode's premise was, exactly as the real picker does.
#
# The ONE live step of Check 111 — mode 2's caller without billing.accounts.list,
# which takes a second Google account or the owner's own IAM — is not run here
# and cannot be: what is tested is that the check records it in its manual row
# and refuses to read mode 2 as a pass while its premise is not there.
#
# Usage: bash scripts/live-checks/check111_test.sh
set -euo pipefail

# Mode 4's document is read with jq, which the check itself requires: without it
# there is no honest way to assert the document, so this suite says so and stops
# rather than skipping the assertion (Check 146's suite has no such need, and
# jq is not on every machine).
if ! command -v jq >/dev/null 2>&1; then
  echo "check111_test: jq is not installed on this machine, and Check 111 needs it for mode 4's document — no test run" >&2
  exit 0
fi

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check111.sh
# The PHYSICAL path, so a path this test compares and a path a child reports are
# spelled the same way.
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK111_TEST_KEEP:-}" ]; then
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
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT (aborted|project|adc-quota|agent-session|xtrace):' | sed 's/^/ ... last run: /')"
}

# The three throwaways the whole suite runs against: a sandbox's, never the
# owner's, and never belong.
P=prj-sandbox-p
Q=prj-sandbox-q
S=prj-sandbox-s
# The quota project the machine's ADC "already" carries: the value the Restore
# has to put back, and the one string every restore assertion reads.
OWN_QUOTA=prj-sandbox-home
# A display name of the owner's, in the file the leak greps read.
DISPLAY_NAME='Sandbox Billing'

# Every fact key the run's block must carry, whatever it measured. The block's
# shape does not depend on how far the run got, which is what makes two blocks
# comparable — and what makes an unrun mode read as FAIL rather than as absent.
BLOCK_KEYS=(fugaro-version project api-off-project api-on-project account-names account-ids
  agent-session xtrace modes config-root adc-quota second-adc precondition
  run-1a offer-1a line-1a api-1a leak-1a
  run-1a-yes offer-1a-yes picker-1a-yes line-1a-yes
  run-1b refusal-1b offer-absent-1b line-1b api-1b leak-1b
  run-2a premise-2a source-2a list-2a line-2a api-2a leak-2a
  run-2b premise-2b unlisted-2b line-2b api-2b leak-2b
  run-3 picker-3 line-3 api-3 leak-3
  run-4 picker-4 document-4 line-4 api-4 leak-4
  run-5 picker-5 needsyou-5 line-5 api-5 leak-5
  run-5-yes needsyou-5-yes line-5-yes
  restore scratch-removed manual)

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake gcloud for check111.sh's test: it answers from $FAKE111_DIR and reaches
# nothing. Every call is recorded, so a case can tell a run that ran its modes
# from one that refused before it did — and, after a FAILed assertion, that the
# Restore's own two writes happened.
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"$FAKE111_DIR/calls"
case "${1:-}" in
services)
  verb=${2:-}
  proj=""
  shift 2
  for a in "$@"; do
    case "$a" in
    --project=*) proj=${a#--project=} ;;
    esac
  done
  case "$verb" in
  list)
    # Cloud Billing's state in that project: a file this test creates and the
    # fake's own disable/enable calls move. Nothing else is ever enabled — and an
    # API that is off is an empty answer with exit 0, exactly as gcloud prints it,
    # never a failure: the check reads a failed read as unreadable, not as "off".
    if [ -f "$FAKE111_DIR/$proj.enabled" ]; then printf 'cloudbilling.googleapis.com\n'; fi
    ;;
  disable)
    rm -f "$FAKE111_DIR/$proj.enabled"
    printf 'disable %s\n' "$proj" >>"$FAKE111_DIR/changed"
    ;;
  enable)
    : >"$FAKE111_DIR/$proj.enabled"
    printf 'enable %s\n' "$proj" >>"$FAKE111_DIR/changed"
    ;;
  *)
    printf 'fake gcloud: unknown services verb %s\n' "$verb" >&2
    exit 64
    ;;
  esac
  ;;
projects)
  # describe <project> --format=value(projectId,lifecycleState,labels.fugaro_created)
  printf '%s\tACTIVE\ttrue\n' "${3:-}"
  ;;
billing)
  case "${2:-}" in
  projects)
    # describe <project> --format=value(billingEnabled): the throwaway has no
    # link, which is every mode's premise.
    printf 'false\n'
    ;;
  accounts)
    # The fallback the picker tries when the API refuses. Mode 1(b), 1(a) and
    # 2(b) run with Check 111's own maimed gcloud first on PATH, so this branch
    # is what mode 2(a)'s premise reaches and theirs never does.
    printf '  %s (billingAccounts/AAAAAA-BBBBBB-CCCCCC)\n' "$DISPLAY_NAME_NAME"
    ;;
  *)
    printf 'fake gcloud: unknown billing verb %s\n' "${2:-}" >&2
    exit 64
    ;;
  esac
  ;;
auth)
  # application-default set-quota-project <project> — the real gcloud rewrites
  # the ADC file; so does this one, so the fake fugaro can tell which mode it is
  # in from the same file the wizard would read.
  if [ "${2:-}" = application-default ] && [ "${3:-}" = set-quota-project ]; then
    # The marker key survives the rewrite: the real gcloud edits this file's
    # JSON and keeps every other key, and the fake fugaro reads the marker to say
    # which login was in place for a run — the one thing mode 2's swap is about.
    who=$(sed -n 's/.*"fake_login"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
      "${CLOUDSDK_CONFIG:?}/application_default_credentials.json" 2>/dev/null || true)
    # FAKE111_QUOTA_DENIED: the account the ADC names cannot use this project, so
    # gcloud refuses to point the quota project at it — the premise of mode 1(a)
    # and 1(b) failing, which the check must turn into a stop rather than a run.
    if [ -n "${FAKE111_QUOTA_DENIED:-}" ] && [ "${4:-}" = "$FAKE111_QUOTA_DENIED" ]; then
      printf 'ERROR: (gcloud.auth.application-default.set-quota-project) The project %s cannot be used by the credentials at %s\n' "${4:-}" "$CLOUDSDK_CONFIG/application_default_credentials.json" >&2
      exit 1
    fi
    printf '{"type": "authorized_user", "quota_project_id": "%s"%s}\n' "${4:-}" \
      "${who:+", \"fake_login\": \"$who\""}" \
      >"${CLOUDSDK_CONFIG:?}/application_default_credentials.json"
  fi
  ;;
esac
GCLOUD_EOF

  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check111.sh's test. Every mode is answered out of the state
# the check set up for it: the quota project it pointed, whether the maimed
# gcloud is first on PATH and answered, and whether this run has a terminal.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE111_DIR/calls"
# Which login was in place for this run: the marker the ADC files carry, which
# the check never writes or reads (it copies files whole) and the fake gcloud
# preserves. It is the only way a case can tell that mode 2 ran under the second
# account and every other mode under the owner's own.
printf 'login %s\n' "$(sed -n 's/.*"fake_login"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
  "${CLOUDSDK_CONFIG:?}/application_default_credentials.json" 2>/dev/null || true)" >>"$FAKE111_DIR/calls"
case "${1:-}" in
version)
  echo "fugaro 0.22.0"
  exit 0
  ;;
setup) ;;
*)
  printf 'fake fugaro: unexpected %s\n' "${1:-}" >&2
  exit 64
  ;;
esac

proj=""
yes=no
json=no
while [ $# -gt 0 ]; do
  case "$1" in
  --gcp-project) proj=${2:-} ;;
  --yes) yes=yes ;;
  --json) json=yes ;;
  esac
  shift
done
quota=$(sed -n 's/.*"quota_project_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
  "${CLOUDSDK_CONFIG:?}/application_default_credentials.json")
link="gcloud billing projects link $proj --billing-account=<billing-account-id>"

# FAKE111_LEAK: the reversal this whole check exists to catch — an account's
# display name and its id in a run that must list nothing. Only the runs that
# are not --json's (whose document the check reads on its own) carry it.
if [ -n "${FAKE111_LEAK:-}" ] && [ "$json" = no ]; then
  echo " 1. $DISPLAY_NAME_NAME (billingAccounts/AAAAAA-BBBBBB-CCCCCC)"
fi

# One form of a leak at a time, in mode 3 alone (the no-TTY, non---json run that
# carries no agent marker), so each of the check's criterion-3 detectors is
# pinned on its own rather than as a union: the API's own resource form
# (FAKE111_LEAK_ID), one of the ids the owner wrote into --account-ids
# (FAKE111_LEAK_BARE_ID — the same id WITHOUT the resource prefix, which is the
# only form that file's grep catches), a display name of theirs
# (FAKE111_LEAK_NAME), and a numbered line that names no account at all
# (FAKE111_LEAK_NUMBERED).
if [ ! -t 1 ] && [ "$json" = no ] && [ -z "${AI_AGENT:-}" ]; then
  [ -z "${FAKE111_LEAK_ID:-}" ] || echo "the account billingAccounts/AAAAAA-BBBBBB-CCCCCC could not be read"
  [ -z "${FAKE111_LEAK_BARE_ID:-}" ] || echo "the account AAAAAA-BBBBBB-CCCCCC could not be read"
  [ -z "${FAKE111_LEAK_NAME:-}" ] || echo "the account $DISPLAY_NAME_NAME could not be read"
  [ -z "${FAKE111_LEAK_NUMBERED:-}" ] || echo "  1. an account you already have"
fi

# FAKE111_ASK: the picker running in a run that cannot answer it — the reversal
# mode 1(a) under --yes and modes 3, 4 and 5 exist to rule out, and the one
# failure the check's own "nothing may be asked where nothing can answer" row
# has to catch. Mode 3 alone carries it here.
if [ -n "${FAKE111_ASK:-}" ] && [ ! -t 1 ] && [ "$json" = no ] && [ -z "${AI_AGENT:-}" ]; then
  echo "Billing accounts you can link to $proj (from gcloud billing accounts list):"
  printf 'Billing account (a number, or none to link none now): '
fi

# The applying stage that belongs to the owner when no terminal can answer it.
if [ -n "${AI_AGENT:-}" ]; then
  echo "the project stage is left for you: run this in your own terminal window, not through a coding agent"
  echo "$link"
  exit 0
fi

if [ "$json" = yes ]; then
  printf '{"ok": true, "project": "%s", "billing": {"linked": false}}\n' "$proj"
  echo "$link" >&2
  exit 0
fi

if [ ! -t 1 ]; then
  echo "$link"
  exit 0
fi

if [ "$quota" = "$FAKE111_Q" ]; then
  # Mode 1(b): the API is disabled on the quota project, which is NOT the one
  # being set up — a refusal that names the line and who to ask, never an offer.
  echo "The Cloud Billing API (cloudbilling.googleapis.com) is disabled on $quota, the quota project of your credentials."
  echo "Run: gcloud services enable cloudbilling.googleapis.com --project $quota"
  echo "then ask the owner of that project to enable it, or link an account you already have."
  echo "$link"
  exit 0
fi

if [ "$quota" = "$FAKE111_S" ]; then
  if gcloud billing accounts list >/dev/null 2>&1; then
    # Mode 2(a): the API refused and the fallback answered. The list is the
    # point of this run, and 'none' is the picker's own way out of it.
    echo "Billing accounts you can link to $proj (from gcloud billing accounts list):"
    echo "  1) $DISPLAY_NAME_NAME (billingAccounts/AAAAAA-BBBBBB-CCCCCC)"
    printf 'Billing account (a number, or none to link none now): '
    read -r answer || true
    echo "linking $answer"
    echo "This run links no billing account."
    echo "$link"
  else
    # Mode 2(b): the API refused and the fallback is maimed — neither answered.
    echo "Billing accounts could not be listed (neither the Cloud Billing API nor gcloud answered)."
    echo "$link"
  fi
  exit 0
fi

if [ "$quota" = "$FAKE111_P" ]; then
  # FAKE111_HANG: a wizard that asks and then never reads the answer — a run
  # that blocks, which is what modes 1(a) and 2(a) exist to rule out. It prints
  # the offer first, so the capture is not empty and the check's runner has to
  # turn this into a FAIL of the mode on its own timeout, not hang with it.
  if [ -n "${FAKE111_HANG:-}" ]; then
    echo "CONFIRM: ENABLE the Cloud Billing API (cloudbilling.googleapis.com) in GCP project $proj, which will incur charges on your account."
    printf 'Type yes to enable it, or press Enter to link an account by hand instead: '
    sleep 600
    exit 0
  fi
  if [ "$yes" = yes ]; then
    # The picker never runs without a terminal that can answer it, --yes or not.
    echo "$link"
    exit 0
  fi
  # FAKE111_NO_OFFER: the picker does not offer at all — the reversal that makes
  # mode 1(a)'s own criterion fail while the run around it carries on.
  if [ -n "${FAKE111_NO_OFFER:-}" ]; then
    echo "$link"
    exit 0
  fi
  echo "CONFIRM: ENABLE the Cloud Billing API (cloudbilling.googleapis.com) in GCP project $proj, which will incur charges on your account."
  printf 'Type yes to enable it, or press Enter to link an account by hand instead: '
  read -r answer || true
  echo "Nothing was enabled. Run:"
  echo "  gcloud services enable cloudbilling.googleapis.com --project $proj"
  echo "then run this again, or link an account you already have."
  echo "$link"
  exit 0
fi

# No quota project of this check's: a run this suite is not about.
echo "$link"
exit 0
FUGARO_EOF

  chmod +x "$1/bin/gcloud" "$1/bin/fugaro"
}

# --------------------------------------------------------------- the fixtures
# setup_case NAME — a fresh case: the fakes, three throwaways (P's API off, Q's
# off, S's ON — mode 2's premise), an ADC that carries this machine's own quota
# project, and the owner's display-name file. A case changes what it is about.
setup_case() {
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/tmp" "$dir/cloudsdk"
  write_fakes "$dir"
  : >"$dir/calls"
  : >"$dir/changed"
  printf '%s\n' "$DISPLAY_NAME" >"$dir/names.txt"
  printf '{"type": "authorized_user", "quota_project_id": "%s", "fake_login": "own"}\n' "$OWN_QUOTA" \
    >"$dir/cloudsdk/application_default_credentials.json"
  # The second account's login, saved aside by the owner the way the manual row
  # says: a file of its own, which the check copies over the ADC for mode 2's
  # runs alone and gives back unchanged. The marker key is this test's own — the
  # check never reads a key of it.
  printf '{"type": "authorized_user", "quota_project_id": "%s", "fake_login": "second"}\n' "$OWN_QUOTA" \
    >"$dir/second-adc.json"
  cp -p "$dir/cloudsdk/application_default_credentials.json" "$dir/adc-pristine.json"
  : >"$dir/$S.enabled"
  RUN111_ARGS=()
  RUN111_ENV=()
  RUN111_XTRACE=""
}

# run111 — runs the check on the case at hand: RUN111_ARGS are the arguments
# after the two every case passes, RUN111_ENV names environment variables
# (NAME=value), and RUN111_XTRACE=1 runs it under `bash -x`. Sets $status, $out
# and $dir/err.
#
# The agent markers are unset: this suite is itself run inside an agent's
# session in CI, and Check 111's first guard is exactly that marker — a marker
# the fake wizard's AI_AGENT route is about, set by the check for one child.
run111() {
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT -u CLAUDE_CODE_SSE_PORT -u XDG_CONFIG_HOME \
    TMPDIR="$dir/tmp" \
    FAKE111_DIR="$dir" \
    FAKE111_Q="$Q" FAKE111_S="$S" FAKE111_P="$P" \
    DISPLAY_NAME_NAME="$DISPLAY_NAME" \
    CLOUDSDK_CONFIG="$dir/cloudsdk" \
    CHECK111_RUN_SECONDS="${CHECK111_TEST_RUN_SECONDS:-10}" \
    PATH="$dir/bin:$PATH" \
    ${RUN111_ENV[@]+"${RUN111_ENV[@]}"} \
    bash ${RUN111_XTRACE:+-x} "$script" \
    --project "$P" --account-names "$dir/names.txt" \
    ${RUN111_ARGS[@]+"${RUN111_ARGS[@]}"} 2>"$dir/err") || status=$?
  RUN111_ARGS=()
  RUN111_ENV=()
  RUN111_XTRACE=""
}

# The case's own three throwaways, spelled out: every case that wants the seven
# runs uses this one line, so what a case is about is the difference it makes.
sandbox_args() {
  RUN111_ARGS=(--api-off-project "$Q" --api-on-project "$S" --second-account sandbox-second@example.invalid \
    --second-adc "$dir/second-adc.json")
}

expect_block_keys() { # out what
  local key
  expect_contains "$1" "CHECK 111 RESULT" "$2: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$1" "FACT $key:" "$2: the $key row"
  done
}

expect_fugaro_ran() { # WHAT — the wizard ran at least once
  grep -q '^fugaro setup ' "$dir/calls" \
    || fail "$1: the wizard never ran (calls: $(tr '\n' ';' <"$dir/calls"))"
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

expect_changed() { # what
  grep -qx "$1" "$dir/changed" \
    || fail "$2: '$1' is not in the Restore's own writes ($(tr '\n' ';' <"$dir/changed"))"
}

expect_unchanged() { # what
  grep -qx "$1" "$dir/changed" \
    && fail "$2: '$1' ran, and this case's run may not touch that project's API" || true
}

# ------------------------------------------------------------------- the tests
# The whole check, all seven runs, with every premise in place: the verdict is
# PASS and the block carries each of the seven runs' criteria.
test_all_seven_runs_pass() {
  setup_case allpass
  sandbox_args
  run111
  expect_status "$status" 0 "all seven"
  expect_block_keys "$out" "all seven"
  expect_contains "$out" "CHECK 111 RESULT END (exit 0)" "all seven: the end line"
  expect_contains "$out" "CHECK 111 RESULT: PASS" "all seven: the verdict"
  expect_contains "$out" "FACT offer-1a: PASS — " "all seven: the typed offer came and was declined"
  expect_contains "$out" "FACT offer-1a-yes: PASS — " "all seven: --yes printed no offer"
  expect_contains "$out" "FACT refusal-1b: PASS — " "all seven: mode 1(b) refused and named the line"
  expect_contains "$out" "FACT source-2a: PASS — " "all seven: the fallback answered mode 2(a)"
  expect_contains "$out" "gcloud billing accounts list" "all seven: the source that answered is named"
  expect_contains "$out" "FACT unlisted-2b: PASS — " "all seven: neither source answered mode 2(b)"
  expect_contains "$out" "FACT picker-3: PASS — " "all seven: no picker without a terminal"
  expect_contains "$out" "FACT document-4: PASS — " "all seven: no account id in --json's document"
  expect_contains "$out" "FACT needsyou-5: PASS — " "all seven: an agent's session is left for you"
  expect_contains "$out" "FACT needsyou-5-yes: PASS — " "all seven: and --yes does not change that"
  expect_contains "$out" "FACT restore: PASS — " "all seven: the Restore"
  expect_contains "$out" "FACT manual: INFO — " "all seven: the one manual step is recorded"
  expect_fugaro_ran "all seven"
  # The check's seven runs, plus the two reruns its section records (mode 1(a)
  # under --yes and mode 5 under --yes): nine wizard runs, every one of them the
  # release build.
  [ "$(grep -c '^fugaro setup ' "$dir/calls")" = 9 ] \
    || fail "all seven: expected 9 wizard runs, got $(grep -c '^fugaro setup ' "$dir/calls")"
  expect_changed "enable $P" "all seven: the Restore re-enables the API mode 1(a) disabled"
  expect_changed "enable $Q" "all seven: and the one mode 1(b) disabled on Q"
  expect_unchanged "enable $S" "all seven: S's API was never disabled, so the Restore must not touch it"
  grep -q "set-quota-project $OWN_QUOTA" "$dir/calls" \
    || fail "all seven: the Restore did not put the machine's own quota project back"
  expect_scratch_gone "$dir" "all seven"
}

# A failed assertion does not stop the Restore: the API mode 1(a) disabled is
# enabled again, the ADC's quota project goes back, and the scratch directory is
# removed — which is the whole of what the owner is promised.
test_a_failed_assertion_still_restores() {
  setup_case failed
  sandbox_args
  RUN111_ENV=(FAKE111_NO_OFFER=1)
  run111
  expect_status "$status" 1 "failed assertion"
  expect_contains "$out" "FACT offer-1a: FAIL — " "failed assertion: the offer is the criterion that failed"
  expect_contains "$out" "CHECK 111 RESULT: FAIL" "failed assertion: the verdict"
  expect_contains "$out" "FACT restore: PASS — " "failed assertion: the Restore still ran"
  expect_changed "enable $P" "failed assertion: the API mode 1(a) disabled is enabled again"
  grep -q "set-quota-project $OWN_QUOTA" "$dir/calls" \
    || fail "failed assertion: the ADC's quota project was not put back"
  expect_scratch_gone "$dir" "failed assertion"
}

# An account list, a display name or a numbered list anywhere but mode 2(a) is
# the leak the whole check exists to catch, and it is a FAIL wherever it appears.
test_a_leak_is_a_fail() {
  setup_case leak
  sandbox_args
  RUN111_ENV=(FAKE111_LEAK=1)
  run111
  expect_status "$status" 1 "leak"
  expect_contains "$out" "FACT leak-3: FAIL — " "leak: the display name in mode 3's capture"
  expect_contains "$out" "FACT leak-5: FAIL — " "leak: and in mode 5's"
  expect_contains "$out" "FACT leak-2a: INFO — " "leak: mode 2(a) is exempt by design"
  expect_contains "$out" "CHECK 111 RESULT: FAIL" "leak: the verdict"
  expect_scratch_gone "$dir" "leak"
}

# Mode 2 is the only part of the run whose caller is the second account: with
# --second-adc the check copies that login over gcloud's own ADC file for mode
# 2's runs alone, and every other mode runs under YOUR login — the premise the
# checklist writes for them (their project stage reads the project through those
# credentials). The two runs are the only ones that read the second marker, and
# both files are given back byte for byte.
test_only_mode_2_runs_under_the_second_login() {
  setup_case swap
  sandbox_args
  cp -p "$dir/second-adc.json" "$dir/second-pristine.json"
  run111
  expect_status "$status" 0 "the swap"
  expect_block_keys "$out" "the swap"
  expect_contains "$out" "FACT second-adc: PASS — " "the swap: the second login is recorded"
  # Mode 2's two runs are the only ones the second login was in place for; the
  # other seven wizard runs — and the `fugaro version` the check reads before the
  # modes — ran under your own login, which is the premise every one of them is
  # written for.
  [ "$(grep -c '^login second$' "$dir/calls")" = 2 ] \
    || fail "the swap: expected mode 2's two runs under the second login, got $(grep -c '^login second$' "$dir/calls"): $(grep '^login ' "$dir/calls" | tr '\n' ' ')"
  [ "$(grep -c '^login own$' "$dir/calls")" = 8 ] \
    || fail "the swap: expected the other seven wizard runs and the version read under your own login, got $(grep -c '^login own$' "$dir/calls")"
  cmp -s "$dir/cloudsdk/application_default_credentials.json" "$dir/adc-pristine.json" \
    || fail "the swap: your own ADC file did not come back byte for byte: $(cat "$dir/cloudsdk/application_default_credentials.json")"
  cmp -s "$dir/second-adc.json" "$dir/second-pristine.json" \
    || fail "the swap: the second login's own file was not given back byte for byte: $(cat "$dir/second-adc.json")"
  expect_scratch_gone "$dir" "the swap"

  # The two copies the swap makes are the only credential files this check ever
  # writes, and they go even when the scratch directory is kept: a kept scratch
  # holds transcripts, never a login.
  setup_case swapkeep
  sandbox_args
  cp -p "$dir/second-adc.json" "$dir/second-pristine.json"
  RUN111_ENV=(CHECK111_KEEP_SCRATCH=1)
  run111
  expect_status "$status" 0 "the swap, scratch kept"
  [ "$(grep -c '^login second$' "$dir/calls")" = 2 ] \
    || fail "the swap, scratch kept: expected mode 2's two runs under the second login, got $(grep -c '^login second$' "$dir/calls")"
  cmp -s "$dir/cloudsdk/application_default_credentials.json" "$dir/adc-pristine.json" \
    || fail "the swap, scratch kept: your own ADC file did not come back byte for byte"
  cmp -s "$dir/second-adc.json" "$dir/second-pristine.json" \
    || fail "the swap, scratch kept: the second login's own file was not given back byte for byte"
  scratch=$(scratch_of "$dir")
  [ -d "$scratch" ] || fail "the swap, scratch kept: the scratch directory was removed anyway"
  [ -z "$(find "$scratch" -name 'adc-*.bak' 2>/dev/null)" ] \
    || fail "the swap, scratch kept: a copy of a login outlived the run: $(find "$scratch" -name 'adc-*.bak')"
}

# A run that stops while mode 2's login is still in place — its premise failing
# at the step that sets it — has to put your own ADC file back from the exit
# trap, byte for byte, or the machine is left logged in as the second account.
test_the_second_login_is_put_back_when_a_mode_stops() {
  setup_case swapfail
  sandbox_args
  cp -p "$dir/second-adc.json" "$dir/second-pristine.json"
  RUN111_ENV=(FAKE111_QUOTA_DENIED="$S")
  run111
  expect_status "$status" 2 "the swap after a stop"
  expect_contains "$(cat "$dir/err")" "could not point your ADC's quota project at $S" "the swap after a stop: the stop names the step"
  # The stop is at the step that sets mode 2's premise, before mode 2(a)'s run
  # and long before mode 2(b)'s: a run that got as far as reading 2(a)'s criteria
  # reads nothing here, and the mode that never started is not concluded about.
  [ "$(grep -c '^fugaro setup ' "$dir/calls")" = 3 ] \
    || fail "the swap after a stop: mode 2(a) never ran, so the only runs are 1(a) twice and 1(b) — got $(grep -c '^fugaro setup ' "$dir/calls")"
  expect_absent "$(cat "$dir/err")" "mode 2(b):" "the swap after a stop: and mode 2(b) never started"
  expect_contains "$out" "FACT restore: PASS — " "the swap after a stop: the Restore still ran"
  expect_contains "$out" "your own login again, byte for byte" "the swap after a stop: and says the file is yours again"
  expect_absent "$out" "STILL the login in" "the swap after a stop: no leftover swap"
  cmp -s "$dir/cloudsdk/application_default_credentials.json" "$dir/adc-pristine.json" \
    || fail "the swap after a stop: your own ADC file did not come back byte for byte: $(cat "$dir/cloudsdk/application_default_credentials.json")"
  cmp -s "$dir/second-adc.json" "$dir/second-pristine.json" \
    || fail "the swap after a stop: the second login's own file was not given back byte for byte"
  grep -q "set-quota-project $OWN_QUOTA" "$dir/calls" \
    || fail "the swap after a stop: the ADC's quota project was not put back"
  expect_scratch_gone "$dir" "the swap after a stop"
}

# The live ADC already IS the login --second-adc names: this machine is still
# logged in as the second account, so the modes that are not mode 2 would run
# under it. Refused before anything is called.
test_a_machine_left_on_the_second_login_is_refused() {
  setup_case stillsecond
  sandbox_args
  cp -p "$dir/second-adc.json" "$dir/cloudsdk/application_default_credentials.json"
  run111
  expect_status "$status" 2 "still on the second login"
  [ -s "$dir/calls" ] && fail "still on the second login: a tool was called: $(tr '\n' ';' <"$dir/calls")"
  expect_contains "$(cat "$dir/err")" "choose your own account" "still on the second login: the stop names the way out"
  expect_absent "$out" "CHECK 111 RESULT: PASS" "still on the second login: no verdict"
  expect_scratch_gone "$dir" "still on the second login"
}

# --second-account without --second-adc says the second account is the caller of
# the WHOLE run, which modes 1(a), 1(b), 3, 4 and 5 are not written for — and
# which the section's own mode-by-mode block makes impossible in one command.
# The refusal names both ways forward, and a run of modes 2a,2b alone is one of
# them: it really does run under that account.
test_a_second_account_for_every_mode_is_refused() {
  setup_case everymode
  RUN111_ARGS=(--api-off-project "$Q" --api-on-project "$S" --second-account sandbox-second@example.invalid)
  run111
  expect_status "$status" 2 "every mode under the second account"
  [ -s "$dir/calls" ] && fail "every mode under the second account: a tool was called: $(tr '\n' ';' <"$dir/calls")"
  expect_contains "$(cat "$dir/err")" "--second-adc" "every mode under the second account: the stop names what to pass instead"
  expect_contains "$(cat "$dir/err")" "--modes 2a,2b" "every mode under the second account: and the run of mode 2 alone"
  expect_scratch_gone "$dir" "every mode under the second account"

  setup_case onlymode2
  local only2=2a,2b
  RUN111_ARGS=(--api-off-project "$Q" --api-on-project "$S" --second-account sandbox-second@example.invalid --modes "$only2")
  run111
  expect_absent "$out" "FACT aborted: FAIL — " "mode 2 alone: the second account as the whole caller is allowed there"
  expect_status "$status" 1 "mode 2 alone"
  expect_contains "$out" "CHECK 111 RESULT: FAIL" "mode 2 alone: the modes this run did not read are not a pass"
  expect_contains "$out" "FACT source-2a: PASS — " "mode 2 alone: and mode 2(a) ran and passed"
  expect_contains "$out" "FACT run-1a: FAIL — not run" "mode 2 alone: the modes it did not run are not a pass"
  expect_scratch_gone "$dir" "mode 2 alone"
}

# --second-adc's own guards: a file that is not there, one that is not a
# credentials file, one that IS gcloud's own ADC, and a run whose credentials
# come from GOOGLE_APPLICATION_CREDENTIALS — where the file this check swaps is
# not the file the run reads, so the swap would change nothing.
test_second_adc_guards() {
  local what
  for what in "missing file" "not a credentials file" "is the live ADC" "GOOGLE_APPLICATION_CREDENTIALS"; do
    setup_case "secondadc-${what// /-}"
    sandbox_args
    case "$what" in
    "missing file") RUN111_ARGS=(--api-off-project "$Q" --api-on-project "$S" --second-account second@example.invalid --second-adc "$dir/no-such-file.json") ;;
    "not a credentials file")
      printf 'not json at all\n' >"$dir/junk.json"
      RUN111_ARGS=(--api-off-project "$Q" --api-on-project "$S" --second-account second@example.invalid --second-adc "$dir/junk.json")
      ;;
    "is the live ADC") RUN111_ARGS=(--api-off-project "$Q" --api-on-project "$S" --second-account second@example.invalid --second-adc "$dir/cloudsdk/application_default_credentials.json") ;;
    "GOOGLE_APPLICATION_CREDENTIALS") RUN111_ENV=(GOOGLE_APPLICATION_CREDENTIALS="$dir/cloudsdk/application_default_credentials.json") ;;
    esac
    run111
    expect_status "$status" 2 "second-adc: $what"
    [ -s "$dir/calls" ] && fail "second-adc: $what: a tool was called: $(tr '\n' ';' <"$dir/calls")"
    expect_absent "$out" "CHECK 111 RESULT: PASS" "second-adc: $what: no verdict"
    expect_scratch_gone "$dir" "second-adc: $what"
  done
}

# The premise of modes 1(a) and 1(b) failing: gcloud refuses to point the ADC's
# quota project at a project the credentials cannot use (a second account that
# has no role there, say). The check stops at that step rather than running the
# mode under a premise it does not have — and the API mode 1(a) already disabled
# is still enabled again by the Restore.
test_a_quota_project_the_adc_cannot_use_stops() {
  setup_case quotadenied
  sandbox_args
  RUN111_ENV=(FAKE111_QUOTA_DENIED="$P")
  run111
  expect_status "$status" 2 "quota project the ADC cannot use"
  expect_contains "$out" "FACT aborted: FAIL — " "quota project the ADC cannot use: the block says the run was stopped"
  expect_contains "$(cat "$dir/err")" "could not point your ADC's quota project at $P" "quota project the ADC cannot use: the stop names the step"
  expect_changed "enable $P" "quota project the ADC cannot use: the API mode 1(a) disabled is enabled again"
  expect_absent "$out" "CHECK 111 RESULT: PASS" "quota project the ADC cannot use: no verdict"
  expect_scratch_gone "$dir" "quota project the ADC cannot use"
}

# The picker's own prompt in a run that cannot answer it: mode 1(a) under --yes
# and modes 3, 4 and 5 are the runs whose whole point is that nothing is asked
# where nothing can answer, and an ask is not a leak — so this is the one case
# that reads that row as a FAIL, and it leaves the leak rows alone.
test_a_picker_where_none_may_run_is_a_fail() {
  setup_case asked
  sandbox_args
  RUN111_ENV=(FAKE111_ASK=1)
  run111
  expect_status "$status" 1 "picker where none may run"
  expect_contains "$out" "FACT picker-3: FAIL — the picker ran where it must not: Billing account (a number, or" "picker where none may run: mode 3's own row"
  expect_contains "$out" "FACT picker-4: PASS — " "picker where none may run: --json's run asks nothing"
  expect_contains "$out" "FACT picker-5: PASS — " "picker where none may run: and neither does an agent's session"
  expect_contains "$out" "FACT leak-3: PASS — " "picker where none may run: a prompt is not a leak"
  expect_contains "$out" "CHECK 111 RESULT: FAIL" "picker where none may run: the verdict"
  expect_scratch_gone "$dir" "picker where none may run"
}

# Criterion 3 reads four forms of a leak, and each of these four cases carries
# exactly one of them in mode 3 — so a detector that is deleted or inverted
# fails its own case instead of hiding behind the others.
test_criterion_3_detects_an_account_resource_id() {
  setup_case leakid
  sandbox_args
  RUN111_ENV=(FAKE111_LEAK_ID=1)
  run111
  expect_status "$status" 1 "resource id"
  expect_contains "$out" "FACT leak-3: FAIL — a leak: an account resource id: billingAccounts/AAAAAA-BBBBBB-CCCCCC" "resource id: the API's own resource form"
  expect_absent "$out" "a display name of yours: $DISPLAY_NAME" "resource id: no display name was read"
  expect_absent "$out" "numbered line" "resource id: and no numbered line was counted"
  expect_contains "$out" "CHECK 111 RESULT: FAIL" "resource id: the verdict"
  expect_scratch_gone "$dir" "resource id"
}

test_criterion_3_detects_one_of_the_owners_ids() {
  setup_case leakbareid
  printf 'AAAAAA-BBBBBB-CCCCCC\n' >"$dir/ids.txt"
  sandbox_args
  RUN111_ARGS+=(--account-ids "$dir/ids.txt")
  RUN111_ENV=(FAKE111_LEAK_BARE_ID=1)
  run111
  expect_status "$status" 1 "one of your ids"
  expect_contains "$out" "FACT account-ids: PASS — $dir/ids.txt (1 id(s)" "one of your ids: --account-ids was read"
  expect_contains "$out" "FACT leak-3: FAIL — a leak: one of YOUR account ids: AAAAAA-BBBBBB-CCCCCC" "one of your ids: the ids file's own grep"
  expect_absent "$out" "an account resource id:" "one of your ids: a bare id is not the API's resource form, so that detector must not claim it"
  expect_contains "$out" "CHECK 111 RESULT: FAIL" "one of your ids: the verdict"
  expect_scratch_gone "$dir" "one of your ids"
}

test_criterion_3_detects_a_display_name() {
  setup_case leakname
  sandbox_args
  RUN111_ENV=(FAKE111_LEAK_NAME=1)
  run111
  expect_status "$status" 1 "display name"
  expect_contains "$out" "FACT leak-3: FAIL — a leak: a display name of yours: $DISPLAY_NAME" "display name: the names file's own grep"
  expect_absent "$out" "an account resource id:" "display name: no id was read"
  expect_absent "$out" "numbered line" "display name: and no numbered line was counted"
  expect_contains "$out" "CHECK 111 RESULT: FAIL" "display name: the verdict"
  expect_scratch_gone "$dir" "display name"
}

# The numbered-line detector is the one that reads a list of anything: what it
# counts is not the account's own name or id, so it is pinned by a line that
# carries neither.
test_criterion_3_detects_a_numbered_line() {
  setup_case leaknumbered
  sandbox_args
  RUN111_ENV=(FAKE111_LEAK_NUMBERED=1)
  run111
  expect_status "$status" 1 "numbered line"
  expect_contains "$out" "FACT leak-3: FAIL — the capture carries 1 numbered line(s)" "numbered line: the numbered-line detector"
  expect_absent "$out" "FACT leak-3: FAIL — a leak:" "numbered line: neither an id nor a name was read"
  expect_contains "$out" "FACT leak-4: PASS — " "numbered line: mode 3 alone carried it"
  expect_contains "$out" "CHECK 111 RESULT: FAIL" "numbered line: the verdict"
  expect_scratch_gone "$dir" "numbered line"
}

# --account-ids that names a file which is not there stops the run before a tool
# is called: without it the ids cannot be grepped for, and a check that cannot
# read one of its own criterion-3 detectors must not run.
test_a_missing_ids_file_stops() {
  setup_case idsmissing
  sandbox_args
  RUN111_ARGS+=(--account-ids "$dir/no-such-ids.txt")
  run111
  expect_status "$status" 2 "missing ids file"
  [ -s "$dir/calls" ] && fail "missing ids file: a tool was called: $(tr '\n' ';' <"$dir/calls")"
  expect_contains "$(cat "$dir/err")" "does not exist or is empty" "missing ids file: the stop says which file"
  expect_absent "$out" "CHECK 111 RESULT: PASS" "missing ids file: no verdict"
  expect_scratch_gone "$dir" "missing ids file"
}

# An unrun mode is not a pass: with --modes naming one run, the other six read
# FAIL — not run and the verdict is FAIL, whatever the one run that did run said.
test_an_unrun_mode_is_not_a_pass() {
  setup_case unrun
  sandbox_args
  RUN111_ARGS+=(--modes 1a)
  run111
  expect_status "$status" 1 "unrun mode"
  expect_contains "$out" "FACT offer-1a: PASS — " "unrun mode: the run that was selected passed"
  expect_contains "$out" "FACT run-1b: FAIL — not run" "unrun mode: a mode the run did not select is not a pass"
  expect_contains "$out" "FACT run-5: FAIL — not run" "unrun mode: and neither is the last of them"
  expect_contains "$out" "CHECK 111 RESULT: FAIL" "unrun mode: the verdict"
  expect_scratch_gone "$dir" "unrun mode"
}

# Mode 2's premise is an API that IS enabled on S: with it off the mode takes
# mode 1's disabled-API path and proves nothing, so it is recorded as not run —
# the check's own Fails-when, and the one thing that must never read as a pass.
test_mode_2s_premise_missing_is_not_run() {
  setup_case nopremise
  sandbox_args
  rm -f "$dir/$S.enabled"
  run111
  expect_status "$status" 1 "mode 2's premise"
  expect_contains "$out" "FACT premise-2a: FAIL — not run" "mode 2's premise: mode 2(a) is not run"
  expect_contains "$out" "FACT run-2b: FAIL — not run" "mode 2's premise: nor mode 2(b)"
  expect_contains "$out" "FACT offer-1a: PASS — " "mode 2's premise: the modes that do not need it still ran"
  expect_contains "$out" "CHECK 111 RESULT: FAIL" "mode 2's premise: the verdict"
  expect_scratch_gone "$dir" "mode 2's premise"
}

# A run that blocks waiting for an answer it can never get is a FAIL of that
# mode, not a hang: every run is capped (CHECK111_RUN_SECONDS), the runner kills
# the run at the cap, and the modes around it and the Restore carry on.
test_a_blocking_run_is_a_fail() {
  setup_case blocked
  sandbox_args
  RUN111_ENV=(FAKE111_HANG=1)
  CHECK111_TEST_RUN_SECONDS=5 run111
  expect_status "$status" 1 "blocking run"
  expect_contains "$out" "FACT run-1a: FAIL — " "blocking run: the mode that blocked is not a pass"
  expect_contains "$out" "was killed" "blocking run: and the row says the cap is what ended it"
  expect_contains "$out" "FACT run-1a-yes: FAIL — " "blocking run: the rerun under --yes hit the same cap"
  expect_contains "$out" "CHECK 111 RESULT: FAIL" "blocking run: the verdict"
  expect_contains "$out" "FACT restore: PASS — " "blocking run: the Restore still ran"
  expect_changed "enable $P" "blocking run: the API mode 1(a) disabled is enabled again"
  expect_scratch_gone "$dir" "blocking run"
}

# The three throwaways have opposite premises — Q is the quota project mode
# 1(b) disables the API on, S is the one mode 2 needs enabled — so naming one of
# them twice would make a mode's premise another's opposite. Refused by name,
# before a tool is called.
test_projects_that_collide_are_refused() {
  local what
  for what in off-is-project on-is-project on-is-off off-is-belong; do
    setup_case "collide-$what"
    case "$what" in
    off-is-project) RUN111_ARGS=(--api-off-project "$P" --api-on-project "$S") ;;
    on-is-project) RUN111_ARGS=(--api-off-project "$Q" --api-on-project "$P") ;;
    on-is-off) RUN111_ARGS=(--api-off-project "$Q" --api-on-project "$Q") ;;
    off-is-belong) RUN111_ARGS=(--api-off-project belong --api-on-project "$S") ;;
    esac
    run111
    expect_status "$status" 2 "collide $what"
    [ -s "$dir/calls" ] && fail "collide $what: a tool was called before the guard: $(tr '\n' ';' <"$dir/calls")"
    expect_absent "$out" "CHECK 111 RESULT: PASS" "collide $what: no verdict"
    expect_scratch_gone "$dir" "collide $what"
  done
}

# A dry run runs nothing at all: no gcloud call, no wizard, no verdict — a
# verdict is about the runs that happened, and a dry run's block would read as
# one. What it does is print the plan and its own local readings.
test_dry_run_runs_nothing() {
  setup_case dry
  sandbox_args
  RUN111_ARGS+=(--dry-run)
  run111
  expect_status "$status" 0 "dry run"
  [ -s "$dir/calls" ] && fail "dry run: something was called: $(tr '\n' ';' <"$dir/calls")"
  expect_contains "$(cat "$dir/err")" "dry run: NOTHING below was run" "dry run: the plan says so"
  expect_contains "$(cat "$dir/err")" "gcloud services disable cloudbilling.googleapis.com --project=$P --force" "dry run: the plan names the commands a real run would run"
  expect_contains "$(cat "$dir/err")" "set-quota-project $OWN_QUOTA" "dry run: and the Restore's own write"
  expect_contains "$(cat "$dir/err")" "cp -p $dir/second-adc.json over $dir/cloudsdk/application_default_credentials.json" "dry run: the plan says which login mode 2's runs would use, and where it would go"
  expect_contains "$(cat "$dir/err")" "your own ADC file back over" "dry run: and that the Restore would put your own file back"
  expect_contains "$out" "CHECK 111 DRY RUN: nothing was run" "dry run: the block carries no verdict"
  expect_absent "$out" "CHECK 111 RESULT: PASS" "dry run: no verdict"
  expect_absent "$out" "CHECK 111 RESULT: FAIL" "dry run: no verdict either way"
  expect_contains "$out" "FACT dry-run: PASS — " "dry run: recorded as its own row"
  expect_scratch_gone "$dir" "dry run"
}

# The belong project is refused by NAME, before anything is called: this check
# writes in three projects of the owner's, and belong is not one of them.
test_belong_is_refused() {
  local bad
  for bad in belong belong-fugaro fugaro-dev edge-devel-dimi; do
    setup_case "belong$bad"
    RUN111_ARGS=(--project "$bad")
    run111
    expect_status "$status" 2 "belong $bad"
    [ -s "$dir/calls" ] && fail "belong $bad: a tool was called before the guard: $(tr '\n' ';' <"$dir/calls")"
    expect_contains "$(cat "$dir/err")" "belong" "belong $bad: the stop says which project it is"
    expect_contains "$out" "FACT aborted: FAIL — " "belong $bad: the block says the run was refused"
    expect_absent "$out" "CHECK 111 RESULT: PASS" "belong $bad: and carries no verdict"
    expect_scratch_gone "$dir" "belong $bad"
  done
}

# The guard that keeps every mode honest: this script must not be run BY an
# agent, because an agent's session is one of the five modes it observes — a
# shell carrying a marker would make mode 5's premise the shell itself.
test_the_agent_marker_is_refused() {
  setup_case agent
  sandbox_args
  RUN111_ENV=(CLAUDECODE=1)
  run111
  expect_status "$status" 2 "agent marker"
  [ -s "$dir/calls" ] && fail "agent marker: a tool was called: $(tr '\n' ';' <"$dir/calls")"
  expect_contains "$(cat "$dir/err")" "agent" "agent marker: the stop says why"
  expect_scratch_gone "$dir" "agent marker"
}

# Without a quota project to put back, the Restore could not be complete, so the
# run stops before it changes anything.
test_no_adc_quota_project_stops() {
  setup_case noadc
  sandbox_args
  printf '{"type": "authorized_user"}\n' >"$dir/cloudsdk/application_default_credentials.json"
  run111
  expect_status "$status" 2 "no ADC quota project"
  [ -s "$dir/calls" ] && fail "no ADC quota project: a tool was called: $(tr '\n' ';' <"$dir/calls")"
  expect_contains "$(cat "$dir/err")" "quota project" "no ADC quota project: the stop says what is missing"
  expect_scratch_gone "$dir" "no ADC quota project"
}

# A trace would put the accounts mode 2(a)'s fallback lists into a trace file.
test_xtrace_is_refused() {
  setup_case xtrace
  sandbox_args
  RUN111_XTRACE=1
  run111
  expect_status "$status" 2 "xtrace"
  [ -s "$dir/calls" ] && fail "xtrace: a tool was called: $(tr '\n' ';' <"$dir/calls")"
  expect_contains "$out" "FACT aborted: FAIL — " "xtrace: the block says the run was refused"
  expect_scratch_gone "$dir" "xtrace"
}

test_usage() {
  local what
  for what in "no project" "no names file" "unknown flag"; do
    setup_case "usage-${what// /-}"
    case "$what" in
    "no project") RUN111_ARGS=() ;;
    "no names file") RUN111_ARGS=(--project "$P") ;;
    "unknown flag") RUN111_ARGS=(--project "$P" --not-a-flag) ;;
    esac
    # run111 always passes --project and --account-names; the cases that must
    # fail on a missing one call the script directly instead.
    if [ "$what" = "no project" ] || [ "$what" = "no names file" ]; then
      status=0
      out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
        -u CURSOR_AGENT -u AI_AGENT -u CLAUDE_CODE_SSE_PORT -u XDG_CONFIG_HOME \
        TMPDIR="$dir/tmp" FAKE111_DIR="$dir" CLOUDSDK_CONFIG="$dir/cloudsdk" \
        PATH="$dir/bin:$PATH" \
        bash "$script" ${RUN111_ARGS[@]+"${RUN111_ARGS[@]}"} 2>"$dir/err") || status=$?
    else
      run111
    fi
    expect_status "$status" 2 "usage: $what"
    [ -s "$dir/calls" ] && fail "usage: $what: a tool was called before the arguments were read"
    expect_contains "$(cat "$dir/err")" "usage:" "usage: $what: the stop prints the usage line"
    expect_absent "$out" "CHECK 111 RESULT: PASS" "usage: $what: no verdict"
  done
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

t test_all_seven_runs_pass
t test_a_failed_assertion_still_restores
t test_a_leak_is_a_fail
t test_a_picker_where_none_may_run_is_a_fail
t test_criterion_3_detects_an_account_resource_id
t test_criterion_3_detects_one_of_the_owners_ids
t test_criterion_3_detects_a_display_name
t test_criterion_3_detects_a_numbered_line
t test_a_missing_ids_file_stops
t test_an_unrun_mode_is_not_a_pass
t test_mode_2s_premise_missing_is_not_run
t test_only_mode_2_runs_under_the_second_login
t test_the_second_login_is_put_back_when_a_mode_stops
t test_a_machine_left_on_the_second_login_is_refused
t test_a_second_account_for_every_mode_is_refused
t test_second_adc_guards
t test_a_quota_project_the_adc_cannot_use_stops
t test_a_blocking_run_is_a_fail
t test_projects_that_collide_are_refused
t test_dry_run_runs_nothing
t test_belong_is_refused
t test_the_agent_marker_is_refused
t test_no_adc_quota_project_stops
t test_xtrace_is_refused
t test_usage

if [ "$failures" -ne 0 ]; then
  echo "check111_test: $failures failure(s)" >&2
  exit 1
fi
echo "check111_test: all tests passed" >&2
