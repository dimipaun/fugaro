#!/usr/bin/env bash
# Tests for scripts/live-checks/check574.sh, with a fake `gcloud` and a fake
# `fugaro` on PATH: NOTHING here reaches a cloud, a project, a keyring or a
# released build, no agent runs a live command, and both fakes record every call
# they are given — the argv, the directory it was made in and whether
# FUGARO_LICENCE_KEY was in that child's environment — so a case can assert what
# the check read, where it read it and which child the key was handed to.
#
# Check 574 measures a build, and a released build exists only where it is
# installed: the only way to measure its rows is against a fake that answers the
# way the build does, including the refusal it gives a key whose id it does not
# hold. The fake `fugaro` models exactly four things — `version`, `doctor --json`,
# `licence status --json` and `licence set --file` — and its verifier is the one
# rule the check's rows rest on: a key whose payload is not the bytes the fixture
# signed does not verify, and a key whose id is not in `$FAKE574_DIR/build-ring`
# is refused for its id alone. `build-ring` is absent by default, which is the
# state a build of this task is in (ee/register's licenceRing is empty until S2.9
# publishes the owner's production public halves) — and writing it is how a case
# says "this build holds an id it must not".
#
# Usage: bash scripts/live-checks/check574_test.sh
#        CHECK_TEST_ONLY=test_happy_path bash scripts/live-checks/check574_test.sh
#        CHECK_TEST_SHARD=0/3 bash scripts/live-checks/check574_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check574.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK574_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK574_TEST_HEAD:-1600}")" ;;
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
expect_row() { # key needle what
  local row
  row=$(printf '%s\n' "$out" | grep -m1 "^FACT $1: " || true)
  if [ -z "$row" ]; then
    fail "$3: the block carries no FACT $1 row"
    return 0
  fi
  expect_contains "$row" "$2" "$3"
}
assert_scratch_removed() { # what
  [ -n "$scratch" ] || {
    fail "$2: the check named no scratch directory"
    return 0
  }
  [ ! -e "$scratch" ] || fail "$2: the scratch directory $scratch is still there"
}

project=chk574-deploy-proj
sandbox_project=chk574-sandbox-proj
ring=fugaro-licence-staging
kms_key=fugaro-licence-staging
staging_id=fugaro-licence-staging
production_id=fugaro-licence-production

# --------------------------------------------------------------------- fakes
write_fake_gcloud() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for scripts/live-checks/check574_test.sh only: it
# answers each read out of $FAKE574_DIR and records the call. A document that is
# not there exits 1 with an error the way a missing resource does; `<name>.exit`
# makes the call fail with the status a case picked. Nothing here talks to a
# cloud, and a keyring it prints is a case's own file.
set -euo pipefail
dir=${FAKE574_DIR:?}
mkdir -p "$dir/logs"
printf 'gcloud %s (in %s)\n' "$*" "$PWD" >>"$dir/logs/gcloud.calls"

# A copy of the arguments with the flags that are not part of a document's
# identity dropped: which file answers a call must not depend on the order a flag
# was passed in.
args=()
while [ $# -gt 0 ]; do
  case "$1" in
  --format=*) shift ;;
  --keyring=* | --location=* | --project=*) shift ;;
  --keyring | --location | --project | --filter) shift 2 ;;
  *)
    args+=("$1")
    shift
    ;;
  esac
done
case "${args[0]:-}" in
--version) name=gcloud-version ;;
config) name=account ;;
projects) name=project ;;
kms)
  case "${args[1]:-}" in
  keys)
    if [ "${args[2]:-}" = describe ]; then name=kms-key; else name=kms-key-versions; fi
    ;;
  versions) name=kms-key-versions ;;
  *)
    echo "fake gcloud: no route for '$*'" >&2
    exit 7
    ;;
  esac
  ;;
*)
  echo "fake gcloud: no route for '$*'" >&2
  exit 7
  ;;
esac
if [ -f "$dir/$name.exit" ]; then
  [ -f "$dir/$name.err" ] && cat "$dir/$name.err" >&2
  exit "$(cat "$dir/$name.exit")"
fi
if [ ! -f "$dir/$name.json" ] && [ ! -f "$dir/$name.txt" ]; then
  echo "ERROR: (gcloud) NOT_FOUND: $name was not found" >&2
  exit 1
fi
if [ -f "$dir/$name.txt" ]; then cat "$dir/$name.txt"; else cat "$dir/$name.json"; fi
GCLOUD_EOF
  chmod +x "$1/bin/gcloud"
}

write_fake_fugaro() { # dir
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A stand-in for the released `fugaro`, for scripts/live-checks/check574_test.sh
# only. It records every call with the directory it was made in and whether
# FUGARO_LICENCE_KEY was in ITS OWN environment: the key is handed over through
# the environment and never on a command line (F41), and a case asserts exactly
# that. Four verbs are modelled and nothing else is: `version`, `doctor --json`,
# `licence status --json` and `licence set --file`.
set -euo pipefail
dir=${FAKE574_DIR:?}
mkdir -p "$dir/logs"
handed=${FUGARO_LICENCE_KEY:-}
if [ -n "$handed" ]; then held=yes; else held=no; fi
printf 'fugaro %s (in %s) envkey=%s\n' "$*" "$PWD" "$held" >>"$dir/logs/fugaro.calls"

case "${1:-}" in
version)
  printf 'fugaro v0.0.0-chk574\n'
  exit 0
  ;;
doctor)
  if [ -f "$dir/doctor.exit" ]; then
    [ -f "$dir/doctor.err" ] && cat "$dir/doctor.err" >&2
    exit "$(cat "$dir/doctor.exit")"
  fi
  if [ -f "$dir/doctor.txt" ]; then cat "$dir/doctor.txt"; else printf '{}\n'; fi
  exit 0
  ;;
licence)
  case "${2:-}" in
  status)
    if [ -n "$handed" ]; then
      # The read the console's hand-off makes: the key is verified against the
      # ring this build holds, and the answer is the verifier's own.
      if [ -f "$dir/handed-refusal.txt" ]; then
        cat "$dir/handed-refusal.txt" >&2
        exit "$(cat "$dir/handed-refusal.exit" 2>/dev/null || echo 1)"
      fi
      python3 - "$dir" <<'PY'
import base64, json, os, sys

dir = sys.argv[1]
text = os.environ.get("FUGARO_LICENCE_KEY", "").strip()
parts = text[4:].split(".")
try:
    held = open(os.path.join(dir, "build-ring")).read().strip()
except OSError:
    held = ""
try:
    kid = json.loads(base64.urlsafe_b64decode(parts[0] + "=" * (-len(parts[0]) % 4))).get("kid", "")
except Exception:
    kid = ""
if kid != held:
    print("the project's licence object (fugaro/licence.json) holds a key this Fugaro does not accept, and nothing was written: licence: the key id is not in the keyring", file=sys.stderr)
    raise SystemExit(1)
signed = ""
try:
    signed = open(os.path.join(dir, "key-payload")).read().strip()
except OSError:
    pass
if parts[0] != signed:
    print("the project's licence object (fugaro/licence.json) holds a key this Fugaro does not accept, and nothing was written: licence: the signature does not verify", file=sys.stderr)
    raise SystemExit(1)
print(json.dumps({"project": "chk574", "state": "active", "source": "machine",
                  "tier": "team", "seats": 5, "kind": "paid", "renewal_mode": "auto"},
                 indent=2))
PY
      exit 0
    fi
    if [ -f "$dir/status.exit" ]; then
      [ -f "$dir/status.err" ] && cat "$dir/status.err" >&2
      exit "$(cat "$dir/status.exit")"
    fi
    if [ -f "$dir/status.json" ]; then cat "$dir/status.json"; else printf '{"project":"chk574","state":"free","tier":"","source":"","seats":0}\n'; fi
    exit 0
    ;;
  set)
    if [ -f "$dir/set-refusal.txt" ]; then
      cat "$dir/set-refusal.txt" >&2
      exit "$(cat "$dir/set-refusal.exit" 2>/dev/null || echo 1)"
    fi
    # The same verifier as the status route, over the key the file holds — which
    # is what makes the install path's row a reading of the ring — plus
    # `set-accepts`, the bend for a build that installs a key it should refuse.
    path=""
    args=("$@")
    for i in "${!args[@]}"; do
      if [ "${args[$i]}" = --file ] && [ $((i + 1)) -lt ${#args[@]} ]; then path=${args[$((i + 1))]}; fi
    done
    kid=$(chk574-key-kid "$path")
    held=""
    if [ -f "$dir/set-accepts" ]; then
      held=$kid
    elif [ -f "$dir/build-ring" ]; then
      held=$(cat "$dir/build-ring")
    fi
    if [ -n "$kid" ] && [ "$kid" = "$held" ]; then
      # The fault this check exists to find: a build that installs the key.
      printf 'key installed in project chk574 (fugaro/licence.json)\n'
      if [ -f "$dir/set-writes-status" ]; then
        cp "$dir/set-writes-status" "$dir/status.json"
      else
        printf '{"project":"chk574","state":"active","tier":"team","source":"project","seats":5}\n' >"$dir/status.json"
      fi
      exit 0
    fi
    printf 'the key was not accepted, and nothing was written: licence: the key id is not in the keyring\n' >&2
    exit 1
    ;;
  *)
    echo "fake fugaro: no route for '$*'" >&2
    exit 7
    ;;
  esac
  ;;
*)
  echo "fake fugaro: no route for '$*'" >&2
  exit 7
  ;;
esac
FUGARO_EOF
  cat >"$1/bin/chk574-key-kid" <<'KID_EOF'
#!/usr/bin/env python3
# The id a licence key's payload names, for the fake `fugaro` above: the key's own
# claim, read the way the build reads it, and nothing else of the text.
import base64, json, sys

try:
    text = open(sys.argv[1]).read().strip()
    payload = text[4:].split(".")[0]
    print(json.loads(base64.urlsafe_b64decode(payload + "=" * (-len(payload) % 4))).get("kid", ""))
except Exception:
    print("")
KID_EOF
  chmod +x "$1/bin/fugaro" "$1/bin/chk574-key-kid"
}

# ------------------------------------------------------------------ the fixture
# write_key DIR KID: the one-line key file a case hands the check, and the payload
# text the fixture "signed" — the fake build verifies a handed key by comparing
# its payload with those exact bytes, which is how a real signature is over the
# payload's raw bytes. The file is 0600 and holds one line, as the check requires.
write_key() { # write_key DIR KID
  python3 - "$2" "$1/key.txt" "$1/key-payload" <<'PY'
import base64, json, sys

kid, path, payload_path = sys.argv[1], sys.argv[2], sys.argv[3]
claims = {
    "v": 1,
    "kid": kid,
    "workspace_id": "chk574-workspace",
    "tier": "team",
    "kind": "paid",
    "seats": 5,
    "issued_at": "2026-11-01T00:00:00Z",
    "paid_through": "2027-11-01T00:00:00Z",
    "grace_days": 30,
    "renewal_mode": "auto",
    "renewal_secret_hash": "0" * 64,
}
payload = json.dumps(claims, separators=(",", ":")).encode()
payload_text = base64.urlsafe_b64encode(payload).decode().rstrip("=")
signature = base64.urlsafe_b64encode(bytes(range(64))).decode().rstrip("=")
with open(path, "w") as out:
    out.write("fgk_%s.%s\n" % (payload_text, signature))
with open(payload_path, "w") as out:
    out.write(payload_text + "\n")
PY
  chmod 600 "$1/key.txt"
}

# prepare DIR: the state every row holds — a project whose ring holds the staging
# signing key, a throwaway installation whose licence reads free, and a key file
# signed for the staging id. `build-ring` is deliberately absent: the released
# build of this task holds no id at all, which is what the control row reads.
prepare() { # dir
  local dir=$1
  mkdir -p "$dir/bin" "$dir/logs" "$dir/tmp" "$dir/home" "$dir/sandbox"
  write_fake_gcloud "$dir"
  write_fake_fugaro "$dir"
  python3 - >"$dir/gcloud-version.json" <<'PY'
import json
print(json.dumps({"Google Cloud SDK": "500.0.0-chk574"}))
PY
  printf 'owner@fugaro.test\n' >"$dir/account.txt"
  python3 - "$project" >"$dir/project.json" <<'PY'
import json, sys
print(json.dumps({"projectId": sys.argv[1], "projectNumber": "574574574574",
                  "lifecycleState": "ACTIVE", "name": "chk574"}))
PY
  python3 - "$project" "$ring" "$kms_key" >"$dir/kms-key.json" <<'PY'
import json, sys
project, ring, key = sys.argv[1], sys.argv[2], sys.argv[3]
base = "projects/%s/locations/us-east5/keyRings/%s/cryptoKeys/%s" % (project, ring, key)
print(json.dumps({
    "name": base,
    "purpose": "ASYMMETRIC_SIGN",
    "versionTemplate": {"algorithm": "EC_SIGN_ED25519", "protectionLevel": "SOFTWARE"},
    "primary": {"name": base + "/cryptoKeyVersions/1", "state": "ENABLED"},
}))
PY
  python3 - "$project" "$ring" "$kms_key" >"$dir/kms-key-versions.json" <<'PY'
import json, sys
project, ring, key = sys.argv[1], sys.argv[2], sys.argv[3]
base = "projects/%s/locations/us-east5/keyRings/%s/cryptoKeys/%s" % (project, ring, key)
print(json.dumps([{"name": base + "/cryptoKeyVersions/1", "state": "ENABLED"}]))
PY
  python3 - "$sandbox_project" >"$dir/doctor.txt" <<'PY'
import json, sys
print(json.dumps({"project": {"name": "chk574-sandbox", "gcp_project": sys.argv[1], "region": "us-east5"}}))
PY
  printf '{"project":"chk574","state":"free","tier":"","source":"","seats":0}\n' >"$dir/status.json"
  write_key "$dir" "$staging_id"
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
  mkdir -p "$dir/tmp" "$dir/home" "$dir/logs"
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/fugaro.calls"
  status=0
  # `-x` is bash's own flag, so it goes BEFORE the script: the check refuses an
  # argument it does not know, and only a traced interpreter reaches the guard.
  local bashargs=()
  case "${cli[0]:-}" in
  -x | --xtrace)
    bashargs=(-x)
    cli=("${cli[@]:1}")
    ;;
  esac
  # The agent markers are dropped from this test's own environment: the suite that
  # runs this file may itself be started from an agent's session, and the case
  # that measures that refusal sets the marker again.
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
    PATH="$dir/bin:$PATH" HOME="$dir/home" TMPDIR="$dir/tmp" \
    FAKE574_DIR="$dir" \
    ${args[@]+"${args[@]}"} \
    bash ${bashargs[@]+"${bashargs[@]}"} "$script" \
    --project "$project" --sandbox "$dir/sandbox" --key-file "$dir/key.txt" \
    ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
  scratch=$(sed -n 's/^check574\.sh: scratch directory: \([^ ]*\).*/\1/p' "$dir/err" | head -n 1)
  if [ -n "${CHECK574_TEST_DUMP:-}" ]; then printf -- '--- %s (exit %s)\n%s\n' "$dir" "$status" "$out" >&2; fi
}

# Sharding, so a Go test could run these side by side: CHECK_TEST_SHARD="i/n"
# runs every n-th test starting at i, CHECK_TEST_ONLY=name runs that one alone.
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

# --------------------------------------------------------------- the cases
test_help() {
  local dir=$work/help
  prepare "$dir"
  run_check "$dir" -- --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Usage:" "--help: the usage line"
  expect_contains "$out" "check574.sh --project" "--help: the command it documents"
  expect_absent "$out" "FACT " "--help: no block is printed"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "--help: nothing is read"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro" "--help: no build is driven"
  expect_contains "$out" "is refused by a production" "--help: what it settles"
  expect_contains "$out" "THROWAWAY installation" "--help: which checkout it drives"
  expect_contains "$out" "NEVER PRINTED" "--help: what it never prints"
}

test_dry_run() {
  local dir=$work/dry
  prepare "$dir"
  run_check "$dir" CHECK574_DRY_RUN=1
  expect_status "$status" 0 "dry run"
  expect_contains "$out" "FACT dry-run: PASS" "dry run: the marker row"
  expect_contains "$out" "CHECK 574 RESULT END (exit 0)" "dry run: the end line"
  expect_contains "$err" "kms keys describe $kms_key --keyring=$ring --location=us-east5 --project=$project" "dry run: the plan names the ring read"
  expect_contains "$err" "would write IF THE BUILD IS WRONG" "dry run: the plan says which command could write"
  expect_contains "$err" "payload's id rewritten to $production_id" "dry run: the plan names the control"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "dry run: no call is made"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro" "dry run: no build is driven"
  assert_scratch_removed "dry run"
}

test_happy_path() {
  local dir=$work/happy
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "happy path"
  expect_row gcloud "500.0.0-chk574" "happy path: the gcloud row names the SDK"
  expect_row gcloud "$project (number 574574574574" "happy path: the gcloud row names the project"
  expect_row ring "purpose ASYMMETRIC_SIGN, algorithm EC_SIGN_ED25519, 1 ENABLED version(s)" "happy path: the ring row holds the terraform's own key"
  expect_row ring "$kms_key" "happy path: the ring row names the key id"
  expect_row key "mode 0600" "happy path: the key row reads the file"
  expect_row key "for id $staging_id" "happy path: the key row names the id"
  expect_row key "renewal-secret digest present" "happy path: the key row names the digest"
  expect_row key "tier team, kind paid, renewal auto" "happy path: the key row carries the claims"
  expect_row sandbox "chk574-sandbox in $sandbox_project (region us-east5)" "happy path: the sandbox row names the installation"
  expect_row version "v0.0.0-chk574" "happy path: the version row names the build"
  expect_row before "reads free" "happy path: the installation reads free"
  expect_row refusal "refused the staging key for its id alone, on the read path" "happy path: the read path refuses it"
  expect_row install "refused the staging key on the install path too" "happy path: the install path refuses it"
  expect_row control "refused the rewritten key for its id too" "happy path: the control reads the empty ring"
  expect_row control "ring holds NEITHER id" "happy path: the control says what that means"
  expect_row after "still reads 'free|" "happy path: the installation did not move"
  expect_row restore "nothing of this check's own is left" "happy path: nothing to undo"
  # The key is handed to the two children that need it — the read that must refuse
  # it and the control — and to no other, and it is never a word on a command line
  # (F41: a key in argv is a key every local process reads).
  local calls handed
  calls=$(cat "$dir/logs/fugaro.calls")
  expect_contains "$calls" "licence status --json (in $dir/sandbox) envkey=yes" "happy path: the key is handed over for the read that must refuse it"
  expect_contains "$calls" "doctor --json (in $dir/sandbox) envkey=no" "happy path: no key is handed to doctor"
  expect_contains "$calls" "licence set --file $dir/key.txt (in $dir/sandbox) envkey=no" "happy path: no key is handed to the install path"
  expect_contains "$calls" "version (in $dir/sandbox) envkey=no" "happy path: no key is handed to version"
  expect_contains "$calls" "licence status --json (in $dir/sandbox) envkey=no" "happy path: the two reads of the installation's own licence carry no key"
  handed=$(grep -c 'envkey=yes' "$dir/logs/fugaro.calls" || true)
  [ "$handed" = 2 ] || fail "happy path: the key was handed to $handed child(ren), want 2 (the read that refuses it, and the control)"
  expect_absent "$calls" "fgk_" "happy path: the key text is never on a command line"
  expect_absent "$out$err" "fgk_" "happy path: the key text is never printed"
  assert_scratch_removed "happy path"
}

test_the_key_runs_in_the_sandbox() {
  # `licence status` and `licence set` both read the installation they are run in,
  # so every CLI call is made from the checkout the check was given.
  local dir=$work/cwd
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the sandbox is where the build runs"
  expect_contains "$(cat "$dir/logs/fugaro.calls")" "(in $dir/sandbox)" "the sandbox: the build ran in the checkout"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "(in $(pwd))" "the sandbox: the build did not run in this test's own directory"
}

test_a_build_that_holds_the_other_id_is_read_as_having_the_id_fence() {
  # The control's other reading: a ring that holds the OTHER environment's id
  # refuses the rewrite for its SIGNATURE, which is what makes the refusal above
  # the id's rather than an empty ring's.
  local dir=$work/other-id
  prepare "$dir"
  printf '%s\n' "$production_id" >"$dir/build-ring"
  run_check "$dir"
  expect_status "$status" 0 "a build that holds the production id"
  expect_row refusal "refused the staging key for its id alone" "a build that holds the production id: the staging key is still refused for its id"
  expect_row control "refused for its SIGNATURE" "a build that holds the production id: the control reads the fence"
  expect_row control "ring HOLDS that id" "a build that holds the production id: the control says what that means"
  expect_row after "still reads 'free|" "a build that holds the production id: nothing was written"
}

test_a_build_that_holds_the_staging_id_is_a_finding() {
  # F17/D459's fault: a ring that holds the staging id accepts a staging key. The
  # check must say so on both paths, and the after row is what says whether the
  # install path wrote anything — a build that installs the key moves the
  # installation, and the check's own before/after pair is the proof.
  local dir=$work/staging-held
  prepare "$dir"
  printf '%s\n' "$staging_id" >"$dir/build-ring"
  run_check "$dir"
  expect_status "$status" 1 "a build that holds the staging id"
  expect_row refusal "FAIL" "a build that holds the staging id: the read path is a finding"
  expect_row refusal "ACCEPTED the staging key" "a build that holds the staging id: the read path says what happened"
  expect_row refusal "has accepted a key F17/D459 keeps out of it" "a build that holds the staging id: the read path names the rule"
  expect_row install "FAIL" "a build that holds the staging id: the install path is a finding"
  expect_row install "INSTALLED the staging key" "a build that holds the staging id: the install path says what happened"
  expect_row after "FAIL" "a build that holds the staging id: the installation moved"
  expect_row after "the only one of them that can write is \`licence set\`" "a build that holds the staging id: the after row names the command that did"
}

test_an_install_that_writes_is_a_finding() {
  # The install path that installs although nothing about the ring says it may:
  # `set-accepts` is the bend, and the state the fake writes is what the after read
  # finds. This is the row that makes "nothing was written" a reading rather than
  # an assumption.
  local dir=$work/install-writes
  prepare "$dir"
  printf 'x\n' >"$dir/set-accepts"
  run_check "$dir"
  expect_status "$status" 1 "an install that writes"
  expect_row install "FAIL" "an install that writes: the install row"
  expect_row install "INSTALLED the staging key" "an install that writes: the install row says what happened"
  expect_row after "FAIL" "an install that writes: the licence moved"
  expect_row after "read 'free||' before the run and 'active|project|team' after it" "an install that writes: the after row carries both states"
}

test_a_refusal_that_is_not_the_ids_is_a_finding() {
  # A verifier refusal that is not the id's is only reachable once the build has
  # ALREADY HELD the staging id — the id is read before the signature, the claims
  # and the workspace — so it is the same fault with a different sentence.
  local dir=$work/not-the-id
  prepare "$dir"
  printf '%s\n' "$staging_id" >"$dir/build-ring"
  printf 'the project'"'"'s licence object (fugaro/licence.json) holds a key this Fugaro does not accept, and nothing was written: licence: the key is bound to a different workspace\n' >"$dir/handed-refusal.txt"
  printf 'the key was not accepted, and nothing was written: licence: the key is bound to a different workspace\n' >"$dir/set-refusal.txt"
  run_check "$dir"
  expect_status "$status" 1 "a refusal that is not the id's"
  expect_row refusal "FAIL" "a refusal that is not the id's: the read path is a finding"
  expect_row refusal "but NOT for its id" "a refusal that is not the id's: the read path says so"
  expect_row refusal "already held the staging id $staging_id" "a refusal that is not the id's: the row says why that is the fault"
  expect_row install "but NOT for its id" "a refusal that is not the id's: the install path says so too"
}

test_the_ring_is_read_where_the_terraform_puts_it() {
  # D459's spelling: the ring and the key are the environment's own names, read in
  # the project --project names, at the keyrings' location.
  local dir=$work/ring-read
  prepare "$dir"
  run_check "$dir"
  expect_status "$status" 0 "the ring read"
  local calls
  calls=$(cat "$dir/logs/gcloud.calls")
  expect_contains "$calls" "kms keys describe $kms_key --keyring=$ring --location=us-east5 --project=$project" "the ring read: describe names all four"
  expect_contains "$calls" "kms keys versions list --key=$kms_key --keyring=$ring --location=us-east5 --project=$project --format=json --filter=state=ENABLED" "the ring read: the versions are filtered to the ENABLED ones"
}

test_a_ring_that_is_not_a_signing_key_is_a_finding() {
  local dir=$work/not-signing
  prepare "$dir"
  python3 - "$dir/kms-key.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
doc["purpose"] = "ENCRYPT_DECRYPT"
doc["versionTemplate"] = {"algorithm": "GOOGLE_SYMMETRIC_ENCRYPTION"}
json.dump(doc, open(sys.argv[1], "w"))
PY
  run_check "$dir"
  expect_status "$status" 1 "a ring that does not hold the signing key"
  expect_row ring "FAIL" "a ring that does not hold the signing key: the ring row"
  expect_row ring "purpose ENCRYPT_DECRYPT" "a ring that does not hold the signing key: the row says what is there"
  expect_row refusal "FAIL — not reached" "a ring that does not hold the signing key: the run stops before the build"
}

test_a_ring_the_project_does_not_hold_is_unmeasured() {
  local dir=$work/ring-missing
  prepare "$dir"
  rm -f "$dir/kms-key.json"
  run_check "$dir"
  expect_status "$status" 2 "a ring the project does not hold"
  expect_row ring "FAIL" "a ring the project does not hold: the row is unmeasured"
  expect_row ring "did not answer: ERROR: (gcloud) NOT_FOUND" "a ring the project does not hold: the row says so"
}

test_a_gcloud_that_is_not_authenticated_is_unmeasured() {
  local dir=$work/no-account
  prepare "$dir"
  printf '(unset)\n' >"$dir/account.txt"
  run_check "$dir"
  expect_status "$status" 2 "gcloud with no account"
  expect_row gcloud "gcloud has no authenticated account" "gcloud with no account: the row"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro" "gcloud with no account: no build is driven"
}

test_a_key_file_that_is_not_0600_is_unmeasured() {
  local dir=$work/key-mode
  prepare "$dir"
  chmod 644 "$dir/key.txt"
  run_check "$dir"
  expect_status "$status" 2 "a key file any local user can read"
  expect_row key "FAIL" "a key file any local user can read: the key row"
  expect_row key "mode 644" "a key file any local user can read: the row names the mode"
  expect_row key "chmod 600" "a key file any local user can read: the row says what to do"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "licence" "a key file any local user can read: the build is never driven with it"
}

test_a_key_file_with_a_second_line_is_unmeasured() {
  # The file the check takes is ONE line — the key. A second line is a renewal
  # secret, which this check has no business handing over.
  local dir=$work/key-two-lines
  prepare "$dir"
  printf 'a renewal secret this check never reads\n' >>"$dir/key.txt"
  run_check "$dir"
  expect_status "$status" 2 "a key file with a second line"
  expect_row key "the file holds 2 non-empty line(s)" "a key file with a second line: the row says why"
}

test_a_key_of_another_id_is_unmeasured() {
  # A key of another id is a different measurement: the id is what a production
  # build refuses a staging key for.
  local dir=$work/key-other-id
  prepare "$dir"
  write_key "$dir" "$production_id"
  run_check "$dir"
  expect_status "$status" 2 "a key of the production id"
  expect_row key "names the id '$production_id'" "a key of the production id: the row says which id"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "licence" "a key of the production id: the build is never driven with it"
}

test_a_text_that_is_not_a_v1_key_is_unmeasured() {
  local dir=$work/key-malformed
  prepare "$dir"
  printf 'not a licence key at all\n' >"$dir/key.txt"
  chmod 600 "$dir/key.txt"
  run_check "$dir"
  expect_status "$status" 2 "a text that is not a v1 key"
  expect_row key "does not start with the fgk_ prefix" "a text that is not a v1 key: the row says why"
}

test_a_sandbox_whose_licence_is_not_free_is_refused() {
  # The row the check drives is `licence set`: a checkout that already holds a
  # licence is one it must not touch, and a working installation's licence is not
  # what it is for.
  local dir=$work/licensed
  prepare "$dir"
  printf '{"project":"chk574","state":"active","tier":"team","source":"project","seats":5}\n' >"$dir/status.json"
  run_check "$dir"
  expect_status "$status" 2 "a checkout whose licence is not free"
  expect_row before "FAIL" "a checkout whose licence is not free: the before row"
  expect_row before "reads 'active'" "a checkout whose licence is not free: the row names the state"
  expect_row before "THROWAWAY installation" "a checkout whose licence is not free: the row says what to point at"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "licence set" "a checkout whose licence is not free: nothing is installed"
}

test_a_sandbox_without_a_firebase_project_is_unmeasured() {
  # `licence status` and `licence set` both refuse for a workspace before they
  # read a key: that is a reading of the checkout, not of the ring.
  local dir=$work/no-workspace
  prepare "$dir"
  printf 'chk574 has no Firebase project (budget.firebase_project is not set in its project config), so a licence key, which is bound to that project'"'"'s id, cannot be read or installed here: an operator runs `fugaro update --firebase-project chk574 --name chk574`\n' >"$dir/handed-refusal.txt"
  printf 'chk574 has no Firebase project (budget.firebase_project is not set in its project config), so a licence key, which is bound to that project'"'"'s id, cannot be read or installed here: an operator runs `fugaro update --firebase-project chk574 --name chk574`\n' >"$dir/set-refusal.txt"
  run_check "$dir"
  expect_status "$status" 2 "a sandbox with no Firebase project"
  expect_row refusal "has no Firebase project" "a sandbox with no Firebase project: the row says why"
  expect_row refusal "rather than about the ring" "a sandbox with no Firebase project: the row places the refusal"
}

test_an_ambient_key_is_refused() {
  # A shell that already carries FUGARO_LICENCE_KEY would hand it to every call
  # below, including the two that must read the installation's own licence.
  local dir=$work/ambient
  prepare "$dir"
  run_check "$dir" FUGARO_LICENCE_KEY=a-key-this-check-did-not-read
  expect_status "$status" 1 "an ambient key"
  expect_row ambient "FAIL" "an ambient key: the row"
  expect_row ambient "FUGARO_LICENCE_KEY is set in this shell" "an ambient key: the row says what to unset"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "an ambient key: nothing is read"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro" "an ambient key: no build is driven"
}

test_a_sandbox_that_is_not_a_directory_is_refused() {
  local dir=$work/no-sandbox
  prepare "$dir"
  run_check "$dir" -- --sandbox "$dir/not-a-checkout"
  expect_status "$status" 2 "a sandbox that is not a directory"
  expect_contains "$err" "--sandbox is not a directory" "a sandbox that is not a directory: the refusal"
}

test_a_checkout_that_is_not_an_installations_is_unmeasured() {
  local dir=$work/no-project
  prepare "$dir"
  printf '{}\n' >"$dir/doctor.txt"
  run_check "$dir"
  expect_status "$status" 2 "a checkout that is not an installation's"
  expect_row sandbox "answers no project" "a checkout that is not an installation's: the row"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "licence" "a checkout that is not an installation's: no licence is read"
}

test_a_build_that_does_not_answer_doctor_is_unmeasured() {
  local dir=$work/doctor-fails
  prepare "$dir"
  printf '2\n' >"$dir/doctor.exit"
  printf 'the build could not reach the project\n' >"$dir/doctor.err"
  run_check "$dir"
  expect_status "$status" 2 "a build that does not answer doctor"
  expect_row sandbox "exited 2" "a build that does not answer doctor: the row names the status"
}

test_fugaro_prod_is_refused_without_production() {
  local dir=$work/prod
  prepare "$dir"
  run_check "$dir" -- --project fugaro-prod
  expect_status "$status" 1 "fugaro-prod without --production"
  expect_contains "$err" "fugaro-prod, which is refused unless --production is given" "fugaro-prod: the refusal"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "fugaro-prod: nothing is read"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro" "fugaro-prod: no build is driven"
}

test_fugaro_prod_with_production_is_measured() {
  local dir=$work/prod-by-choice
  prepare "$dir"
  run_check "$dir" -- --project fugaro-prod --production --dry-run
  expect_status "$status" 0 "fugaro-prod with --production"
  expect_row project "production, named with --production" "fugaro-prod: the row"
  expect_contains "$err" "nothing is written: no key, no secret, no object" "fugaro-prod: the plan says what it does not write"
}

test_a_project_naming_belong_is_refused() {
  local dir=$work/belong
  prepare "$dir"
  run_check "$dir" -- --project the-belong-project
  expect_status "$status" 1 "a project naming belong"
  expect_row belong "FAIL" "a project naming belong: the row"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "a project naming belong: nothing is read"
}

test_an_agent_session_is_refused() {
  local dir=$work/agent
  prepare "$dir"
  run_check "$dir" CLAUDECODE=1
  expect_status "$status" 1 "an agent's session"
  expect_row agent-session "FAIL" "an agent's session: the row"
  expect_row agent-session "CLAUDECODE is set" "an agent's session: the row names the marker"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "gcloud" "an agent's session: nothing is read"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro" "an agent's session: no build is driven"
}

test_a_traced_shell_is_refused() {
  local dir=$work/xtrace
  prepare "$dir"
  run_check "$dir" -- --xtrace
  expect_status "$status" 1 "a traced shell"
  expect_row xtrace "FAIL" "a traced shell: the row"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro" "a traced shell: no build is driven"
}

test_a_kept_scratch_is_reported() {
  local dir=$work/kept
  prepare "$dir"
  run_check "$dir" CHECK574_KEEP_SCRATCH=1
  expect_status "$status" 0 "a kept scratch"
  expect_row scratch "the scratch directory was kept" "a kept scratch: the row"
  [ -d "$scratch" ] || fail "a kept scratch: the directory is gone"
  rm -rf "$scratch"
}

test_a_fugaro_that_is_not_there_is_refused() {
  local dir=$work/no-cli
  prepare "$dir"
  run_check "$dir" -- --fugaro "$dir/bin/not-fugaro"
  expect_status "$status" 2 "a build that is not there"
  expect_contains "$err" "is not an executable file" "a build that is not there: the refusal"
}

test_an_unknown_argument_is_refused() {
  local dir=$work/arg
  prepare "$dir"
  run_check "$dir" -- --not-a-flag
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument '--not-a-flag'" "an unknown argument: the refusal"
}

t test_help
t test_dry_run
t test_happy_path
t test_the_key_runs_in_the_sandbox
t test_a_build_that_holds_the_other_id_is_read_as_having_the_id_fence
t test_a_build_that_holds_the_staging_id_is_a_finding
t test_an_install_that_writes_is_a_finding
t test_a_refusal_that_is_not_the_ids_is_a_finding
t test_the_ring_is_read_where_the_terraform_puts_it
t test_a_ring_that_is_not_a_signing_key_is_a_finding
t test_a_ring_the_project_does_not_hold_is_unmeasured
t test_a_gcloud_that_is_not_authenticated_is_unmeasured
t test_a_key_file_that_is_not_0600_is_unmeasured
t test_a_key_file_with_a_second_line_is_unmeasured
t test_a_key_of_another_id_is_unmeasured
t test_a_text_that_is_not_a_v1_key_is_unmeasured
t test_a_sandbox_whose_licence_is_not_free_is_refused
t test_a_sandbox_without_a_firebase_project_is_unmeasured
t test_an_ambient_key_is_refused
t test_a_sandbox_that_is_not_a_directory_is_refused
t test_a_checkout_that_is_not_an_installations_is_unmeasured
t test_a_build_that_does_not_answer_doctor_is_unmeasured
t test_fugaro_prod_is_refused_without_production
t test_fugaro_prod_with_production_is_measured
t test_a_project_naming_belong_is_refused
t test_an_agent_session_is_refused
t test_a_traced_shell_is_refused
t test_a_kept_scratch_is_reported
t test_a_fugaro_that_is_not_there_is_refused
t test_an_unknown_argument_is_refused

if [ "$failures" -ne 0 ]; then
  echo "check574_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check574_test.sh: ok"
