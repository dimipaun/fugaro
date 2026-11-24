#!/usr/bin/env bash
# Tests for scripts/live-checks/check-890-data-layout.sh, with fake `gcloud`,
# `curl` and `fugaro` on PATH: NOTHING here reaches a cloud, a database or a
# bucket. The fakes answer out of the case's own files (an RTDB tree, a list of
# bucket objects, the Firestore collection and document ids) and record every
# call they are given, so a case can assert what the check read and how.
#
# Every case runs the check with stdin closed, under a watchdog, with HOME,
# XDG_CONFIG_HOME and TMPDIR inside the case's own directory.
#
# Usage: bash scripts/live-checks/check-890-data-layout_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-890-data-layout.sh
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK890_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK890_TEST_HEAD:-1500}")" ;;
  esac
}
expect_absent() {
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1 (stderr: $(printf '%s' "$err" | tail -c 600))"
}

project=chk890
gcp=chk890-gcp
fp=chk890-fp
bucket=fugaro-runs-${gcp}
rtdb_url=https://${fp}-default-rtdb.firebaseio.com
day=20780
slug_a=acme-app-0123456789abcdef
slug_b=acme-web-fedcba9876543210
run1=20261122-090000-b890
run2=20261122-100000-c890
# A build identity is an account, escaped as an RTDB key (budget.Key): it is
# keyed by account, not by repository, and must not read as a slug.
build_identity=fugaro-build@${gcp}%2Eiam%2Egserviceaccount%2Ecom
# Placeholders the fakes hand out. Neither is shaped like a credential: the
# token is what `gcloud auth print-access-token` answers here, and the leaf is
# a primitive value the RTDB's shallow read returns verbatim.
fake_token=chk890-fake-access-token-placeholder
fake_leaf=chk890-fake-leaf-value-placeholder
timeout_seconds=${CHECK890_TEST_TIMEOUT:-90}

write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
# A stand-in for `gcloud`, for check-890's test only: the access token, and
# `storage ls` of the case's own object list.
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"$FAKE890_LOG/gcloud.calls"
if IFS= read -r -t 1 _line 2>/dev/null; then printf 'gcloud read stdin\n' >>"$FAKE890_LOG/stdin.reads"; fi
case "${1:-} ${2:-}" in
"auth print-access-token")
  printf '%s\n' "$FAKE890_TOKEN"
  ;;
"storage ls")
  url=${3:?}
  python3 - "$FAKE890_ANSWERS/objects.txt" "$url" <<'PY'
import sys
objects = [l.strip() for l in open(sys.argv[1]) if l.strip()]
url = sys.argv[2]
assert url.startswith("gs://")
bucket, _, prefix = url[len("gs://"):].partition("/")
seen = []
for o in objects:
    if not o.startswith(prefix):
        continue
    rest = o[len(prefix):]
    if not rest:
        continue
    first, sep, _ = rest.partition("/")
    entry = "gs://%s/%s%s%s" % (bucket, prefix, first, sep)
    if entry not in seen:
        seen.append(entry)
if not seen:
    sys.stderr.write("ERROR: (gcloud.storage.ls) One or more URLs matched no objects.\n")
    sys.exit(1)
print("\n".join(sorted(seen)))
PY
  ;;
*)
  echo "gcloud: unexpected command $*" >&2
  exit 1
  ;;
esac
GCLOUD_EOF
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env bash
# A stand-in for `curl`, for check-890's test only. It answers the RTDB's
# shallow reads out of the case's tree, Firestore's listCollectionIds and the
# spendDaily list out of the case's ids, and records its argv and whether the
# --config file it was handed carried the token.
set -euo pipefail
printf 'curl %s\n' "$*" >>"$FAKE890_LOG/curl.calls"
if IFS= read -r -t 1 _line 2>/dev/null; then printf 'curl read stdin\n' >>"$FAKE890_LOG/stdin.reads"; fi
out="" confs=() url=""
while [ $# -gt 0 ]; do
  case "$1" in
  -o) out=$2; shift 2 ;;
  --config) confs+=("$2"); shift 2 ;;
  -w | -H | -X | --data | --max-time) shift 2 ;;
  https://*) url=$1; shift ;;
  *) shift ;;
  esac
done
if [ ${#confs[@]} -gt 0 ] && cat "${confs[@]}" | grep -qF -- "$FAKE890_TOKEN"; then
  printf 'config carried the token\n' >>"$FAKE890_LOG/curl.auth"
fi
case "$url" in
https://firestore.googleapis.com/*)
  if [ ${#confs[@]} -gt 0 ] && cat "${confs[@]}" | grep -qF -- "X-Goog-User-Project: chk890-fp"; then
    printf '%s\n' "$url" >>"$FAKE890_LOG/curl.userproject"
  fi
  ;;
esac
code=200
case "${FAKE890_MODE:-default}:$url" in
rtdb-denied:*firebaseio.com*) code=401; printf '{"error":"Permission denied"}' >"$out" ;;
*firebaseio.com*)
  python3 - "$FAKE890_ANSWERS/tree.json" "$url" >"$out" <<'PY'
import json, sys, urllib.parse
tree = json.load(open(sys.argv[1]))
u = urllib.parse.urlsplit(sys.argv[2])
assert "shallow=true" in u.query, "a read without shallow=true"
path = urllib.parse.unquote(u.path)
assert path.endswith(".json")
node = tree
for seg in [s for s in path[:-len(".json")].split("/") if s]:
    node = node.get(seg) if isinstance(node, dict) else None
if isinstance(node, dict):
    node = {k: True if isinstance(v, (dict, list)) else v for k, v in node.items()}
print(json.dumps(node))
PY
  ;;
*:listCollectionIds) cat "$FAKE890_ANSWERS/collections.json" >"$out" ;;
*/documents/spendDaily\?*)
  python3 - "$FAKE890_ANSWERS/spenddaily.txt" "$url" >"$out" <<'PY'
import json, sys, urllib.parse
q = urllib.parse.parse_qs(urllib.parse.urlsplit(sys.argv[2]).query)
size = int(q.get("pageSize", ["300"])[0])
ids = [l.strip() for l in open(sys.argv[1]) if l.strip()][:size]
base = "projects/chk890-fp/databases/(default)/documents/spendDaily/"
print(json.dumps({"documents": [{"name": base + i} for i in ids]}))
PY
  ;;
*) code=404; printf '{}' >"$out" ;;
esac
printf '%s' "$code"
CURL_EOF
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
printf 'fugaro %s\n' "$*" >>"$FAKE890_LOG/fugaro.calls"
echo "fugaro: this check has no fugaro call to make" >&2
exit 1
FUGARO_EOF
  chmod +x "$1/bin/gcloud" "$1/bin/curl" "$1/bin/fugaro"
}

# write_answers DIR: a project whose every repo-keyed path is one report.md
# names. A case bends the file it is about.
write_answers() { # dir
  local a=$1/answers
  mkdir -p "$a"
  cat >"$a/tree.json" <<JSON
{
  "config": {
    "caps": {"global": {"dailyMicros": 5, "note": "$fake_leaf"}, "repos": {"$slug_a": {"perRunMicros": 1}}},
    "kill": {"repos": {"$slug_b": true}},
    "policy": {
      "shared": {"version": 3},
      "repos": {"$slug_a": {"accounts": {"main": true}}},
      "voice": {"repos": {"$slug_a": {"voice": "plain"}}},
      "route": {"$slug_a": {"source": "pool"}}
    },
    "admin": {"access": {"owner": "role"}}
  },
  "creds": {
    "repoauth": {"$slug_a": {"mode": "pool"}},
    "repoaccess": {"$slug_a": {"kind": "app"}},
    "workflowsecrets": {"$slug_a": {"names": true}},
    "leases": {"$slug_a": {"$run1": {"held": true}}},
    "current": {"$slug_a": {"$run1": {"on": true}}},
    "review-signs": {"$slug_a": {"$run1": {"signed": true}}},
    "issuance": {"$slug_a": {"$run1": {"issued": true}}},
    "buildpairs": {"$build_identity": {"build-1": {"run": "$run1"}}},
    "tokens": {"tok-1": {"a": 1}, "tok-2": {"a": 1}, "tok-3": {"a": 1}, "tok-4": {"a": 1}, "tok-5": {"a": 1}, "tok-6": {"a": 1}},
    "marker": "$fake_leaf"
  },
  "runs": {"$slug_a": {"$run1": {"spent": 1}}, "$slug_b": {"$run2": {"spent": 2}}},
  "spend": {"$day": {
    "global": {"spent": 3},
    "meta": {"date": "2026-11-23"},
    "repos": {"$slug_a": {"spent": 1}},
    "runs": {"$slug_a": {"$run1": {"spent": 1}}}
  }},
  "agents": {"$slug_a": {"$run1": {"state": "running"}}},
  "outcomes": {"$day": {"$slug_a": {"$run1": {"outcome": "ready"}}}},
  "buildleases": {"$slug_a": {"lease-1": {"holder": "build"}}},
  "coordinator": {"repos": {"$slug_a": {"facts": {"one": true}}}},
  "version": "$fake_leaf"
}
JSON
  cat >"$a/objects.txt" <<EOF
runs/$slug_a/$run1/task.json
runs/$slug_a/pr-index/12/$run1.json
runs/$slug_b/$run2/task.json
builds/$slug_a/implement/image.json
builds/$slug_a/implement/check.json
builds/$slug_a/project-layer.yaml
builds/$slug_a/catalog/node/record.json
cache/$slug_a/deps.tar
locks/$slug_a/lock.json
tokens/$slug_a/token.json
reports/$slug_a/weekly.md
EOF
  cat >"$a/collections.json" <<'JSON'
{"collectionIds": ["meta", "runIndex", "spendDaily"]}
JSON
  cat >"$a/spenddaily.txt" <<EOF
2026-11-21_$slug_a
2026-11-22_$slug_a
2026-11-22_$slug_b
EOF
  cat >"$1/config.yaml" <<YAML
name: $project
gcp_project: $gcp
region: us-east5
runs_bucket: $bucket
budget:
    mode: enforce
    per_run_usd: 5
    allowed_models:
        - claude-sonnet-5-5
    caps:
        rtdb_url: https://decoy-default-rtdb.firebaseio.com
    rtdb_url: $rtdb_url
    firebase_project: $fp
YAML
}

setup_case() { # dir
  mkdir -p "$1/home" "$1/tmp" "$1/xdg" "$1/logs"
  write_fakes "$1"
  write_answers "$1"
}

# run_check DIR [K=V ...] [-- ARGS...]: the check, with stdin closed (or from
# $STDIN_FILE when a case sets it), under a watchdog that kills it after
# $timeout_seconds. A check that waits for input is a timeout, not a hang.
run_check() {
  local dir=$1
  shift
  local envs=() cli=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    envs+=("$1")
    shift
  done
  if [ "${1:-}" = "--" ]; then
    shift
    cli=("$@")
  fi
  : >"$dir/logs/gcloud.calls"
  : >"$dir/logs/curl.calls"
  : >"$dir/logs/fugaro.calls"
  : >"$dir/logs/curl.auth"
  : >"$dir/logs/curl.userproject"
  : >"$dir/logs/stdin.reads"
  rm -f "$dir/timed-out"
  (
    if [ -n "${STDIN_FILE:-}" ]; then exec 0<"$STDIN_FILE"; else exec 0<&-; fi
    exec env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT \
      PATH="$dir/bin:$PATH" HOME="$dir/home" XDG_CONFIG_HOME="$dir/xdg" TMPDIR="$dir/tmp" \
      FAKE890_LOG="$dir/logs" FAKE890_ANSWERS="$dir/answers" FAKE890_TOKEN="$fake_token" \
      CHECK890_DAY="$day" \
      ${envs[@]+"${envs[@]}"} \
      bash "$script" ${cli[@]+"${cli[@]}"} >"$dir/out" 2>"$dir/err"
  ) &
  local pid=$! ticks=$((timeout_seconds * 10))
  (
    while [ "$ticks" -gt 0 ]; do
      kill -0 "$pid" 2>/dev/null || exit 0
      sleep 0.1
      ticks=$((ticks - 1))
    done
    : >"$dir/timed-out"
    kill -9 "$pid" 2>/dev/null || true
  ) </dev/null >/dev/null 2>&1 &
  local watchdog=$!
  status=0
  wait "$pid" || status=$?
  wait "$watchdog" 2>/dev/null || true
  out=$(cat "$dir/out" 2>/dev/null || true)
  err=$(cat "$dir/err" 2>/dev/null || true)
  if [ -e "$dir/timed-out" ]; then
    fail "the check did not finish within ${timeout_seconds}s with stdin closed: it waited for something"
  fi
}

test_a_known_layout_passes() {
  local dir=$work/known
  setup_case "$dir"
  run_check "$dir" -- "$project" --config "$dir/config.yaml"
  expect_status "$status" 0 "a known layout"
  expect_contains "$out" "CHECK 890 RESULT: PASS (exit 0)" "a known layout: the verdict"
  local row
  for row in "RC40 rtdb config/caps/repos/<slug>" "RC41 rtdb config/kill/repos/<slug>" \
    "RC42 rtdb runs/<slug>" "RC42 rtdb agents/<slug>" "RC42 rtdb outcomes/<day>/<slug>" \
    "RC42 rtdb spend/<day>/runs/<slug>" "RC43 rtdb spend/<day>/repos/<slug>" \
    "RC44 rtdb buildleases/<slug>" "RC45 rtdb config/policy/repos/<slug>" \
    "RC46 rtdb config/policy/voice/repos/<slug>" "RC47 rtdb config/policy/route/<slug>" \
    "RC48 rtdb creds/repoauth/<slug>" "RC48 rtdb creds/repoaccess/<slug>" \
    "RC49 rtdb coordinator/repos/<slug>" "RC99 rtdb creds/workflowsecrets/<slug>" \
    "RC100 rtdb creds/leases/<slug>" "RC101 rtdb creds/review-signs/<slug>" "RC102 rtdb creds/issuance/<slug>" "RC106 rtdb creds/current/<slug>" \
    "RC50 firestore spendDaily/<date>_<slug>" \
    "RC52 gcs runs/<slug>/<run>" "RC52 gcs runs/<slug>/pr-index" \
    "RC53 gcs cache/<slug>" "RC53 gcs locks/<slug>" "RC53 gcs tokens/<slug>" "RC53 gcs reports/<slug>" \
    "RC54 gcs builds/<slug>/implement" "RC55 gcs builds/<slug>/catalog" "RC56 gcs builds/<slug>/project-layer.yaml"; do
    expect_contains "$out" "[slug] $row" "a known layout: the path is marked and matched"
  done
  expect_absent "$out" "UNNAMED" "a known layout: nothing is outside the report"
  expect_absent "$out" "creds/buildpairs/<" "a known layout: a build identity is not read as a repo key"
  expect_contains "$out" "rtdb  creds/tokens  6 keys: tok-1, tok-2, tok-3, ..." "a known layout: creds/ children print at most --sample names"
  expect_absent "$out" "tok-4" "a known layout: no fourth creds/tokens name"
  local fs_calls fs_headed
  fs_calls=$(grep -c 'https://firestore.googleapis.com/' "$dir/logs/curl.calls" || true)
  fs_headed=$(grep -c . "$dir/logs/curl.userproject" || true)
  [ "$fs_calls" -eq 2 ] && [ "$fs_headed" -eq 2 ] ||
    fail "a known layout: every Firestore call carries X-Goog-User-Project from a config file ($fs_headed of $fs_calls did)"
  expect_absent "$(cat "$dir/logs/curl.calls")" "X-Goog-User-Project" "a known layout: the header is not on argv"
  expect_contains "$out" "gcs   /" "a known layout: the bucket's root is listed"
  expect_absent "$(cat "$dir/logs/fugaro.calls")" "fugaro" "a known layout: no fugaro call"
}

test_an_unnamed_repo_keyed_path_fails() {
  local dir=$work/unnamed
  setup_case "$dir"
  python3 - "$dir/answers/tree.json" "$slug_a" <<'PY'
import json, sys
p, slug = sys.argv[1], sys.argv[2]
t = json.load(open(p))
t["creds"]["mirrors"] = {slug: {"names": True}}
t["mirror"] = {slug: {"x": 1}}
json.dump(t, open(p, "w"))
PY
  printf 'logs/%s/today.txt\n' "$slug_a" >>"$dir/answers/objects.txt"
  run_check "$dir" -- "$project" --config "$dir/config.yaml"
  expect_status "$status" 1 "an unnamed repo-keyed path"
  expect_contains "$out" "CHECK 890 RESULT: FAIL (exit 1)" "an unnamed path: the verdict"
  expect_contains "$out" "[slug] UNNAMED rtdb creds/mirrors/<slug>" "an unnamed path under creds/"
  expect_contains "$out" "[slug] UNNAMED rtdb mirror/<slug>" "an unnamed top-level node"
  expect_contains "$out" "[slug] UNNAMED gcs logs/<slug>" "an unnamed bucket prefix"
  local layout
  layout=$(printf '%s\n' "$out" | grep -m1 '^  layout ' || true)
  expect_contains "$layout" "FAIL" "an unnamed path: the layout row"
  expect_contains "$layout" "creds/mirrors/<slug>" "an unnamed path: the layout row names it"
}

test_only_reads_are_called() {
  local dir=$work/reads
  setup_case "$dir"
  run_check "$dir" -- "$project" --config "$dir/config.yaml"
  expect_status "$status" 0 "only reads"
  local line bad=""
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    case "$line" in
    *" --quiet"*) ;;
    *) bad="${bad}${bad:+; }no --quiet: $line" ;;
    esac
    case "$line" in
    "gcloud auth print-access-token"* | "gcloud storage ls "*) ;;
    *) bad="${bad}${bad:+; }$line" ;;
    esac
  done <"$dir/logs/gcloud.calls"
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    case "$line" in
    *" -X PUT"* | *" -X PATCH"* | *" -X DELETE"* | *" --request "* | *" -d "* | *" -T "* | *" -F "*)
      bad="${bad}${bad:+; }$line"
      ;;
    *" -X POST "*)
      case "$line" in
      *"/documents:listCollectionIds") ;;
      *) bad="${bad}${bad:+; }a POST that is not listCollectionIds: $line" ;;
      esac
      ;;
    *firebaseio.com*)
      case "$line" in
      *"?shallow=true") ;;
      *) bad="${bad}${bad:+; }an RTDB read without shallow=true: $line" ;;
      esac
      ;;
    *"/documents/spendDaily?"*)
      case "$line" in
      *"mask.fieldPaths="*) ;;
      *) bad="${bad}${bad:+; }a Firestore list with no mask: $line" ;;
      esac
      ;;
    *) bad="${bad}${bad:+; }$line" ;;
    esac
    case "$line" in
    *" --data "*)
      case "$line" in
      *"/documents:listCollectionIds") ;;
      *) bad="${bad}${bad:+; }a body sent to: $line" ;;
      esac
      ;;
    esac
  done <"$dir/logs/curl.calls"
  [ -z "$bad" ] || fail "only reads: a call outside the read set: $bad"
  [ ! -s "$dir/logs/fugaro.calls" ] || fail "only reads: fugaro was called: $(cat "$dir/logs/fugaro.calls")"
  grep -q 'storage ls' "$dir/logs/gcloud.calls" || fail "only reads: the bucket was never listed"
  grep -q 'listCollectionIds' "$dir/logs/curl.calls" || fail "only reads: Firestore was never listed"
}

test_values_are_never_printed() {
  local dir=$work/values
  setup_case "$dir"
  run_check "$dir" -- "$project" --config "$dir/config.yaml"
  expect_status "$status" 0 "values"
  expect_absent "$out" "$fake_leaf" "values: stdout"
  expect_absent "$err" "$fake_leaf" "values: stderr"
  expect_contains "$out" "creds" "values: the node holding the leaf is still listed by key"
  local leftover
  leftover=$(grep -rl -- "$fake_leaf" "$dir/tmp" 2>/dev/null || true)
  [ -z "$leftover" ] || fail "values: a body holding a value is left on disk: $leftover"
}

test_the_token_is_never_on_argv() {
  local dir=$work/token
  setup_case "$dir"
  run_check "$dir" -- "$project" --config "$dir/config.yaml"
  expect_status "$status" 0 "token"
  expect_absent "$(cat "$dir/logs/curl.calls")" "$fake_token" "token: curl's argv"
  expect_absent "$(cat "$dir/logs/gcloud.calls")" "$fake_token" "token: gcloud's argv"
  expect_absent "$out" "$fake_token" "token: stdout"
  expect_absent "$err" "$fake_token" "token: stderr"
  local calls auths
  calls=$(grep -c . "$dir/logs/curl.calls" || true)
  auths=$(grep -c . "$dir/logs/curl.auth" || true)
  [ "$calls" -gt 0 ] && [ "$calls" = "$auths" ] ||
    fail "token: every curl call carries the token in its --config file ($auths of $calls did)"
  local leftover
  leftover=$(grep -rl -- "$fake_token" "$dir/tmp" 2>/dev/null || true)
  [ -z "$leftover" ] || fail "token: the token is left on disk: $leftover"
}

test_runs_are_sampled() {
  local dir=$work/sampled
  setup_case "$dir"
  local i s names=""
  for i in 1 2 3 4 5 6; do
    s=acme-svc$i-00000000000000a$i
    names="$names $s"
    printf 'runs/%s/%s/task.json\n' "$s" "$run1" >>"$dir/answers/objects.txt"
  done
  run_check "$dir" -- "$project" --config "$dir/config.yaml"
  expect_status "$status" 0 "sampled"
  local lists
  lists=$(grep -c "storage ls gs://$bucket/runs/[^ ]*/ " "$dir/logs/gcloud.calls" || true)
  [ "$lists" -le 3 ] && [ "$lists" -ge 1 ] || fail "sampled: runs/ was listed under $lists slugs, where --sample defaults to 3"
  local shown=0
  for s in $names; do
    case "$out" in *"$s"*) shown=$((shown + 1)) ;; esac
  done
  [ "$shown" -le 3 ] || fail "sampled: $shown of the six runs/ slugs were printed, where at most 3 are"
  expect_contains "$out" "8 keys" "sampled: the count of runs/'s slugs is still told"

  run_check "$dir" -- "$project" --config "$dir/config.yaml" --sample 1
  expect_status "$status" 0 "sampled with --sample 1"
  lists=$(grep -c "storage ls gs://$bucket/runs/[^ ]*/ " "$dir/logs/gcloud.calls" || true)
  [ "$lists" -eq 1 ] || fail "sampled: with --sample 1, runs/ was listed under $lists slugs"
}

test_the_slug_level_is_reached() {
  local dir=$work/slug-level
  setup_case "$dir"
  run_check "$dir" -- "$project" --config "$dir/config.yaml"
  expect_status "$status" 0 "slug level"
  grep -q "/config/caps/repos.json?shallow=true" "$dir/logs/curl.calls" ||
    fail "slug level: config/caps/repos was never read"
  expect_contains "$out" "[slug] RC40 rtdb config/caps/repos/<slug>" "slug level: the slug under config/caps/repos is marked and matched"
  expect_contains "$out" "$slug_a" "slug level: the slug itself is in the sample"
}

test_stdin_is_closed() {
  local dir=$work/stdin
  setup_case "$dir"
  local feed=$dir/feed
  local i
  for i in 1 2 3 4 5 6 7 8 9 10; do printf 'y\n'; done >"$feed"
  STDIN_FILE=$feed run_check "$dir" -- "$project" --config "$dir/config.yaml"
  expect_status "$status" 0 "stdin"
  [ ! -s "$dir/logs/stdin.reads" ] || fail "stdin: a call read the check's stdin: $(cat "$dir/logs/stdin.reads")"
  run_check "$dir" -- "$project" --config "$dir/config.yaml"
  expect_status "$status" 0 "stdin closed"
}

# Sharding, so the Go test can run these side by side: CHECK_TEST_SHARD="i/n"
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

t test_a_known_layout_passes
t test_an_unnamed_repo_keyed_path_fails
t test_only_reads_are_called
t test_values_are_never_printed
t test_the_token_is_never_on_argv
t test_runs_are_sampled
t test_the_slug_level_is_reached
t test_stdin_is_closed

if [ "$failures" -ne 0 ]; then
  echo "check-890-data-layout_test.sh: $failures failure(s)" >&2
  exit 1
fi
echo "check-890-data-layout_test.sh: ok"
