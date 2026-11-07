#!/usr/bin/env bash
# Tests for scripts/live-checks/check130.sh, against a fake curl on PATH and a
# throwaway checkout this test makes: nothing here reaches the network, there is
# no cloud project and no credential, and no step of the live check is run.
#
# The fake curl answers the three anonymous GETs the check makes — the
# repository's metadata, the licence endpoint's status, the licence object — from
# $FAKE130_DIR, and records every call there, so a test says what the check read
# by writing files and can tell a run that fetched from one that never did. The
# fake gcloud and fugaro log any call and fail: whatever this check does, it
# never reaches a cloud tool, and that is one of the assertions.
#
# The repository the check reads is not typed into the script and no variable
# can name it: it is the checkout's origin, cross-checked against go.mod. The
# fixture checkout here is therefore a REAL git repository (`git init` plus
# `remote add origin`) with a go.mod in it, made fresh per case — a fake `git`
# would test the fixture instead of the guard.
#
# Usage: bash scripts/live-checks/check130_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check130.sh
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK130_TEST_KEEP:-}" ]; then
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
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT (aborted|setup|repository|belong|licensing-md|links|licence-api|restore):' | sed 's/^/ ... last run: /')"
}

# The repository the fixture checkout claims to be: the one this check exists
# for, so the happy path reads as the real run does.
REPO=dimipaun/fugaro
ORIGIN=https://github.com/${REPO}.git
MODULE=github.com/${REPO}
BRANCH=main

# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(setup repository belong licensing-md links licence-api restore)

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env python3
# A fake curl for check130.sh's test: it answers the three anonymous GETs the
# check makes out of $FAKE130_DIR and reaches nothing. Every call is appended to
# $FAKE130_DIR/calls, argv verbatim, so a test can tell a run that fetched the
# three answers from one that fetched none.
import json
import os
import sys

state = os.environ["FAKE130_DIR"]
mode = os.environ.get("FAKE130_MODE", "ok")
args = sys.argv[1:]
with open(os.path.join(state, "calls"), "a", encoding="utf-8") as fh:
    fh.write("curl " + " ".join(args) + "\n")


def flag(name):
    return args[args.index(name) + 1] if name in args else None


url = args[-1]
out = flag("-o")
fail_on_status = "-f" in args

if url.endswith("/license"):
    if mode == "rate-limited":
        code, body = 429, {"message": "API rate limit exceeded for 1.2.3.4."}
    elif mode == "not-found":
        code, body = 404, {"message": "Not Found"}
    elif mode == "broken":
        code, body = 200, "this answer is not JSON at all"
    else:
        # The detector read the statement file rather than the untouched
        # LICENSE: the two readings the check's fails-when clause names, on one
        # body, since the path is the half that decides it.
        # The two readings the check's fails-when clause names are separate
        # modes: `other` is the licence GitHub reports when it classified a file
        # it does not recognise, and `read-the-statement` is a clean MIT with the
        # path pointed at the new statement file. The path is the half a reader
        # would otherwise skip, so it gets a mode of its own.
        read_statement = mode == "read-the-statement"
        spdx = "other" if mode == "other" else "MIT"
        body = {
            "path": "LICENSING.md" if read_statement else "LICENSE",
            "license": {
                "key": "other" if spdx == "other" else "mit",
                "name": "Other" if spdx == "other" else "MIT License",
                "spdx_id": spdx,
                "node_id": "MDc6TGljZW5zZTEz",
                "url": "https://api.github.com/licenses/mit",
            },
            "message": None,
        }
        code = 200
else:
    if mode == "meta-not-found":
        code, body = 404, {"message": "Not Found"}
    elif mode == "meta-broken":
        code, body = 200, "this answer is not JSON at all"
    elif mode == "meta-no-branch":
        code, body = 200, {"full_name": "acme/webapp", "private": False}
    else:
        code = 200
        body = {
            "full_name": "acme/webapp",
            "default_branch": "main",
            "private": False,
        }

if fail_on_status and code >= 400:
    print("curl: (22) The requested URL returned error: %d" % code, file=sys.stderr)
    sys.exit(22)

if out:
    if flag("-w") == "%{http_code}":
        sys.stdout.write(str(code))
else:
    sys.stdout.write(body if isinstance(body, str) else json.dumps(body))
    sys.stdout.write("\n")
CURL_EOF

  # A cloud tool this check must never reach, whatever the case: any call is
  # recorded and fails the fake, and the tests assert the record is empty.
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
printf 'gcloud %s\n' "$*" >>"$FAKE130_DIR/gcloud.calls"
echo 'fake gcloud: check130 must not reach a cloud tool' >&2
exit 64
GCLOUD_EOF
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
printf 'fugaro %s\n' "$*" >>"$FAKE130_DIR/fugaro.calls"
echo 'fake fugaro: check130 must not reach an installation' >&2
exit 64
FUGARO_EOF

  chmod +x "$1/bin/curl" "$1/bin/gcloud" "$1/bin/fugaro"
}

# ------------------------------------------------------------------ the fixture
# write_checkout dir ORIGIN MODULE [LICENSING] — a real git checkout whose
# origin and go.mod the guard reads. ORIGIN `none` leaves the remote off (the
# guard's stop), MODULE is what go.mod carries (a different module path is the
# guard's cross-check), and LICENSING is `default`, `none` or `nolink`.
write_checkout() { # dir ORIGIN MODULE [LICENSING]
  local dir=$1 origin=$2 module=$3 licensing=${4:-default}
  mkdir -p "$dir/checkout"
  git -C "$dir/checkout" init -q
  printf 'module %s\n\ngo 1.24\n' "$module" >"$dir/checkout/go.mod"
  case "$licensing" in
  none) : ;;
  nolink)
    cat >"$dir/checkout/LICENSING.md" <<'MD'
# Licensing

This statement carries no link at all.
MD
    ;;
  *)
    cat >"$dir/checkout/LICENSING.md" <<'MD'
# Licensing

The subscription code is not open source. The open-source terms are in
[LICENSE](LICENSE); what the subscription buys is in
[the enterprise directory](ee/README.md), and its terms are in
[the enterprise licence](ee/LICENSE).

The agent skill files are a separate case, under
[.agents/LICENSE](.agents/LICENSE), and a subscription is licensed under the
[Fugaro Subscription terms](LICENSES/LicenseRef-Fugaro-Subscription.txt).

The name and the marks are not licensed by any of this; see
[the trademarks section](README.md#trademarks).
MD
    ;;
  esac
  [ "$origin" = none ] || git -C "$dir/checkout" remote add origin "$origin"
}

# setup_case NAME [ORIGIN] [MODULE] [LICENSING] — a case's own directory, its
# fakes and its fixture checkout.
setup_case() { # NAME [ORIGIN] [MODULE] [LICENSING]
  local name=$1
  dir="$work/$name"
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp" "$dir/home"
  write_fakes "$dir"
  : >"$dir/state/calls"
  : >"$dir/state/gcloud.calls"
  : >"$dir/state/fugaro.calls"
  write_checkout "$dir" "${2:-$ORIGIN}" "${3:-$MODULE}" "${4:-default}"
}

# run130 [ARGS...] — runs the check on the case at hand, from the case's
# checkout. Sets $status, $out and $dir/err. RUN130_MODE is which answer the fake
# curl gives (`ok` by default, so a case that says nothing reads the happy path),
# RUN130_DIR runs the check from somewhere else (a directory that is not a git
# checkout at all), and RUN130_PATH prefixes PATH (for a fake tool that has to
# come first). All three are cleared after each run: a mode that outlived its
# case would make the next case read a different answer than it wrote.
RUN130_MODE=""
RUN130_DIR=""
RUN130_PATH=""
run130() {
  status=0
  (cd "${RUN130_DIR:-$dir/checkout}" &&
    exec env TMPDIR="$dir/tmp" FAKE130_DIR="$dir/state" \
      FAKE130_MODE="${RUN130_MODE:-ok}" HOME="$dir/home" \
      PATH="${RUN130_PATH:+$RUN130_PATH:}$dir/bin:$PATH" bash "$script" "$@") >"$dir/out" 2>"$dir/err" || status=$?
  RUN130_MODE=""
  RUN130_DIR=""
  RUN130_PATH=""
  out=$(cat "$dir/out")
}

# write_failing_rm dir — a `rm` that fails, first on PATH. The exit trap's
# `rm -rf` on the scratch directory is the one thing this check can leave
# behind, and the `restore` row is where it reports that it could not: the row
# exists for a machine where that removal fails (a read-only parent, a file
# somebody else holds), and the block and the exit status have to agree about
# it. A fake failing tool is the only way to reach that path without breaking
# the test's own machine.
write_failing_rm() { # dir
  mkdir -p "$1/norm"
  cat >"$1/norm/rm" <<'RM_EOF'
#!/usr/bin/env bash
printf 'rm %s\n' "$*" >>"$FAKE130_DIR/rm.calls"
exit 1
RM_EOF
  chmod +x "$1/norm/rm"
}

# ---------------------------------------------------------------- the assertions
expect_block_keys() { # what
  local key
  expect_contains "$out" "CHECK 130 RESULT" "$1: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$out" "FACT $key:" "$1: the $key row"
  done
}

expect_fetched() { # n what
  local n
  n=$(grep -c '^curl ' "$dir/state/calls" 2>/dev/null || true)
  [ "$n" = "$1" ] || fail "$2: expected $1 curl call(s), saw $n: $(tr '\n' ';' <"$dir/state/calls")"
}

expect_fetched_nothing() { # what
  [ ! -s "$dir/state/calls" ] \
    || fail "$1: a request was made where none may be: $(tr '\n' ';' <"$dir/state/calls")"
}

expect_no_cloud_tool() { # what
  [ ! -s "$dir/state/gcloud.calls" ] || fail "$1: gcloud was called: $(tr '\n' ';' <"$dir/state/gcloud.calls")"
  [ ! -s "$dir/state/fugaro.calls" ] || fail "$1: fugaro was called: $(tr '\n' ';' <"$dir/state/fugaro.calls")"
}

scratch_of() { # dir — the scratch directory the run announced
  sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$1/err" | head -n 1
}

expect_scratch_gone() { # what
  local scratch
  scratch=$(scratch_of "$dir")
  if [ -z "$scratch" ]; then
    fail "$1: the script did not say where its scratch directory is"
    return 0
  fi
  [ -d "$scratch" ] && fail "$1: the scratch directory $scratch survived the run"
  [ -z "$(find "$dir/tmp" -mindepth 1 2>/dev/null)" ] || fail "$1: files were left in TMPDIR: $(find "$dir/tmp" -mindepth 1)"
  return 0
}

# ------------------------------------------------------------------- the tests
test_happy_path() {
  setup_case happy
  run130
  expect_status "$status" 0 "happy path"
  expect_block_keys "happy path"
  expect_contains "$out" "FACT setup: PASS — CHECK 130 SETUP: repository ${REPO}, default branch ${BRANCH}, licence API HTTP 200" "happy path: the setup line the paste-back asks for"
  expect_contains "$out" "FACT repository: PASS — github.com/${REPO}" "happy path: the repository is the checkout's origin"
  expect_contains "$out" "$ORIGIN" "happy path: the origin it read it from"
  expect_contains "$out" "$MODULE" "happy path: the go.mod module path it cross-checked against"
  expect_contains "$out" "FACT licensing-md: PASS — " "happy path: LICENSING.md is in the tree"
  expect_contains "$out" "FACT links: PASS — 6 relative link(s)" "happy path: the links read out of LICENSING.md"
  expect_contains "$out" "LICENSES/LicenseRef-Fugaro-Subscription.txt" "happy path: a nested path among them"
  expect_contains "$out" "README.md#trademarks" "happy path: the fragment among them"
  expect_contains "$out" 'FACT licence-api: PASS — {"license":{"key":"mit","name":"MIT License"' "happy path: the object verbatim, not a summary"
  expect_contains "$out" '"path":"LICENSE"' "happy path: the path the detector classified"
  expect_contains "$out" '"spdx_id":"MIT"' "happy path: the licence it reported"
  expect_contains "$out" "FACT restore: PASS — " "happy path: the restore row"
  expect_contains "$out" "FACT xtrace: PASS — " "happy path: the xtrace guard ran"
  expect_contains "$out" "CHECK 130 RESULT: PASS" "happy path: the verdict"
  expect_contains "$out" "MANUAL: open https://github.com/${REPO} and read the About box's licence label VERBATIM" "happy path: the one manual line, step 1"
  expect_contains "$out" "https://github.com/${REPO}/blob/${BRANCH}/LICENSING.md" "happy path: and step 3, on the branch the API reported"
  expect_contains "$out" "Paste back, with this block:" "happy path: the exact text to paste back"
  # The three anonymous GETs, and no fourth: the check's own blocks run three.
  expect_fetched 3 "happy path"
  expect_contains "$(cat "$dir/state/calls")" "curl -sS -f https://api.github.com/repos/${REPO}" "happy path: the metadata read"
  expect_contains "$(cat "$dir/state/calls")" "curl -sS -H Accept: application/vnd.github+json https://api.github.com/repos/${REPO}/license" "happy path: the object read, with the API's own Accept header"
  expect_no_cloud_tool "happy path"
  expect_scratch_gone "happy path"
  expect_absent "$out" "FACT aborted:" "happy path"
}

test_belong_is_refused() {
  # The owner's own installation, both spellings: the repository name alone and
  # the organisation with the repository.
  setup_case belong-name "https://github.com/acme/belong.git" "github.com/acme/belong"
  run130
  expect_status "$status" 2 "belong by name"
  expect_contains "$out" "FACT belong: FAIL — " "belong by name: the row"
  expect_contains "$out" "acme/belong" "belong by name: the repository it refused"
  expect_contains "$out" "EdgeWeb or EdgeServer" "belong by name: what else is refused"
  expect_contains "$out" "CHECK 130 RESULT: FAIL" "belong by name: the verdict"
  expect_fetched_nothing "belong by name"
  expect_no_cloud_tool "belong by name"
  expect_scratch_gone "belong by name"

  setup_case belong-installation "git@github.com:EdgeWeb/EdgeServer.git" "github.com/EdgeWeb/EdgeServer"
  run130
  expect_status "$status" 2 "belong by installation"
  expect_contains "$out" "FACT belong: FAIL — " "belong by installation: the row, whatever case the origin is spelled in"
  expect_contains "$out" "EdgeWeb/EdgeServer" "belong by installation: the repository it refused"
  expect_fetched_nothing "belong by installation"
  expect_scratch_gone "belong by installation"
}

test_dry_run_runs_nothing() {
  setup_case dry
  run130 --dry-run
  expect_status "$status" 0 "dry run"
  expect_block_keys "dry run"
  expect_fetched_nothing "dry run"
  expect_contains "$out" "FACT dry-run: PASS — guards and the link list only" "dry run: the row"
  expect_contains "$out" "FACT setup: INFO — not taken: this is a dry run" "dry run: nothing was fetched, and the row says so rather than reading as a pass"
  expect_contains "$out" "FACT licence-api: INFO — not taken: this is a dry run" "dry run: and the API reading it did not take is not a FAIL"
  expect_absent "$out" "FACT licence-api: FAIL" "dry run"
  expect_contains "$out" "CHECK 130 RESULT: PASS" "dry run: the verdict"
  # The plan is on stderr: what a real run would run, in order, and the two
  # browser readings as the one MANUAL line.
  expect_contains "$(cat "$dir/err")" "dry run: NOTHING below was run, and no request left this machine" "dry run: the plan's own heading"
  expect_contains "$(cat "$dir/err")" "curl -sS -f https://api.github.com/repos/${REPO}" "dry run: the metadata read it would make"
  expect_contains "$(cat "$dir/err")" "https://api.github.com/repos/${REPO}/license" "dry run: the licence read it would make"
  expect_contains "$(cat "$dir/err")" "[your browser] step 1: https://github.com/${REPO}" "dry run: the browser reading it would ask for"
  expect_contains "$out" "MANUAL: open https://github.com/${REPO}" "dry run: the manual line is still the one line"
  expect_no_cloud_tool "dry run"
  expect_scratch_gone "dry run"
}

test_a_detector_that_read_the_statement_is_a_fail() {
  setup_case read-statement
  RUN130_MODE=read-the-statement run130
  expect_status "$status" 1 "read the statement"
  expect_contains "$out" "FACT licence-api: FAIL — LICENSING.md / spdx_id MIT / key mit" "read the statement: the mismatch the row reports"
  expect_contains "$out" '"path":"LICENSING.md"' "read the statement: the object verbatim"
  expect_contains "$out" "means the detector read the new statement file rather than the untouched LICENSE" "read the statement: the reading the section gives it"
  expect_contains "$out" "do not fix it by editing LICENSE" "read the statement: the check does not fix it"
  expect_contains "$out" "CHECK 130 RESULT: FAIL" "read the statement: the verdict"
  expect_contains "$out" "FACT restore: PASS — " "read the statement: teardown still ran"
  expect_scratch_gone "read the statement"
}

test_a_licence_endpoint_that_answers_404_is_a_fail() {
  setup_case not-found
  RUN130_MODE=not-found run130
  expect_status "$status" 1 "404"
  expect_contains "$out" "FACT setup: PASS — CHECK 130 SETUP: repository ${REPO}, default branch ${BRANCH}, licence API HTTP 404" "404: the setup line records the code"
  expect_contains "$out" "FACT licence-api: FAIL — the licence endpoint answered HTTP 404" "404: the finding, not a stop"
  expect_contains "$out" "'Not Found'" "404: the message it carried in place of both"
  expect_contains "$out" "no longer the one LICENSE carries" "404: the section's fails-when clause"
  expect_contains "$out" "CHECK 130 RESULT: FAIL" "404: the verdict"
  expect_scratch_gone "404"
}

test_every_guard_stops() {
  # A guard's stop is exit 2 and a FAILed row: this check has no way to pass a
  # criterion it did not measure.
  setup_case module-disagrees "https://github.com/acme/other.git" "github.com/acme/webapp"
  run130
  expect_status "$status" 2 "module disagrees"
  expect_contains "$out" "FACT aborted: FAIL — Check 130 stopped: go.mod's module path is github.com/acme/webapp and this checkout's origin is acme/other" "module disagrees: the stop"
  expect_fetched_nothing "module disagrees"

  setup_case no-origin none
  run130
  expect_status "$status" 2 "no origin"
  expect_contains "$out" "FACT aborted: FAIL — Check 130 stopped: this checkout has no origin remote" "no origin: the stop"
  expect_fetched_nothing "no origin"

  setup_case not-github "https://gitlab.com/acme/webapp.git"
  run130
  expect_status "$status" 2 "not github"
  expect_contains "$out" "names no github.com repository" "not github: the stop"
  expect_fetched_nothing "not github"

  setup_case no-licensing "$ORIGIN" "$MODULE" none
  run130
  expect_status "$status" 2 "no LICENSING.md"
  expect_contains "$out" "FACT aborted: FAIL — Check 130 stopped: this tree has no LICENSING.md" "no LICENSING.md: the stop, naming the task that writes it"
  expect_contains "$out" "task L1.11" "no LICENSING.md: and the task that re-runs it"
  expect_fetched_nothing "no LICENSING.md"

  setup_case no-links "$ORIGIN" "$MODULE" nolink
  run130
  expect_status "$status" 2 "no link in LICENSING.md"
  expect_contains "$out" "carries no link at all" "no link: the stop, rather than a browser step with nothing to follow"
  expect_fetched_nothing "no link"

  setup_case rate-limited
  RUN130_MODE=rate-limited run130
  expect_status "$status" 2 "rate limited"
  expect_contains "$out" "the licence endpoint answered 429" "rate limited: the stop"
  expect_contains "$out" "this check holds no credential and needs none" "rate limited: what the owner is told to do"
  expect_contains "$out" "CHECK 130 RESULT: FAIL" "rate limited: the verdict is not a pass"

  setup_case meta-not-found
  RUN130_MODE=meta-not-found run130
  expect_status "$status" 2 "metadata 404"
  expect_contains "$out" "api.github.com did not answer for ${REPO}" "metadata 404: the stop"

  setup_case meta-broken
  RUN130_MODE=meta-broken run130
  expect_status "$status" 2 "metadata not JSON"
  expect_contains "$out" "something that is not the repository JSON object" "metadata not JSON: the stop, not a traceback"

  setup_case meta-no-branch
  RUN130_MODE=meta-no-branch run130
  expect_status "$status" 2 "no default branch"
  expect_contains "$out" "no default_branch" "no default branch: the stop, since step 3's URL needs one"

  setup_case broken-answer
  RUN130_MODE=broken run130
  expect_status "$status" 2 "licence answer not JSON"
  expect_contains "$out" "something that is not the JSON object it answers with" "licence answer not JSON: the stop"
  expect_absent "$(cat "$dir/err")" "Traceback" "licence answer not JSON: no python traceback reads like a broken check"

  setup_case not-a-checkout
  mkdir -p "$dir/plain"
  RUN130_DIR="$dir/plain" run130
  expect_status "$status" 2 "not a checkout"
  expect_contains "$out" "FACT aborted: FAIL — Check 130 stopped: this is not a git checkout" "not a checkout: the stop"
  expect_fetched_nothing "not a checkout"
}

test_the_block_shape_is_fixed_on_a_stopped_run() {
  setup_case stopped-shape
  RUN130_MODE=meta-not-found run130
  expect_status "$status" 2 "stopped run"
  expect_block_keys "stopped run"
  expect_contains "$out" "FACT setup: FAIL — not reached" "stopped run: a row it never reached reads not reached"
  expect_contains "$out" "FACT licence-api: FAIL — not reached" "stopped run: and so does the reading it stopped before"
  expect_contains "$out" "FACT belong: PASS — " "stopped run: the guards it did run keep their readings"
  expect_contains "$out" "FACT restore: PASS — " "stopped run: the scratch directory is still removed"
  expect_contains "$out" "MANUAL: not reached" "stopped run: and the manual line says the browser steps were not measured"
  expect_contains "$out" "CHECK 130 RESULT: FAIL" "stopped run: a stop is not a pass"
  expect_scratch_gone "stopped run"
}

test_teardown_runs_on_a_failed_assertion() {
  setup_case other-licence
  RUN130_MODE=other run130
  expect_status "$status" 1 "failed assertion"
  expect_contains "$out" 'FACT licence-api: FAIL — LICENSE / spdx_id other / key other' "failed assertion: the row"
  expect_contains "$out" "spdx_id of other or NOASSERTION" "failed assertion: the reading the section gives it"
  expect_contains "$out" 'FACT restore: PASS — this check created nothing and changed nothing anywhere' "failed assertion: the restore row is printed and it passed"
  # The teardown is the exit trap, not a step: it runs after the failure, which
  # is the whole point of asserting it here.
  expect_contains "$out" "its scratch directory is gone" "failed assertion: the scratch directory is named as removed"
  expect_scratch_gone "failed assertion"
}

test_a_restore_that_failed_is_a_fail() {
  setup_case restore-failed
  write_failing_rm "$dir"
  RUN130_PATH="$dir/norm" run130
  # Every row the check measured passed; the one that did not is the restore.
  # The table's contract is that one FAILed row is exit 1, so a block whose last
  # line is FAIL may not sit next to an exit status of 0.
  expect_status "$status" 1 "restore failed"
  expect_contains "$out" "FACT licence-api: PASS — " "restore failed: the rows it did measure passed"
  expect_contains "$out" "FACT restore: FAIL — " "restore failed: the row"
  expect_contains "$out" "is STILL THERE" "restore failed: the row names the directory left behind"
  expect_contains "$out" "remove it yourself" "restore failed: and tells the owner to"
  expect_contains "$out" "CHECK 130 RESULT: FAIL" "restore failed: the verdict"
  expect_contains "$(cat "$dir/state/rm.calls")" "rm -rf" "restore failed: the trap did try"
  # The block's last line is the verdict, and it is the one the exit status
  # agrees with.
  last=$(printf '%s\n' "$out" | tail -n 1)
  expect_contains "$last" "CHECK 130 RESULT: FAIL" "restore failed: the block's last line"
  # The directory the row says is still there really is: this run is the one
  # case whose scratch outlives it, and the test removes it.
  scratch=$(scratch_of "$dir")
  [ -n "$scratch" ] && [ -d "$scratch" ] || fail "restore failed: the run said it could not remove $scratch, and nothing is there to have failed on"
  rm -rf "$scratch"
}

test_xtrace_is_refused() {
  setup_case xtrace
  status=0
  (cd "$dir/checkout" &&
    exec env TMPDIR="$dir/tmp" FAKE130_DIR="$dir/state" HOME="$dir/home" \
      PATH="$dir/bin:$PATH" bash -x "$script") >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  expect_status "$status" 2 "xtrace"
  expect_contains "$out" "FACT xtrace: FAIL — " "xtrace: the row"
  expect_contains "$out" "this shell is tracing (bash -x)" "xtrace: the stop, naming the flag"
  expect_contains "$out" "CHECK 130 RESULT: FAIL" "xtrace: the verdict"
  expect_fetched_nothing "xtrace"
  expect_scratch_gone "xtrace"
}

test_usage() {
  setup_case usage
  run130 --help
  expect_status "$status" 0 "help"
  expect_contains "$out" "usage: check130.sh [--dry-run]" "help: the usage line"

  run130 --nonsense
  expect_status "$status" 2 "unknown argument"
  expect_contains "$out" "FACT aborted: FAIL" "unknown argument: a fact, so the block is still printable"
  expect_contains "$out" "unknown argument '--nonsense'" "unknown argument: the argument it names"
  expect_fetched_nothing "unknown argument"
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

t test_happy_path
t test_belong_is_refused
t test_dry_run_runs_nothing
t test_a_detector_that_read_the_statement_is_a_fail
t test_a_licence_endpoint_that_answers_404_is_a_fail
t test_every_guard_stops
t test_the_block_shape_is_fixed_on_a_stopped_run
t test_teardown_runs_on_a_failed_assertion
t test_a_restore_that_failed_is_a_fail
t test_xtrace_is_refused
t test_usage

if [ "$failures" -ne 0 ]; then
  echo "check130_test: $failures failure(s)" >&2
  exit 1
fi
echo "check130_test: all tests passed" >&2
