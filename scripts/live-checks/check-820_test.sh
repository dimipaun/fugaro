#!/usr/bin/env bash
# Tests for scripts/live-checks/check-820.sh. NOTHING here is a cloud, a project,
# an installation or a bucket: `gcloud`, `curl` and `fugaro` are scripts in $work
# whose answers each case owns, so no case can create a project, spend anything
# or reach anything. What the fakes model is what the check reads and cannot fake
# for itself:
#
#   * GCLOUD's read-only answers about one scratch project: whether it exists and
#     what it is called, the buckets it holds (before the setup and after it, so
#     the premise's empty project and the buckets row's created ones are two
#     different readings), each bucket's location, the `(default)` Firestore
#     database's location, the Artifact Registry repositories, the
#     `fugaro=managed` containers and each one's replication, the log buckets,
#     and one access token. Every verb and flag is checked against the real
#     command's own, and anything else is refused with 64.
#   * GOOGLE'S OWN APIs through curl: the Realtime Database instances list (the
#     only door to an instance's location), read through a 0600 curl config file
#     the check writes — the fake refuses a token on an argv or in a readable
#     file — and the three Realtime Database hosts' answers (U8).
#   * FUGARO as the installation's own commands: `setup --location eu` (the run
#     this check is about), `doctor --json`'s locations row (the record's only
#     reader), `run` (which records the directory it was called in, so a case
#     says the launch is made from the checkout and not from wherever the check
#     was started), `runs ls` and `budget show`, each with the flags the real
#     command has and nothing else.
#
# The cases are the readings bent one at a time: a project that is not there,
# another, one that already holds an installation, a name that already has a
# config, a setup that did not converge, a record doctor does not print or prints
# short or prints with Google's automatic replication, a Firestore database read
# nowhere else or read elsewhere, a Realtime Database in the US or not read at
# all, a bucket list neither the premise nor the buckets row can read, buckets in
# the EU (regional and the multi-region) and outside it, a bucket that cannot be
# read, a registry that is empty or elsewhere, a container replicated
# automatically or outside the region or not readable, a log bucket that is not
# there or not in the region, a host that answers something the key service
# cannot read — a status it does not recognise, and a body that only mentions a
# missing entity, which its wording does not cover — and a host whose body is
# one of the two phrases it does, on a status that is neither 401/403 nor 404,
# a checkout the launch is made from, a launch that refuses, a run the runs
# bucket does not hold, a budget read that fails, and every guard.
#
# Usage: bash scripts/live-checks/check-820_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check-820.sh
bashbin=$(command -v bash)
work=$(mktemp -d)
cleanup_all() {
  if [ -n "${CHECK820_TEST_KEEP:-}" ]; then echo "kept: $work" >&2; else rm -rf "$work"; fi
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c "${CHECK820_TEST_HEAD:-2000}")" ;;
  esac
}
expect_not_contains() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: expected NOT to contain '$2'" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '\n%s' "${out:-}" | head -c "${CHECK820_TEST_HEAD:-2000}")"
}
expect_row() { # out key state what
  if printf '%s\n' "$1" | awk -v k="$2" -v s="$3" '$1 == k && $2 == s { found = 1 } END { exit !found }'; then
    return 0
  fi
  fail "$4: expected the row '$2' to read $3; got: $(printf '%s' "$1" | head -c "${CHECK820_TEST_HEAD:-2000}")"
}
expect_count() { # file needle want what
  local got
  got=$(grep -c -- "$2" "$1" 2>/dev/null || true)
  [ "$got" = "$3" ] || fail "$4: expected $3 line(s) matching '$2' in $(basename "$1"), got $got"
}

# ---------------------------------------------------------------- the fakes

fakebin=$work/bin
emptybin=$work/emptybin
mkdir -p "$fakebin" "$emptybin"

# fake gcloud: the eight read-only verbs the check uses and one token. Every flag
# is checked against the command's own set (the flags the real command has), and
# a verb, a sub-verb or a flag outside them is refused with 64 rather than
# answered. `storage buckets list` answers the project BEFORE the setup on its
# first call and the buckets the setup made on its second, which is the whole
# point of the premise.
cat >"$fakebin/gcloud" <<'GCLOUD'
#!/usr/bin/env bash
set -euo pipefail
dir=${FAKE820_DIR:?}
printf 'gcloud %s\n' "$*" >>"${FAKE820_GLOG:?}"

key="$1 ${2:-}"
n=2
case "$key" in
"auth print-access-token" | "projects describe" | "secrets list" | "secrets describe") ;;
*)
  key="$key ${3:-}"
  n=3
  ;;
esac
case "$key" in
"projects describe") val="--format --project" ;;
"storage buckets list") val="--format --project" ;;
"storage buckets describe") val="--format --project" ;;
"firestore databases describe") val="--format --project --database" ;;
"artifacts repositories list") val="--format --project" ;;
"secrets list") val="--format --project --filter" ;;
"secrets describe") val="--format --project" ;;
"logging buckets list") val="--format --project" ;;
"auth print-access-token") val="" ;;
*)
  printf 'fake gcloud: %s is not a command Check 820 may run\n' "$*" >&2
  exit 64
  ;;
esac
shift "$n"

pos=()
while [ $# -gt 0 ]; do
  case "$1" in
  --*=*)
    f=${1%%=*}
    case " $val " in *" $f "*) ;; *) printf 'fake gcloud: %s is not a flag %s has\n' "$f" "$key" >&2; exit 64 ;; esac
    shift
    ;;
  --*)
    f=$1
    case " $val " in *" $f "*) ;; *) printf 'fake gcloud: %s is not a flag %s has\n' "$f" "$key" >&2; exit 64 ;; esac
    shift
    [ $# -ge 1 ] || { printf 'fake gcloud: %s needs a value\n' "$f" >&2; exit 64; }
    pos+=("$1")
    shift
    ;;
  -*)
    printf 'fake gcloud: %s is not a flag %s has\n' "$1" "$key" >&2
    exit 64
    ;;
  *)
    pos+=("$1")
    shift
    ;;
  esac
done

must() { # must <case-file> <what>
  local st
  st=$(cat "$dir/$1" 2>/dev/null || printf 0)
  [ "$st" = 0 ] && return 0
  printf 'fake gcloud: %s refused (ERROR: (gcloud) PERMISSION_DENIED)\n' "$2" >&2
  exit "$st"
}

case "$key" in
"auth print-access-token")
  must token.status token
  printf 'chk820-fake-access-token\n'
  ;;
"projects describe")
  must describe.status describe
  cat "$dir/project"
  ;;
"storage buckets list")
  n=0
  [ -f "$dir/.buckets-list-calls" ] && n=$(cat "$dir/.buckets-list-calls")
  n=$((n + 1))
  printf '%s' "$n" >"$dir/.buckets-list-calls"
  if [ -f "$dir/buckets-list.fail-at" ] && [ "$n" = "$(cat "$dir/buckets-list.fail-at")" ]; then
    printf 'fake gcloud: storage buckets list refused\n' >&2
    exit 1
  fi
  if [ "$n" -gt 1 ] && [ -f "$dir/buckets.after" ]; then
    cat "$dir/buckets.after"
  else
    cat "$dir/buckets"
  fi
  ;;
"storage buckets describe")
  must bucket-describe.status bucket-describe
  name=${pos[0]:-}
  bare=${name#gs://}
  if [ -f "$dir/bucket-unreadable" ] && [ "$bare" = "$(cat "$dir/bucket-unreadable")" ]; then
    printf 'fake gcloud: %s refused\n' "$name" >&2
    exit 1
  fi
  awk -F'\t' -v want="$bare" '$1 == want { print $2 }' "$dir/bucket-locations"
  ;;
"firestore databases describe")
  must firestore.status firestore
  cat "$dir/firestore"
  ;;
"artifacts repositories list")
  must registry.status registry
  cat "$dir/registry"
  ;;
"secrets list")
  must secrets-list.status secrets-list
  cat "$dir/secrets"
  ;;
"secrets describe")
  must secret-describe.status secret-describe
  name=${pos[0]:-}
  if [ -f "$dir/secret-unreadable" ] && [ "$name" = "$(cat "$dir/secret-unreadable")" ]; then
    printf 'fake gcloud: secrets/%s refused\n' "$name" >&2
    exit 1
  fi
  cat "$dir/secret-$name.json"
  ;;
"logging buckets list")
  must logbuckets.status logbuckets
  cat "$dir/logbuckets"
  ;;
esac
GCLOUD

# fake curl: the two calls the check makes and nothing else. The Realtime
# Database list is read through a --config file, and the fake checks that file is
# the check's own 0600 one carrying the bearer token — a token on an argv or in a
# world-readable file is a finding about the check, not about the project.
cat >"$fakebin/curl" <<'CURL'
#!/usr/bin/env bash
set -euo pipefail
dir=${FAKE820_DIR:?}
printf 'curl %s\n' "$*" >>"${FAKE820_CLOG:?}"

out="" url="" conf="" want_status=no
while [ $# -gt 0 ]; do
  case "$1" in
  -sS) shift ;;
  --max-time)
    [ "${2:-}" = 20 ] || { printf 'fake curl: --max-time %s is not the check being run\n' "${2:-}" >&2; exit 64; }
    shift 2
    ;;
  --config)
    [ $# -ge 2 ] || { printf 'fake curl: --config needs a file\n' >&2; exit 64; }
    conf=$2
    shift 2
    ;;
  -o)
    [ $# -ge 2 ] || { printf 'fake curl: -o needs a file\n' >&2; exit 64; }
    out=$2
    shift 2
    ;;
  -w)
    [ "${2:-}" = '%{http_code}' ] || { printf 'fake curl: -w %s is not the check being run\n' "${2:-}" >&2; exit 64; }
    want_status=yes
    shift 2
    ;;
  -*)
    printf 'fake curl: %s is not a flag Check 820 may pass\n' "$1" >&2
    exit 64
    ;;
  *)
    url=$1
    shift
    ;;
  esac
done
[ -n "$url" ] || { printf 'fake curl: no URL\n' >&2; exit 64; }
[ -n "$out" ] || { printf 'fake curl: no -o\n' >&2; exit 64; }

case "$url" in
https://firebasedatabase.googleapis.com/v1beta/projects/*/locations/-/instances)
  [ -n "$conf" ] || { printf 'fake curl: the instances list is read without a --config credential\n' >&2; exit 64; }
  [ -f "$conf" ] || { printf 'fake curl: the --config file is not there\n' >&2; exit 64; }
  case "$(stat -c '%a' "$conf" 2>/dev/null || printf none)" in
  600) ;;
  *) printf 'fake curl: the --config file is not 0600\n' >&2; exit 64 ;;
  esac
  grep -qx 'header = "Authorization: Bearer chk820-fake-access-token"' "$conf" ||
    { printf 'fake curl: the --config file does not carry the access token as a bearer header\n' >&2; exit 64; }
  printf '%s' "$conf" >"$dir/curl-config"
  if [ -f "$dir/rtdb.status" ] && [ "$(cat "$dir/rtdb.status")" != 0 ]; then
    printf 'fake curl: the instances list could not be read\n' >&2
    exit "$(cat "$dir/rtdb.status")"
  fi
  cp "$dir/rtdb.json" "$out"
  ;;
https://*-default-rtdb.*/.json)
  host=${url#https://}
  host=${host%%/*}
  cp "$dir/host.$host" "$out" 2>/dev/null || printf '{"error":"Permission denied"}\n' >"$out"
  code=$(cat "$dir/host.$host.code" 2>/dev/null || printf 401)
  printf '%s' "$code" >"$dir/host.$host.seen"
  if [ "$want_status" = yes ]; then printf '%s' "$code"; fi
  ;;
*)
  printf 'fake curl: %s is not a URL Check 820 reads\n' "$url" >&2
  exit 64
  ;;
esac
CURL

# fake fugaro: the five verbs the check runs, each flag checked against the real
# command's own (value-taking flags and boolean ones apart, so `--json` may not
# swallow the flag after it), a `--help` of its own, and 64 for anything else.
cat >"$fakebin/fugaro" <<'FUGARO'
#!/usr/bin/env bash
set -euo pipefail
dir=${FAKE820_DIR:?}
printf 'fugaro %s\n' "$*" >>"${FAKE820_LOG:?}"

case "${1:-}" in
-h | --help)
  cat <<'USAGE'
Fugaro drives coding-agent runs in your own cloud project.

Usage:
  fugaro [command]

Available Commands:
  budget      Read the installation's budget
  doctor      Check the installation, the local config and a checkout
  run         Launch a task in the cloud
  runs        Read the runs of an installation
  setup       Create or converge an installation
USAGE
  exit 0
  ;;
esac

key=${1:-}
n=1
case "$key" in
runs | budget)
  key="$key ${2:-}"
  n=2
  ;;
esac
case "$key" in
setup) val="--location --gcp-project --name --rigor"; boolf="--bake-repos" ;;
doctor) val="--project"; boolf="--json" ;;
run) val="--repo --project --title --task-file"; boolf="--json" ;;
"runs ls") val="--project --limit"; boolf="--json" ;;
"budget show") val="--project"; boolf="--json" ;;
*)
  printf 'fake fugaro: %s is not a command Check 820 may run\n' "$*" >&2
  exit 64
  ;;
esac
shift "$n"

pos=()
while [ $# -gt 0 ]; do
  case "$1" in
  --*=*)
    f=${1%%=*}
    case " $val $boolf " in *" $f "*) ;; *) printf 'fake fugaro: %s is not a flag %s has\n' "$f" "$key" >&2; exit 64 ;; esac
    shift
    ;;
  --*)
    f=$1
    case " $boolf " in
    *" $f "*) shift ;;
    *)
      case " $val " in *" $f "*) ;; *) printf 'fake fugaro: %s is not a flag %s has\n' "$f" "$key" >&2; exit 64 ;; esac
      shift
      [ $# -ge 1 ] || { printf 'fake fugaro: %s needs a value\n' "$f" >&2; exit 64; }
      pos+=("$1")
      shift
      ;;
    esac
    ;;
  -*)
    printf 'fake fugaro: %s is not a flag %s has\n' "$1" "$key" >&2
    exit 64
    ;;
  *)
    pos+=("$1")
    shift
    ;;
  esac
done

must() { # must <case-file> <what>
  local st
  st=$(cat "$dir/$1" 2>/dev/null || printf 0)
  [ "$st" = 0 ] && return 0
  printf 'fake fugaro: %s refused\n' "$2" >&2
  [ ! -f "$dir/$2.err" ] || cat "$dir/$2.err" >&2
  exit "$st"
}

case "$key" in
setup)
  must setup.status setup
  printf 'fugaro: the installation is created\n'
  ;;
doctor)
  must doctor.status doctor
  cat "$dir/doctor.json"
  ;;
run)
  must run.status run
  printf '%s' "$PWD" >"$dir/run.cwd"
  cat "$dir/run.json"
  ;;
"runs ls")
  must runs.status runs
  cat "$dir/runs.json"
  ;;
"budget show")
  must budget.status budget
  cat "$dir/budget.json"
  ;;
esac
FUGARO
chmod +x "$fakebin/gcloud" "$fakebin/curl" "$fakebin/fugaro"

# ---------------------------------------------------------------- the cases

PROJECT=chk820p
NAME=chk820
HOSTS="chk820p-default-rtdb.firebaseio.com chk820p-default-rtdb.europe-west1.firebasedatabase.app chk820p-default-rtdb.asia-southeast1.firebasedatabase.app"

replication_doc() { # replication_doc <container> <location|automatic>
  if [ "$2" = automatic ]; then
    printf '{"name":"%s","replication":{"automatic":{}}}\n' "$1" >"$dir/secret-$1.json"
    return 0
  fi
  printf '{"name":"%s","replication":{"userManaged":{"replicas":[{"location":"%s"}]}}}\n' "$1" "$2" >"$dir/secret-$1.json"
}

locations_row() { # locations_row <firestore> <rtdb> <secret sentence> <log bucket>
  printf 'its Firestore database is in %s; its Realtime Database is in %s; its secret containers are %s; its run log bucket is in %s' \
    "$1" "$2" "$3" "$4"
}

doctor_doc() { # doctor_doc <locations row>
  printf '{"project":{"name":"chk820"},"checks":[{"id":"project","problem":""},{"id":"locations","problem":"%s"}]}\n' "$1" >"$dir/doctor.json"
}

case_dir() { # case_dir <name>
  dir=$work/$1
  rm -rf "$dir"
  mkdir -p "$dir/config/fugaro/projects"
  : >"$dir/CALLS"
  : >"$dir/GCALLS"
  : >"$dir/CCALLS"
  printf 'chk820p\tACTIVE\n' >"$dir/project"
  : >"$dir/buckets"
  printf 'fugaro-runs-chk820p\nfugaro-tfstate-chk820p\n' >"$dir/buckets.after"
  printf 'fugaro-runs-chk820p\teurope-west1\nfugaro-tfstate-chk820p\teurope-west1\n' >"$dir/bucket-locations"
  printf 'europe-west1\n' >"$dir/firestore"
  printf '{"instances":[{"name":"projects/chk820p/locations/europe-west1/instances/chk820p-default-rtdb","type":"DEFAULT_DATABASE","databaseUrl":"https://chk820p-default-rtdb.europe-west1.firebasedatabase.app"}]}\n' >"$dir/rtdb.json"
  printf 'projects/chk820p/locations/europe-west1/repositories/base\nprojects/chk820p/locations/europe-west1/repositories/history\n' >"$dir/registry"
  printf 'fugaro-agent\nfugaro-budget\n' >"$dir/secrets"
  replication_doc fugaro-agent europe-west1
  replication_doc fugaro-budget europe-west1
  printf '_Default\tglobal\nfugaro\teurope-west1\n' >"$dir/logbuckets"
  local h
  for h in $HOSTS; do
    printf '{"error":"Permission denied"}\n' >"$dir/host.$h"
    printf '401' >"$dir/host.$h.code"
  done
  printf '{"error":"Database not found"}\n' >"$dir/host.chk820p-default-rtdb.firebaseio.com"
  printf '404' >"$dir/host.chk820p-default-rtdb.firebaseio.com.code"
  doctor_doc "$(locations_row europe-west1 europe-west1 'replicated in europe-west1' europe-west1)"
  printf '{"run_id":"chk820-run-1","status":"launched"}\n' >"$dir/run.json"
  printf '{"project":"chk820","runs":[{"run_id":"chk820-run-1","title":"Check 820: a first EU installation","status":"running"}]}\n' >"$dir/runs.json"
  printf '{"global":{"leased_usd":1.25},"repos":[]}\n' >"$dir/budget.json"
}

run820_raw() { # run820_raw <args...>
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    XDG_CONFIG_HOME="$dir/config" PATH="$fakebin:$PATH" \
    FAKE820_DIR="$dir" FAKE820_LOG="$dir/CALLS" FAKE820_GLOG="$dir/GCALLS" \
    FAKE820_CLOG="$dir/CCALLS" \
    bash "$script" "$@" 2>"$dir/err") || status=$?
  err=$(cat "$dir/err")
}

run820() { # run820 [extra args...]
  run820_raw "$PROJECT" "$@"
}

# ------------------------------------------------------------------- the cases

test_the_happy_path() {
  case_dir happy_path
  run820
  expect_status "$status" 0 "the happy path"
  expect_contains "$out" "CHECK 820 RESULT: PASS (exit 0)" "the happy path"
  expect_row "$out" premise PASS "the happy path"
  expect_row "$out" setup PASS "the happy path"
  expect_row "$out" record PASS "the happy path"
  expect_row "$out" firestore PASS "the happy path"
  expect_row "$out" rtdb PASS "the happy path"
  expect_row "$out" buckets PASS "the happy path"
  expect_row "$out" registry PASS "the happy path"
  expect_row "$out" secrets PASS "the happy path"
  expect_row "$out" logbucket PASS "the happy path"
  expect_row "$out" hosts PASS "the happy path"
  expect_row "$out" run PASS "the happy path"
  expect_row "$out" restore PASS "the happy path"
  expect_not_contains "$out" "UNVERIFIED" "the happy path"
  # The run the check is about, and the four reads it makes of it.
  expect_count "$dir/CALLS" "fugaro setup --location eu --gcp-project chk820p --name chk820 --rigor poc --bake-repos" 1 "the happy path"
  expect_count "$dir/CALLS" "fugaro doctor --json --project chk820" 1 "the happy path"
  expect_count "$dir/CALLS" "fugaro run --repo $PWD --project chk820 --title Check 820: a first EU installation --task-file" 1 "the happy path"
  [ "$(cat "$dir/run.cwd" 2>/dev/null)" = "$PWD" ] || fail "the happy path: 'fugaro run' was launched from '$(cat "$dir/run.cwd" 2>/dev/null)' and not from the checkout '$PWD'"
  expect_count "$dir/CALLS" "fugaro runs ls --json --project chk820 --limit 5" 1 "the happy path"
  expect_count "$dir/CALLS" "fugaro budget show --json --project chk820" 1 "the happy path"
  # Every located thing is read from the cloud itself, not only from the record.
  expect_count "$dir/GCALLS" "gcloud projects describe chk820p --format=value(projectId,lifecycleState)" 1 "the happy path"
  expect_count "$dir/GCALLS" "gcloud storage buckets list --project=chk820p --format=value(name)" 2 "the happy path"
  expect_count "$dir/GCALLS" "gcloud firestore databases describe --database=(default) --project=chk820p --format=value(locationId)" 1 "the happy path"
  expect_count "$dir/GCALLS" "gcloud auth print-access-token" 1 "the happy path"
  expect_count "$dir/GCALLS" "gcloud storage buckets describe gs://fugaro-runs-chk820p --project=chk820p --format=value(location)" 1 "the happy path"
  expect_count "$dir/GCALLS" "gcloud storage buckets describe gs://fugaro-tfstate-chk820p --project=chk820p --format=value(location)" 1 "the happy path"
  expect_count "$dir/GCALLS" "gcloud artifacts repositories list --project=chk820p --format=value(name)" 1 "the happy path"
  expect_count "$dir/GCALLS" "gcloud secrets list --project=chk820p --filter=labels.fugaro=managed --format=value(name)" 1 "the happy path"
  expect_count "$dir/GCALLS" "gcloud secrets describe fugaro-agent --project=chk820p --format=json" 1 "the happy path"
  expect_count "$dir/GCALLS" "gcloud secrets describe fugaro-budget --project=chk820p --format=json" 1 "the happy path"
  expect_count "$dir/GCALLS" "gcloud logging buckets list --project=chk820p --format=value(name,location)" 1 "the happy path"
  # The access token is in the check's own 0600 config file (the fake refuses
  # anything else), on no argv and in nothing the check prints.
  [ -f "$dir/curl-config" ] || fail "the happy path: the instances list was not read through a config file"
  expect_not_contains "$out" "chk820-fake-access-token" "the happy path: the token is in nothing printed"
  expect_not_contains "$(cat "$dir/CALLS" "$dir/GCALLS" "$dir/CCALLS")" "chk820-fake-access-token" "the happy path: the token is on no argv"
  # The three hosts are asked, and their answers are in the block.
  expect_count "$dir/CCALLS" "https://chk820p-default-rtdb.firebaseio.com/.json" 1 "the happy path"
  expect_count "$dir/CCALLS" "https://chk820p-default-rtdb.europe-west1.firebasedatabase.app/.json" 1 "the happy path"
  expect_count "$dir/CCALLS" "https://chk820p-default-rtdb.asia-southeast1.firebasedatabase.app/.json" 1 "the happy path"
  expect_contains "$out" "default-rtdb.firebaseio.com: no database at this host" "the happy path"
  expect_contains "$out" "default-rtdb.europe-west1.firebasedatabase.app: a locked database (an installation answers here)" "the happy path"
  # The restore row names the one command that removes the scratch project.
  expect_contains "$out" "gcloud projects delete chk820p --quiet" "the happy path"
}

test_help_prints_the_usage_and_calls_nothing() {
  case_dir help
  run820_raw --help
  expect_status "$status" 0 "--help"
  expect_contains "$out" "Check 820" "--help"
  expect_contains "$out" "bash scripts/live-checks/check-820.sh <project>" "--help"
  [ ! -s "$dir/CALLS" ] || fail "--help: it called fugaro: $(head -c 300 "$dir/CALLS")"
  [ ! -s "$dir/GCALLS" ] || fail "--help: it called gcloud: $(head -c 300 "$dir/GCALLS")"
  [ ! -s "$dir/CCALLS" ] || fail "--help: it called curl: $(head -c 300 "$dir/CCALLS")"
  expect_not_contains "$out" "CHECK 820 RESULT" "--help"
}

test_a_dry_run_prints_the_plan_and_calls_nothing() {
  case_dir dry_run
  run820 --dry-run
  expect_status "$status" 0 "a dry run"
  expect_contains "$out" "Check 820 plan (nothing is called: no gcloud, no fugaro, no curl)" "a dry run"
  expect_contains "$out" "fugaro setup --location eu --gcp-project chk820p --name chk820 --rigor poc --bake-repos" "a dry run"
  expect_contains "$out" "gcloud projects delete chk820p --quiet" "a dry run"
  [ ! -s "$dir/CALLS" ] || fail "a dry run: it called fugaro: $(head -c 300 "$dir/CALLS")"
  [ ! -s "$dir/GCALLS" ] || fail "a dry run: it called gcloud: $(head -c 300 "$dir/GCALLS")"
  [ ! -s "$dir/CCALLS" ] || fail "a dry run: it called curl: $(head -c 300 "$dir/CCALLS")"
  expect_not_contains "$out" "CHECK 820 RESULT" "a dry run"
}

test_no_project_is_refused() {
  case_dir no_project
  run820_raw
  expect_status "$status" 2 "no project"
  expect_contains "$err" "no project id was given" "no project"
  [ ! -s "$dir/GCALLS" ] || fail "no project: it called gcloud"
}

test_two_projects_are_refused() {
  case_dir two_projects
  run820_raw chk820p chk820q
  expect_status "$status" 2 "two projects"
  expect_contains "$err" "two project ids were given" "two projects"
}

test_an_unknown_argument_is_refused() {
  case_dir unknown_argument
  run820 --nonsense
  expect_status "$status" 2 "an unknown argument"
  expect_contains "$err" "unknown argument --nonsense" "an unknown argument"
  [ ! -s "$dir/GCALLS" ] || fail "an unknown argument: it called gcloud"
}

test_a_flag_without_a_value_is_refused() {
  case_dir flag_without_value
  run820 --name
  expect_status "$status" 2 "--name with no value"
  expect_contains "$err" "--name needs the installation's name" "--name with no value"
}

test_an_agent_session_is_refused() {
  case_dir agent
  status=0
  out=$(env CLAUDECODE=1 XDG_CONFIG_HOME="$dir/config" PATH="$fakebin:$PATH" \
    FAKE820_DIR="$dir" FAKE820_LOG="$dir/CALLS" FAKE820_GLOG="$dir/GCALLS" \
    FAKE820_CLOG="$dir/CCALLS" \
    bash "$script" "$PROJECT" 2>"$dir/err") || status=$?
  err=$(cat "$dir/err")
  expect_status "$status" 2 "an agent session"
  expect_contains "$err" "this shell carries CLAUDECODE" "an agent session"
  [ ! -s "$dir/GCALLS" ] || fail "an agent session: it called gcloud"
  [ ! -s "$dir/CALLS" ] || fail "an agent session: it called fugaro"
}

test_a_traced_shell_is_refused() {
  case_dir xtrace
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    XDG_CONFIG_HOME="$dir/config" PATH="$fakebin:$PATH" \
    FAKE820_DIR="$dir" FAKE820_LOG="$dir/CALLS" FAKE820_GLOG="$dir/GCALLS" \
    FAKE820_CLOG="$dir/CCALLS" \
    bash -x "$script" "$PROJECT" 2>"$dir/err") || status=$?
  err=$(cat "$dir/err")
  expect_status "$status" 2 "a traced shell"
  expect_contains "$err" "this shell is tracing" "a traced shell"
  [ ! -s "$dir/GCALLS" ] || fail "a traced shell: it called gcloud"
}

test_a_project_that_cannot_be_read_stops_the_check() {
  case_dir project_unreadable
  printf '1' >"$dir/describe.status"
  run820
  expect_status "$status" 2 "a project that cannot be read"
  expect_row "$out" premise FAIL "a project that cannot be read"
  expect_contains "$out" "aborted" "a project that cannot be read"
  expect_contains "$out" "could not be read" "a project that cannot be read"
  [ ! -s "$dir/CALLS" ] || fail "a project that cannot be read: it called fugaro"
}

test_another_project_is_stopped() {
  case_dir another_project
  printf 'chk820q\tACTIVE\n' >"$dir/project"
  run820
  expect_status "$status" 2 "another project"
  expect_contains "$out" "gcloud names the project 'chk820q' and not chk820p" "another project"
  [ ! -s "$dir/CALLS" ] || fail "another project: it called fugaro"
}

test_a_project_that_already_holds_an_installation_is_stopped() {
  case_dir already_installed
  printf 'fugaro-runs-chk820p\nsome-other-bucket\n' >"$dir/buckets"
  run820
  expect_status "$status" 2 "a project that already holds an installation"
  expect_contains "$out" "already holds fugaro-runs-chk820p" "a project that already holds an installation"
  expect_contains "$out" "never held one" "a project that already holds an installation"
  [ ! -s "$dir/CALLS" ] || fail "a project that already holds an installation: it called fugaro"
}

test_a_name_that_already_has_a_config_is_stopped() {
  case_dir name_taken
  : >"$dir/config/fugaro/projects/chk820.yaml"
  run820
  expect_status "$status" 2 "a name that already has a config"
  expect_contains "$out" "already has a local config for the project name chk820" "a name that already has a config"
  expect_contains "$out" "Name another with --name" "a name that already has a config"
  [ ! -s "$dir/CALLS" ] || fail "a name that already has a config: it called fugaro"
}

test_another_name_runs_in_the_same_project() {
  case_dir other_name
  : >"$dir/config/fugaro/projects/chk820.yaml"
  run820 --name chk820b
  expect_status "$status" 0 "another name"
  expect_row "$out" setup PASS "another name"
  expect_count "$dir/CALLS" "fugaro setup --location eu --gcp-project chk820p --name chk820b" 1 "another name"
}

test_a_setup_that_did_not_converge_is_a_finding() {
  case_dir setup_failed
  printf '1' >"$dir/setup.status"
  printf 'fugaro: the Firestore database could not be placed\n' >"$dir/setup.err"
  run820
  expect_status "$status" 1 "a setup that did not converge"
  expect_row "$out" setup FAIL "a setup that did not converge"
  expect_contains "$out" "did not exit 0 (exit 1)" "a setup that did not converge"
  # The rows below still read what is there rather than assuming the setup worked.
  expect_row "$out" record PASS "a setup that did not converge"
}

test_a_record_with_no_locations_row_is_unverified() {
  case_dir no_locations_row
  printf '{"project":{"name":"chk820"},"checks":[{"id":"project","problem":""}]}\n' >"$dir/doctor.json"
  run820
  expect_status "$status" 3 "no locations row"
  expect_row "$out" record UNVERIFIED "no locations row"
  expect_contains "$out" "carries no locations row" "no locations row"
}

test_a_record_that_is_short_of_a_location_is_a_finding() {
  case_dir short_record
  doctor_doc "$(locations_row europe-west1 europe-west1 'replicated in europe-west1' global)"
  run820
  expect_status "$status" 1 "a record short of a location"
  expect_row "$out" record FAIL "a record short of a location"
  expect_contains "$out" "its run log bucket is in europe-west1" "a record short of a location"
}

test_a_record_that_reads_googles_automatic_replication_is_a_finding() {
  case_dir automatic_record
  doctor_doc "$(locations_row europe-west1 europe-west1 "replicated with Google's automatic replication, which today means the United States" europe-west1)"
  run820
  expect_status "$status" 1 "a record reading Google's automatic replication"
  expect_row "$out" record FAIL "a record reading Google's automatic replication"
  expect_contains "$out" "replicated in europe-west1" "a record reading Google's automatic replication"
}

test_a_firestore_read_that_fails_is_a_finding() {
  case_dir firestore_unreadable
  printf '1' >"$dir/firestore.status"
  run820
  expect_status "$status" 1 "a Firestore read that fails"
  expect_row "$out" firestore FAIL "a Firestore read that fails"
}

test_a_firestore_database_elsewhere_is_a_finding() {
  case_dir firestore_us
  printf 'us-east5\n' >"$dir/firestore"
  run820
  expect_status "$status" 1 "a Firestore database elsewhere"
  expect_row "$out" firestore FAIL "a Firestore database elsewhere"
  expect_contains "$out" "locationId is 'us-east5' and not europe-west1" "a Firestore database elsewhere"
}

test_a_realtime_database_read_that_fails_is_a_finding() {
  case_dir rtdb_unreadable
  printf '{"error":{"code":403,"message":"The caller does not have permission"}}\n' >"$dir/rtdb.json"
  run820
  expect_status "$status" 1 "a Realtime Database read that fails"
  expect_row "$out" rtdb FAIL "a Realtime Database read that fails"
  expect_contains "$out" "The caller does not have permission" "a Realtime Database read that fails"
}

test_a_realtime_database_in_the_us_is_a_finding() {
  case_dir rtdb_us
  printf '{"instances":[{"name":"projects/chk820p/locations/us-central1/instances/chk820p-default-rtdb","type":"DEFAULT_DATABASE","databaseUrl":"https://chk820p-default-rtdb.firebaseio.com"}]}\n' >"$dir/rtdb.json"
  run820
  expect_status "$status" 1 "a Realtime Database in the US"
  expect_row "$out" rtdb FAIL "a Realtime Database in the US"
  expect_contains "$out" "default instance is in 'us-central1' and not europe-west1" "a Realtime Database in the US"
}

test_a_bucket_list_the_premise_cannot_read_stops_the_check() {
  case_dir buckets_list_premise
  printf '1' >"$dir/buckets-list.fail-at"
  run820
  expect_status "$status" 2 "a bucket list the premise cannot read"
  expect_row "$out" premise FAIL "a bucket list the premise cannot read"
  expect_contains "$out" "the scratch project's buckets could not be listed" "a bucket list the premise cannot read"
  [ ! -s "$dir/CALLS" ] || fail "a bucket list the premise cannot read: it called fugaro"
}

test_a_bucket_list_the_buckets_row_cannot_read_is_a_finding() {
  case_dir buckets_list_row
  printf '2' >"$dir/buckets-list.fail-at"
  run820
  expect_status "$status" 1 "a bucket list the buckets row cannot read"
  expect_row "$out" premise PASS "a bucket list the buckets row cannot read"
  expect_row "$out" buckets FAIL "a bucket list the buckets row cannot read"
  expect_contains "$out" "so where the installation's buckets are is NOT measured" "a bucket list the buckets row cannot read"
}

test_no_bucket_at_all_is_unverified() {
  case_dir no_buckets
  : >"$dir/buckets.after"
  run820
  expect_status "$status" 3 "no bucket at all"
  expect_row "$out" buckets UNVERIFIED "no bucket at all"
}

test_a_bucket_outside_the_eu_is_a_finding() {
  case_dir bucket_us
  printf 'fugaro-runs-chk820p\teurope-west1\nfugaro-tfstate-chk820p\tUS\n' >"$dir/bucket-locations"
  run820
  expect_status "$status" 1 "a bucket outside the EU"
  expect_row "$out" buckets FAIL "a bucket outside the EU"
  expect_contains "$out" "these buckets are not in the EU: fugaro-tfstate-chk820p US" "a bucket outside the EU"
}

test_a_bucket_in_the_multi_region_is_in_the_eu() {
  case_dir bucket_eu_multi
  printf 'fugaro-runs-chk820p\tEU\nfugaro-tfstate-chk820p\teurope-west1\n' >"$dir/bucket-locations"
  run820
  expect_status "$status" 0 "a bucket in the EU multi-region"
  expect_row "$out" buckets PASS "a bucket in the EU multi-region"
}

test_a_bucket_in_europe_but_outside_the_union_is_a_finding() {
  case_dir bucket_london
  printf 'fugaro-runs-chk820p\teurope-west2\nfugaro-tfstate-chk820p\teurope-west1\n' >"$dir/bucket-locations"
  run820
  expect_status "$status" 1 "a bucket in London"
  expect_row "$out" buckets FAIL "a bucket in London"
  expect_contains "$out" "these buckets are not in the EU: fugaro-runs-chk820p europe-west2" "a bucket in London"
}

test_a_bucket_that_cannot_be_read_is_a_finding() {
  case_dir bucket_unreadable
  printf 'fugaro-tfstate-chk820p' >"$dir/bucket-unreadable"
  run820
  expect_status "$status" 1 "a bucket that cannot be read"
  expect_row "$out" buckets FAIL "a bucket that cannot be read"
  expect_contains "$out" "these buckets could not be read: fugaro-tfstate-chk820p (unreadable)" "a bucket that cannot be read"
}

test_no_repository_is_unverified() {
  case_dir no_registry
  : >"$dir/registry"
  run820
  expect_status "$status" 3 "no repository"
  expect_row "$out" registry UNVERIFIED "no repository"
}

test_a_repository_elsewhere_is_a_finding() {
  case_dir registry_us
  printf 'projects/chk820p/locations/us-east5/repositories/base\n' >"$dir/registry"
  run820
  expect_status "$status" 1 "a repository elsewhere"
  expect_row "$out" registry FAIL "a repository elsewhere"
  expect_contains "$out" "these repositories are not in europe-west1: projects/chk820p/locations/us-east5/repositories/base" "a repository elsewhere"
}

test_no_container_is_unverified() {
  case_dir no_secrets
  : >"$dir/secrets"
  run820
  expect_status "$status" 3 "no container"
  expect_row "$out" secrets UNVERIFIED "no container"
}

test_a_container_replicated_automatically_is_a_finding() {
  case_dir secret_automatic
  replication_doc fugaro-agent automatic
  run820
  expect_status "$status" 1 "an automatic container"
  expect_row "$out" secrets FAIL "an automatic container"
  expect_contains "$out" "these containers are replicated AUTOMATICALLY: fugaro-agent" "an automatic container"
}

test_a_container_outside_the_region_is_a_finding() {
  case_dir secret_us
  replication_doc fugaro-budget us-central1
  run820
  expect_status "$status" 1 "a container outside the region"
  expect_row "$out" secrets FAIL "a container outside the region"
  expect_contains "$out" "these containers are not replicated in the installation's region: fugaro-budget in us-central1" "a container outside the region"
}

test_a_container_whose_replication_cannot_be_read_is_a_finding() {
  case_dir secret_unreadable
  printf 'fugaro-agent' >"$dir/secret-unreadable"
  run820
  expect_status "$status" 1 "a container that cannot be read"
  expect_row "$out" secrets FAIL "a container that cannot be read"
  expect_contains "$out" "the secret containers could not be read" "a container that cannot be read"
}

test_a_log_bucket_that_is_not_there_is_unverified() {
  case_dir no_log_bucket
  printf '_Default\tglobal\n' >"$dir/logbuckets"
  run820
  expect_status "$status" 3 "no log bucket"
  expect_row "$out" logbucket UNVERIFIED "no log bucket"
}

test_a_log_bucket_in_the_wrong_place_is_a_finding() {
  case_dir log_bucket_global
  printf '_Default\tglobal\nfugaro\tglobal\n' >"$dir/logbuckets"
  run820
  expect_status "$status" 1 "a log bucket in the wrong place"
  expect_row "$out" logbucket FAIL "a log bucket in the wrong place"
  expect_contains "$out" "log bucket fugaro is in 'global' and not europe-west1" "a log bucket in the wrong place"
}

test_a_host_that_answers_nothing_the_key_service_reads_is_a_finding() {
  case_dir host_500
  printf '{"error":"Internal error"}\n' >"$dir/host.chk820p-default-rtdb.europe-west1.firebasedatabase.app"
  printf '500' >"$dir/host.chk820p-default-rtdb.europe-west1.firebasedatabase.app.code"
  run820
  expect_status "$status" 1 "a host that answers 500"
  expect_row "$out" hosts FAIL "a host that answers 500"
  expect_contains "$out" "default-rtdb.europe-west1.firebasedatabase.app answered 500" "a host that answers 500"
}

test_a_body_that_only_mentions_a_missing_entity_is_a_finding() {
  case_dir host_not_found
  # The key service reads this answer as a host that did NOT answer: its
  # "missing database" wording is the database's own two phrases, and a body
  # that merely says an entity was not found is neither of them.
  printf '{"error":{"code":404,"message":"Requested entity was not found."}}\n' \
    >"$dir/host.chk820p-default-rtdb.europe-west1.firebasedatabase.app"
  printf '200' >"$dir/host.chk820p-default-rtdb.europe-west1.firebasedatabase.app.code"
  run820
  expect_status "$status" 1 "a body that only mentions a missing entity"
  expect_row "$out" hosts FAIL "a body that only mentions a missing entity"
  expect_contains "$out" 'default-rtdb.europe-west1.firebasedatabase.app answered 200 ({"error":{"code":404,"message":"Requested entity was not found."}})' "a body that only mentions a missing entity"
  expect_contains "$out" "a host that DID NOT ANSWER" "a body that only mentions a missing entity"
}

test_a_body_saying_no_such_database_is_no_database() {
  case_dir host_no_such
  # The fallback LP15's read has beside the status code: the phrase, on a
  # status that is neither 401/403 nor 404.
  printf '{"error":"no such database"}\n' >"$dir/host.chk820p-default-rtdb.asia-southeast1.firebasedatabase.app"
  printf '400' >"$dir/host.chk820p-default-rtdb.asia-southeast1.firebasedatabase.app.code"
  run820
  expect_status "$status" 0 "a body saying no such database"
  expect_row "$out" hosts PASS "a body saying no such database"
  expect_contains "$out" 'default-rtdb.asia-southeast1.firebasedatabase.app: no database at this host ({"error":"no such database"})' "a body saying no such database"
}

test_the_run_launches_from_the_checkout() {
  case_dir run_repo
  mkdir -p "$dir/repo"
  run820 --repo "$dir/repo"
  expect_status "$status" 0 "the run launches from the checkout"
  expect_row "$out" run PASS "the run launches from the checkout"
  # The checkout the owner named is the one the launch is made from, and the
  # one it names: `fugaro run` resolves the repository from where it is
  # called.
  expect_count "$dir/CALLS" "fugaro run --repo $dir/repo --project chk820 --title Check 820: a first EU installation --task-file" 1 "the run launches from the checkout"
  [ "$(cat "$dir/run.cwd" 2>/dev/null)" = "$dir/repo" ] ||
    fail "the run launches from the checkout: 'fugaro run' ran in '$(cat "$dir/run.cwd" 2>/dev/null)' and not in '$dir/repo'"
}

test_a_launch_that_refuses_is_a_finding() {
  case_dir launch_refused
  printf '1' >"$dir/run.status"
  printf 'fugaro: the project has no agent configured\n' >"$dir/run.err"
  run820
  expect_status "$status" 1 "a launch that refuses"
  expect_row "$out" run FAIL "a launch that refuses"
  expect_contains "$out" "'fugaro run --json' did not launch (exit 1)" "a launch that refuses"
  expect_contains "$out" "the project has no agent configured" "a launch that refuses"
  expect_count "$dir/CALLS" "fugaro runs ls" 0 "a launch that refuses"
}

test_a_runs_read_that_fails_is_a_finding() {
  case_dir runs_unreadable
  printf '1' >"$dir/runs.status"
  run820
  expect_status "$status" 1 "a runs read that fails"
  expect_row "$out" run FAIL "a runs read that fails"
  expect_contains "$out" "'fugaro runs ls --json' failed (1)" "a runs read that fails"
}

test_a_run_the_bucket_does_not_hold_is_a_finding() {
  case_dir run_missing
  printf '{"project":"chk820","runs":[]}\n' >"$dir/runs.json"
  run820
  expect_status "$status" 1 "a run the bucket does not hold"
  expect_row "$out" run FAIL "a run the bucket does not hold"
  expect_contains "$out" "is not among the runs 'fugaro runs ls --json' lists" "a run the bucket does not hold"
}

test_a_budget_read_that_fails_still_passes_the_run() {
  case_dir budget_unreadable
  printf '1' >"$dir/budget.status"
  run820
  expect_status "$status" 0 "a budget read that fails"
  expect_row "$out" run PASS "a budget read that fails"
  expect_not_contains "$out" "leased for the project" "a budget read that fails"
}

test_a_tool_that_is_not_on_path_stops_the_check() {
  case_dir no_tools
  status=0
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT \
    XDG_CONFIG_HOME="$dir/config" PATH="$emptybin" \
    FAKE820_DIR="$dir" FAKE820_LOG="$dir/CALLS" FAKE820_GLOG="$dir/GCALLS" \
    FAKE820_CLOG="$dir/CCALLS" \
    "$bashbin" "$script" "$PROJECT" 2>"$dir/err") || status=$?
  err=$(cat "$dir/err")
  expect_status "$status" 2 "a tool that is not on PATH"
  expect_contains "$err" "is not on PATH, and this check reads a real project with it" "a tool that is not on PATH"
}

# The fakes are the strictness every case rests on, so they are driven directly:
# the flag sets are the real commands' own, and anything else is 64.
test_the_fakes_refuse_what_the_real_commands_do_not_have() {
  case_dir fake_strictness
  local st
  st=0
  PATH="$fakebin:$PATH" FAKE820_DIR="$dir" FAKE820_LOG="$dir/CALLS" FAKE820_GLOG="$dir/GCALLS" \
    FAKE820_CLOG="$dir/CCALLS" fugaro runs bogus >/dev/null 2>&1 || st=$?
  [ "$st" = 64 ] || fail "the fake fugaro: a sub-verb it does not have was not refused with 64 (got $st)"
  st=0
  PATH="$fakebin:$PATH" FAKE820_DIR="$dir" FAKE820_LOG="$dir/CALLS" FAKE820_GLOG="$dir/GCALLS" \
    FAKE820_CLOG="$dir/CCALLS" fugaro doctor --yes >/dev/null 2>&1 || st=$?
  [ "$st" = 64 ] || fail "the fake fugaro: a flag doctor does not have was not refused with 64 (got $st)"
  st=0
  PATH="$fakebin:$PATH" FAKE820_DIR="$dir" FAKE820_LOG="$dir/CALLS" FAKE820_GLOG="$dir/GCALLS" \
    FAKE820_CLOG="$dir/CCALLS" gcloud storage buckets describe gs://b --project=p '--format=value(location)' --bogus=1 >/dev/null 2>&1 || st=$?
  [ "$st" = 64 ] || fail "the fake gcloud: a flag the real command does not have was not refused with 64 (got $st)"
  st=0
  PATH="$fakebin:$PATH" FAKE820_DIR="$dir" FAKE820_LOG="$dir/CALLS" FAKE820_GLOG="$dir/GCALLS" \
    FAKE820_CLOG="$dir/CCALLS" gcloud projects delete p --quiet >/dev/null 2>&1 || st=$?
  [ "$st" = 64 ] || fail "the fake gcloud: a writing verb was not refused with 64 (got $st)"
  local help
  help=$(PATH="$fakebin:$PATH" FAKE820_DIR="$dir" FAKE820_LOG="$dir/CALLS" fugaro --help 2>&1 || true)
  expect_contains "$help" "setup" "the fake fugaro's own --help"
  expect_contains "$help" "doctor" "the fake fugaro's own --help"
}

# ------------------------------------------------------------------- the footer

test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK820_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK820_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_the_happy_path
t test_help_prints_the_usage_and_calls_nothing
t test_a_dry_run_prints_the_plan_and_calls_nothing
t test_no_project_is_refused
t test_two_projects_are_refused
t test_an_unknown_argument_is_refused
t test_a_flag_without_a_value_is_refused
t test_an_agent_session_is_refused
t test_a_traced_shell_is_refused
t test_a_project_that_cannot_be_read_stops_the_check
t test_another_project_is_stopped
t test_a_project_that_already_holds_an_installation_is_stopped
t test_a_name_that_already_has_a_config_is_stopped
t test_another_name_runs_in_the_same_project
t test_a_setup_that_did_not_converge_is_a_finding
t test_a_record_with_no_locations_row_is_unverified
t test_a_record_that_is_short_of_a_location_is_a_finding
t test_a_record_that_reads_googles_automatic_replication_is_a_finding
t test_a_firestore_read_that_fails_is_a_finding
t test_a_firestore_database_elsewhere_is_a_finding
t test_a_realtime_database_read_that_fails_is_a_finding
t test_a_realtime_database_in_the_us_is_a_finding
t test_a_bucket_list_the_premise_cannot_read_stops_the_check
t test_a_bucket_list_the_buckets_row_cannot_read_is_a_finding
t test_no_bucket_at_all_is_unverified
t test_a_bucket_outside_the_eu_is_a_finding
t test_a_bucket_in_the_multi_region_is_in_the_eu
t test_a_bucket_in_europe_but_outside_the_union_is_a_finding
t test_a_bucket_that_cannot_be_read_is_a_finding
t test_no_repository_is_unverified
t test_a_repository_elsewhere_is_a_finding
t test_no_container_is_unverified
t test_a_container_replicated_automatically_is_a_finding
t test_a_container_outside_the_region_is_a_finding
t test_a_container_whose_replication_cannot_be_read_is_a_finding
t test_a_log_bucket_that_is_not_there_is_unverified
t test_a_log_bucket_in_the_wrong_place_is_a_finding
t test_a_host_that_answers_nothing_the_key_service_reads_is_a_finding
t test_a_body_that_only_mentions_a_missing_entity_is_a_finding
t test_a_body_saying_no_such_database_is_no_database
t test_the_run_launches_from_the_checkout
t test_a_launch_that_refuses_is_a_finding
t test_a_runs_read_that_fails_is_a_finding
t test_a_run_the_bucket_does_not_hold_is_a_finding
t test_a_budget_read_that_fails_still_passes_the_run
t test_a_tool_that_is_not_on_path_stops_the_check
t test_the_fakes_refuse_what_the_real_commands_do_not_have

echo
if [ "$failures" -eq 0 ]; then
  echo "check-820_test.sh: ok"
else
  echo "check-820_test.sh: $failures failure(s)" >&2
  exit 1
fi
