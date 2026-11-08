#!/usr/bin/env bash
# Tests for scripts/live-checks/check300.sh, against a fake gcloud, a fake
# fugaro and a fake RTDB behind a fake curl: nothing here reaches the network,
# there is no cloud state, and the access token the fake gcloud prints is a
# marker value that must never reach the block or the log.
#
# The fake curl is a small RTDB: a JSON store this test seeds, where a call
# carrying an Authorization header is IAM (admitted, rules bypassed) and a call
# carrying none is a client the rules evaluate (refused 401 Permission denied,
# unless a case plants the flag that makes the fake admit it). The rules
# document the check reads is the REAL generated one
# (internal/budget/rules/testdata/rules.golden.json), so the subtree's shape is
# the shape this release deploys and not a fixture written to agree with the
# reader.
#
# Usage: bash scripts/live-checks/check300_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check300.sh
# By absolute path, because the missing-tools case replaces PATH with a
# directory that holds nothing and `env` would then find no bash at all.
BASH_BIN=$(command -v bash)
golden=$here/../../internal/budget/rules/testdata/rules.golden.json
# The PHYSICAL path, so a path this test compares and a path a child reports are
# spelled the same way.
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK300_TEST_KEEP:-}" ]; then
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
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT ' | sed 's/^/ ... block: /')"
}
expect_fact() { # key verdict what
  expect_contains "$out" "FACT $1: $2" "$3 (FACT $1)"
}

# The installation the whole suite runs against, and the token the fake gcloud
# prints: a marker value, never a real one and never shaped like one.
INSTALLATION=sandbox300
GCP_PROJECT=prj-sandbox300
DB_HOST=sandbox300-default-rtdb.example
TOKEN_MARKER=fake-access-token-not-a-secret
# What a document the check reads carries that is not this check's to print.
RULES_SENTINEL='SENTINEL-rule-text-from-the-deployed-document'

# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(installation database update marker subtree person-write token-refusal untouched xtrace restore)

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A fake gcloud for check300.sh's test: it answers `auth print-access-token`
# from $FAKE300_DIR and reaches nothing. Every call is recorded, so a test can
# tell a run that read the token from one that stopped before it.
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"$FAKE300_DIR/calls"
case "${1:-}" in
auth)
  case "${2:-}" in
  print-access-token)
    if [ -f "$FAKE300_DIR/token.status" ]; then
      printf 'fake gcloud: not logged in\n' >&2
      exit "$(cat "$FAKE300_DIR/token.status")"
    fi
    cat "$FAKE300_DIR/token"
    ;;
  *)
    printf 'fake gcloud: unknown auth verb %s\n' "${2:-}" >&2
    exit 64
    ;;
  esac
  ;;
*)
  printf 'fake gcloud: unexpected %s\n' "${1:-}" >&2
  exit 64
  ;;
esac
GCLOUD_EOF

  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
# A fake fugaro for check300.sh's test: doctor's answer and update's output come
# from $FAKE300_DIR. `update` exits with the status in $FAKE300_DIR/update.status
# when that file is there, and records that it ran.
set -euo pipefail
printf 'fugaro %s\n' "$*" >>"$FAKE300_DIR/calls"
case "${1:-}" in
doctor)
  cat "$FAKE300_DIR/doctor.json"
  ;;
update)
  printf 'update\n' >>"$FAKE300_DIR/ran-update"
  cat "$FAKE300_DIR/update.out"
  if [ -f "$FAKE300_DIR/update.status" ]; then
    exit "$(cat "$FAKE300_DIR/update.status")"
  fi
  ;;
*)
  printf 'fake fugaro: unexpected %s\n' "${1:-}" >&2
  exit 64
  ;;
esac
FUGARO_EOF

  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A fake curl for check300.sh's test: a small RTDB over $FAKE300_DIR/db.json,
# reached through fakecurl.py beside this file. No network, no socket, no host.
exec python3 "$(dirname "$0")/fakecurl.py" "$@"
CURL_EOF

  cat >"$1/bin/fakecurl.py" <<'PY_EOF'
# The fake RTDB behind check300.sh's test. It answers the four calls the check
# makes -- a GET of a node, a GET of the deployed rules, a PUT of a node and a
# DELETE of one -- out of a JSON store, and it is the RULES' own two answers: a
# call carrying `Authorization: Bearer ...` (or `?access_token=`) is IAM and
# bypasses them, and a call carrying no credential is refused 401 Permission
# denied. $FAKE300_DIR/anon_allowed makes it admit the credential-less call, so
# a case can drive the one row that must FAIL when the fence is not there, and
# $FAKE300_DIR/deny_delete makes the DELETE answer 500, for the Restore row.
import json, os, sys

store_dir = os.environ["FAKE300_DIR"]
store_path = os.path.join(store_dir, "db.json")
log = os.path.join(store_dir, "calls")


def load():
    try:
        with open(store_path, encoding="utf-8") as fh:
            return json.load(fh)
    except FileNotFoundError:
        return {}


def save(store):
    with open(store_path, "w", encoding="utf-8") as fh:
        json.dump(store, fh)


args = sys.argv[1:]
method, out, fmt, headers, body, url = "GET", None, None, [], None, None
i = 0
while i < len(args):
    a = args[i]
    if a in ("-sS", "-s", "-S", "--silent", "--show-error"):
        i += 1
    elif a == "-X":
        method, i = args[i + 1].upper(), i + 2
    elif a == "-o":
        out, i = args[i + 1], i + 2
    elif a == "-w":
        fmt, i = args[i + 1], i + 2
    elif a == "-H":
        headers.append(args[i + 1]), i
        i += 2
    elif a in ("-d", "--data"):
        body, i = args[i + 1], i + 2
    else:
        url, i = a, i + 1

with open(log, "a", encoding="utf-8") as fh:
    fh.write("curl %s %s\n" % (method, url))

if url is None:
    sys.stderr.write("fake curl: no URL\n")
    sys.exit(64)
rest = url.split("://", 1)[-1]
host, _, tail = rest.partition("/")
path, _, query = tail.partition("?")
if not path.endswith(".json"):
    sys.stderr.write("fake curl: not a node URL\n")
    sys.exit(64)
node = path[: -len(".json")]
iam = any(h.lower().startswith("authorization: bearer ") for h in headers) or "access_token=" in query


def answer(code, payload):
    if out:
        with open(out, "w", encoding="utf-8") as fh:
            fh.write(payload)
    else:
        sys.stdout.write(payload)
    if fmt and "%{http_code}" in fmt:
        sys.stdout.write(str(code))
    sys.exit(0)


if host != os.environ.get("FAKE300_HOST", host):
    answer(404, '{"error":"not this database"}')

if not iam and not os.path.exists(os.path.join(store_dir, "anon_allowed")):
    answer(401, '{"error":"Permission denied"}')

store = load()
if method == "GET":
    answer(200, json.dumps(store.get(node, None)))
if method == "PUT":
    if body is None:
        answer(400, '{"error":"no body"}')
    try:
        value = json.loads(body)
    except ValueError:
        answer(400, '{"error":"not JSON"}')
    store[node] = value
    save(store)
    answer(200, json.dumps(value))
if method == "DELETE":
    if os.path.exists(os.path.join(store_dir, "deny_delete")):
        answer(500, '{"error":"the fake refused this delete"}')
    # RTDB deletes a subtree, not one key: the check writes
    # orchestrator/check300/state and deletes orchestrator/check300.
    for key in [k for k in store if k == node or k.startswith(node + "/")]:
        del store[key]
    save(store)
    answer(200, "null")
answer(405, '{"error":"method not allowed"}')
PY_EOF

  chmod +x "$1/bin/gcloud" "$1/bin/fugaro" "$1/bin/curl"
}

# ---------------------------------------------------------------- the fixtures
write_doctor() { # dir [NAME] [PROJECT]
  printf '{"ok": true, "checks": [], "project": {"name": "%s", "gcp_project": "%s", "region": "us-east5"}}\n' \
    "${2:-$INSTALLATION}" "${3:-$GCP_PROJECT}" >"$1/state/doctor.json"
}

write_config() { # dir [NAME]
  local name=${2:-$INSTALLATION}
  mkdir -p "$1/cfg/fugaro/projects"
  # Block style, because that is how the check reads the two fields out of the
  # installation's own file: a line beginning `rtdb_url:`.
  cat >"$1/cfg/fugaro/projects/$name.yaml" <<YAML
budget:
  mode: enforce
  per_run_usd: 5
  rtdb_url: "https://$DB_HOST"
  firebase_project: $GCP_PROJECT
YAML
}

# write_store dir MARKER RULES — the fake database: the generation marker
# (MARKER is `absent`, `null` or a number) and the deployed document, which is
# the real golden unless RULES names a file of its own.
write_store() {
  local dir=$1 marker=$2 rules=${3:-$golden}
  FAKE300_MARKER="$marker" python3 - "$dir/state/db.json" "$rules" <<'PY'
import json, os, sys

marker = os.environ["FAKE300_MARKER"]
with open(sys.argv[2], encoding="utf-8") as fh:
    document = json.load(fh)
store = {".settings/rules": document}
if marker == "absent":
    pass
elif marker == "null":
    store["config/limits/orchestratorRules"] = None
else:
    store["config/limits/orchestratorRules"] = int(marker)
with open(sys.argv[1], "w", encoding="utf-8") as fh:
    json.dump(store, fh)
PY
}

# write_rules FILE NODE... — a deployed document of this test's own, for the
# cases about a document that is not the generated one. NODE is a python
# expression for the /orchestrator node, or `absent`.
write_rules() {
  local file=$1 node=$2
  FAKE300_NODE="$node" RULES_SENTINEL="$RULES_SENTINEL" python3 - "$file" <<'PY'
import json, os, sys

rules = {".read": False, ".write": False, "config": {"limits": {"policyRules": {"x": os.environ["RULES_SENTINEL"]}}}}
node = os.environ["FAKE300_NODE"]
if node != "absent":
    rules["orchestrator"] = eval(node, {"USER": "auth != null && auth.token.mu != null && root.child('config/admin/devices').child(auth.uid).exists()", "false": False})
with open(sys.argv[1], "w", encoding="utf-8") as fh:
    json.dump({"rules": rules}, fh)
PY
}

write_update() { # dir OUT [STATUS]
  printf '%s\n' "$2" >"$1/state/update.out"
  if [ $# -ge 3 ]; then
    printf '%s\n' "$3" >"$1/state/update.status"
  else
    rm -f "$1/state/update.status"
  fi
}

# setup_case NAME — a fresh case: the fakes, a sandbox installation, a healthy
# update and a database on this generation. A case changes what it is about.
setup_case() {
  dir=$work/$1
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp"
  write_fakes "$dir"
  : >"$dir/state/calls"
  printf '%s\n' "$TOKEN_MARKER" >"$dir/state/token"
  write_doctor "$dir"
  write_config "$dir"
  write_store "$dir" 1
  write_update "$dir" "rules: deploys the security rules (59411 bytes, generated)
config/limits/orchestratorRules: records the orchestrator rules generation (config/limits/orchestratorRules = 1)"
}

# run300 [ARGS...] — runs the check on the case at hand. RUN300_ARGS adds
# arguments, RUN300_PATH replaces PATH, RUN300_ENV names environment variables
# (NAME=value) and RUN300_BASH adds flags to the bash that runs it. Sets
# $status, $out and $dir/err.
run300() {
  status=0
  out=$(env TMPDIR="$dir/tmp" FAKE300_DIR="$dir/state" FAKE300_HOST="$DB_HOST" \
    XDG_CONFIG_HOME="$dir/cfg" \
    PATH="${RUN300_PATH:-$dir/bin:$PATH}" \
    ${RUN300_ENV[@]+"${RUN300_ENV[@]}"} \
    "$BASH_BIN" ${RUN300_BASH[@]+"${RUN300_BASH[@]}"} "$script" ${RUN300_ARGS[@]+"${RUN300_ARGS[@]}"} "$@" 2>"$dir/err") || status=$?
  RUN300_ARGS=()
  RUN300_ENV=()
  RUN300_PATH=""
  RUN300_BASH=()
  # Every case's own assertion, wherever it is about something else: the token
  # the fake gcloud printed is in no line this run wrote, and no document the
  # check read is either.
  expect_absent "$out$(cat "$dir/err" 2>/dev/null || true)" "$TOKEN_MARKER" \
    "case $dir: the access token reached the block or the log"
  expect_absent "$out" "$RULES_SENTINEL" "case $dir: a document the check read reached the block"
}

expect_block_keys() { # out what
  local key
  expect_contains "$1" "CHECK 300 RESULT" "$2: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$1" "FACT $key:" "$2: the $key row"
  done
  expect_contains "$1" "CHECK 300 RESULT END (exit " "$2: the block's end"
}

node_in_store() { # path
  python3 - "$dir/state/db.json" "$1" <<'PY'
import json, sys
store = json.load(open(sys.argv[1], encoding="utf-8"))
print("present" if sys.argv[2] in store else "absent")
PY
}

# --------------------------------------------------------------- the healthy run
case_healthy() {
  setup_case healthy
  run300 --installation "$INSTALLATION"
  expect_status "$status" 0 "healthy: a converged installation with the fence in place"
  expect_block_keys "$out" "healthy"
  expect_fact installation PASS "healthy"
  expect_fact database PASS "healthy"
  expect_fact update PASS "healthy"
  expect_fact marker PASS "healthy"
  expect_fact subtree PASS "healthy"
  expect_fact person-write PASS "healthy"
  expect_fact token-refusal PASS "healthy"
  expect_fact untouched PASS "healthy"
  expect_fact xtrace PASS "healthy"
  expect_fact restore PASS "healthy"
  expect_contains "$out" "the node this check wrote (/orchestrator/check300) is deleted and reads back as null" "healthy: the Restore row says what it deleted"
  [ "$(node_in_store orchestrator/check300/state)" = absent ] || fail "healthy: the state node this check wrote is still in the database"
  expect_contains "$(cat "$dir/state/calls")" "fugaro update --yes" "healthy: the one command was run"
  expect_contains "$(cat "$dir/state/calls")" "gcloud auth print-access-token" "healthy: the person's own credential was read"
}

# A database already on this generation: the update plans nothing, which is not
# a failure of the update row — the marker row is what says the generation is
# there.
case_settled() {
  setup_case settled
  write_update "$dir" "The database already has the rules, mark, project name and mode."
  run300 --installation "$INSTALLATION"
  expect_status "$status" 0 "settled: an installation whose update planned nothing"
  expect_fact update PASS "settled"
  expect_contains "$out" "a settled run plans nothing" "settled: the update row says why no marker line appeared"
  expect_fact marker PASS "settled"
}

# --skip-update: the read-only re-run. It must not run the command, and the
# update row must say so rather than pass as if it had.
case_skip_update() {
  setup_case skip-update
  run300 --installation "$INSTALLATION" --skip-update
  expect_status "$status" 0 "skip-update"
  expect_fact update INFO "skip-update"
  expect_absent "$(cat "$dir/state/calls")" "fugaro update" "skip-update: the one command was run anyway"
}

# --------------------------------------------------- the marker and the document
case_stale_marker() {
  setup_case stale-marker
  write_store "$dir" 2
  run300 --installation "$INSTALLATION"
  expect_status "$status" 1 "stale-marker: a database one generation behind"
  expect_fact marker FAIL "stale-marker"
  expect_contains "$out" "want 1" "stale-marker: the row names the generation it wanted"
  expect_fact subtree PASS "stale-marker: the document is still read"
}

case_absent_marker() {
  setup_case absent-marker
  write_store "$dir" absent
  run300 --installation "$INSTALLATION"
  expect_status "$status" 1 "absent-marker"
  expect_fact marker FAIL "absent-marker"
  expect_contains "$out" "config/limits/orchestratorRules = null" "absent-marker: the row names the raw body"
}

case_subtree_absent() {
  setup_case subtree-absent
  write_rules "$dir/state/rules-absent.json" absent
  write_store "$dir" 1 "$dir/state/rules-absent.json"
  run300 --installation "$INSTALLATION"
  expect_status "$status" 1 "subtree-absent: a document with no orchestrator node"
  expect_fact subtree FAIL "subtree-absent"
  expect_contains "$out" "subtree=absent" "subtree-absent: the row says what the document reads"
}

case_subtree_widened() {
  setup_case subtree-widened
  write_rules "$dir/state/rules-widened.json" '{".read": USER, ".write": "true"}'
  write_store "$dir" 1 "$dir/state/rules-widened.json"
  run300 --installation "$INSTALLATION"
  expect_status "$status" 1 "subtree-widened: a write grant on the subtree"
  expect_fact subtree FAIL "subtree-widened"
  # The reader's vocabulary, not the document's spelling: a grant that is not
  # the boolean false is `other`, whatever it says.
  expect_contains "$out" "write=other" "subtree-widened: the row says what the document reads"
}

case_subtree_child() {
  setup_case subtree-child
  write_rules "$dir/state/rules-child.json" '{".read": USER, ".write": false, "state": {".write": "true"}}'
  write_store "$dir" 1 "$dir/state/rules-child.json"
  run300 --installation "$INSTALLATION"
  expect_status "$status" 1 "subtree-child: a grant hidden one level down"
  expect_fact subtree FAIL "subtree-child"
  expect_contains "$out" "children=1" "subtree-child: the row counts the child rule"
}

# ----------------------------------------------------------------- the fence
case_world_writable() {
  setup_case world-writable
  : >"$dir/state/anon_allowed"
  run300 --installation "$INSTALLATION"
  expect_status "$status" 1 "world-writable: the fake admits a credential-less write"
  expect_fact token-refusal FAIL "world-writable"
  expect_fact person-write PASS "world-writable: the positive control still holds"
}

# ---------------------------------------------------------------- the guards
case_update_failed() {
  setup_case update-failed
  write_update "$dir" "terraform: apply failed" 1
  run300 --installation "$INSTALLATION"
  expect_status "$status" 1 "update-failed: the one command exited 1"
  expect_fact update FAIL "update-failed"
  expect_fact marker PASS "update-failed: the database is still read, and it was already on this generation"
}

case_belong() {
  setup_case belong
  run300 --installation belong
  expect_status "$status" 2 "belong: the owner's own installation"
  expect_fact installation FAIL "belong"
  expect_contains "$out" "never runs against 'belong'" "belong: the block says so"
  expect_absent "$(cat "$dir/state/calls" 2>/dev/null || true)" "gcloud" "belong: the token was read anyway"
  expect_absent "$(cat "$dir/state/calls" 2>/dev/null || true)" "fugaro" "belong: doctor was asked anyway"
}

case_name_mismatch() {
  setup_case name-mismatch
  write_doctor "$dir" other-installation
  run300 --installation "$INSTALLATION"
  expect_status "$status" 2 "name-mismatch: the typed name and the config disagree"
  expect_fact installation FAIL "name-mismatch"
  expect_contains "$out" "answers other-installation" "name-mismatch: the row names what the config answered"
  expect_absent "$(cat "$dir/state/calls" 2>/dev/null || true)" "gcloud" "name-mismatch: the token was read anyway"
}

case_doctor_refused() {
  setup_case doctor-refused
  printf 'not json\n' >"$dir/state/doctor.json"
  run300 --installation "$INSTALLATION"
  expect_status "$status" 2 "doctor-refused: an answer that is not the document"
  expect_contains "$out" "not the document this check reads" "doctor-refused: the block says why"
}

case_token_missing() {
  setup_case token-missing
  printf '1\n' >"$dir/state/token.status"
  run300 --installation "$INSTALLATION"
  expect_status "$status" 2 "token-missing: gcloud is not logged in"
  expect_contains "$out" "gcloud auth print-access-token did not answer" "token-missing: the block names the command"
}

case_no_config() {
  setup_case no-config
  rm -f "$dir/cfg/fugaro/projects/$INSTALLATION.yaml"
  run300 --installation "$INSTALLATION"
  expect_status "$status" 2 "no-config: the installation's own file is not there"
  expect_contains "$out" "no project config at" "no-config: the block names the path"
}

case_no_installation_arg() {
  setup_case no-arg
  run300
  expect_status "$status" 2 "no-arg: --installation is required"
  expect_contains "$(cat "$dir/err")" "--installation is required" "no-arg: the usage line"
}

case_missing_tools() {
  setup_case missing-tools
  mkdir -p "$dir/empty"
  RUN300_PATH="$dir/empty" run300 --installation "$INSTALLATION"
  expect_status "$status" 2 "missing-tools: no tools on PATH"
  expect_contains "$(cat "$dir/err")" "Check 300 needs" "missing-tools: the one line naming the tool"
}

case_xtrace() {
  setup_case xtrace
  # On its own line: `RUN300_BASH=(-x) run300` would pass the string "(-x)" as
  # one argument, and bash would then look for a script named (-x).
  RUN300_BASH=(-x)
  run300 --installation "$INSTALLATION"
  expect_status "$status" 2 "xtrace: a shell started with -x"
  expect_fact xtrace FAIL "xtrace"
  # run300's own assertion already holds the token out of out and err; this is
  # the case that would break it if the check read the token before the guard.
  expect_absent "$(cat "$dir/err")" "$TOKEN_MARKER" "xtrace: the token in a trace"
}

case_restore_failed() {
  setup_case restore-failed
  : >"$dir/state/deny_delete"
  run300 --installation "$INSTALLATION"
  expect_status "$status" 1 "restore-failed: the delete answered 500"
  expect_fact restore FAIL "restore-failed"
  expect_contains "$out" "STILL THERE" "restore-failed: the row says the node is still there"
  expect_contains "$out" "delete it yourself" "restore-failed: the row says what to do"
}

# A run that read the token and the database and then stopped before its write
# — here the deployed document is not the object the reader parses, which is
# step_subtree's own stop. The Restore still deletes this check's own id, and
# its row has to say that this run wrote no node: a row claiming the write would
# report a write that never happened, on the path a run that stops early takes.
case_stopped_before_write() {
  setup_case stopped-before-write
  printf '["not a rules document"]\n' >"$dir/state/rules-broken.json"
  write_store "$dir" 1 "$dir/state/rules-broken.json"
  run300 --installation "$INSTALLATION"
  expect_status "$status" 2 "stopped-before-write: the deployed document cannot be read"
  expect_contains "$out" "the deployed rules are not the JSON document this check reads" "stopped-before-write: the block says why it stopped"
  expect_fact restore PASS "stopped-before-write: the Restore row"
  expect_contains "$out" "this run stopped before it wrote anything" "stopped-before-write: the row says no node was written"
  expect_absent "$out" "the node this check wrote" "stopped-before-write: the row claims a write this run never made"
  [ "$(node_in_store orchestrator/check300/state)" = absent ] || fail "stopped-before-write: a node is in the database"
}

# Sharding, so the package's Go test can run the cases of this file side by
# side: CHECK_TEST_SHARD="i/n" runs every n-th case starting at i, and
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
t case_settled
t case_skip_update
t case_stale_marker
t case_absent_marker
t case_subtree_absent
t case_subtree_widened
t case_subtree_child
t case_world_writable
t case_update_failed
t case_belong
t case_name_mismatch
t case_doctor_refused
t case_token_missing
t case_no_config
t case_no_installation_arg
t case_missing_tools
t case_xtrace
t case_restore_failed
t case_stopped_before_write

if [ "$failures" -ne 0 ]; then
  echo "check300_test: $failures failure(s)" >&2
  exit 1
fi
echo "check300_test: all cases passed"
