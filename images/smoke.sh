#!/bin/sh
# smoke.sh IMAGE BASE checks a built base image with plain docker (design
# §13): fugaro, Claude Code, gh and the toolchain run, and the image runs as
# the non-root fugaro user in /work/repo under tini, with no passwordless
# sudo, no leftover credential files, and no file under $HOME owned by
# anyone but fugaro. The image's Claude Code and gh, and its toolchain (Node
# for web-node; Go and Terraform for go; the JDK, Node, PostgreSQL and
# firebase-tools for java-services), are checked against the pins
# CLAUDE_CODE_VERSION, GH_VERSION, NODE_VERSION, GO_VERSION,
# TERRAFORM_VERSION, JDK_VERSION, POSTGRES_MAJOR and FIREBASE_TOOLS_VERSION,
# which default to the ARG lines in images/BASE/Dockerfile, so CI and release
# builds always assert the pins; set one in the environment to override it.
# java-services is also run for real: its services start under a read-only
# root filesystem, a Postgres with PostGIS and pgvector, Redis and the Firebase
# emulators answer, and the preflight of an EdgeServer-style repository
# passes, before they are stopped.
set -eu
image=${1:?usage: smoke.sh IMAGE BASE}
base=${2:?usage: smoke.sh IMAGE BASE}
run() { docker run --rm "$image" "$@"; }
fail() { echo "smoke: $*" >&2; exit 1; }
first_line() { printf '%s\n' "$1" | sed -n '1p'; }

dockerfile="$(dirname "$0")/$base/Dockerfile"
[ -f "$dockerfile" ] || fail "there is no $dockerfile to read the pinned versions from"
# arg_default NAME prints the default of the Dockerfile's `ARG NAME=value`.
arg_default() { sed -n "s/^ARG $1=\([^[:space:]]*\)[[:space:]]*\$/\1/p" "$dockerfile" | sed -n '1p'; }
CLAUDE_CODE_VERSION=${CLAUDE_CODE_VERSION:-$(arg_default CLAUDE_CODE_VERSION)}
GH_VERSION=${GH_VERSION:-$(arg_default GH_VERSION)}
[ -n "$CLAUDE_CODE_VERSION" ] || fail "$dockerfile has no ARG CLAUDE_CODE_VERSION=... pin"
[ -n "$GH_VERSION" ] || fail "$dockerfile has no ARG GH_VERSION=... pin"

fugaro_version=$(run fugaro version) || fail "fugaro version failed"
echo "fugaro $fugaro_version"

claude_version=$(run claude --version) || fail "claude --version failed"
echo "claude $claude_version"
case "$claude_version" in
  "$CLAUDE_CODE_VERSION "*) ;;
  *) fail "claude --version reports '$claude_version', not pinned CLAUDE_CODE_VERSION=$CLAUDE_CODE_VERSION" ;;
esac

gh_version_full=$(run gh --version) || fail "gh --version failed"
gh_version=$(first_line "$gh_version_full")
echo "$gh_version"
case "$gh_version" in
  *" $GH_VERSION "*) ;;
  *) fail "gh --version reports '$gh_version', not pinned GH_VERSION=$GH_VERSION" ;;
esac

# check_node asserts node's version is the NODE_VERSION pin.
check_node() {
  node_version=$(run node -v) || fail "node -v failed"
  echo "node $node_version"
  NODE_VERSION=${NODE_VERSION:-$(arg_default NODE_VERSION)}
  [ -n "$NODE_VERSION" ] || fail "$dockerfile has no ARG NODE_VERSION=... pin"
  # A dotted pin (24.19.0) must match exactly; a bare major (24) matches
  # any release in it.
  case "$NODE_VERSION" in
    *.*)
      [ "$node_version" = "v$NODE_VERSION" ] \
        || fail "node -v reports '$node_version', not pinned NODE_VERSION=$NODE_VERSION" ;;
    *)
      case "$node_version" in
        v"$NODE_VERSION".*) ;;
        *) fail "node -v reports '$node_version', not pinned NODE_VERSION major=$NODE_VERSION" ;;
      esac ;;
  esac
}

case "$base" in
  web-node)
    check_node
    corepack_version=$(run corepack --version) || fail "corepack --version failed"
    echo "corepack $corepack_version"
    ;;
  go)
    GO_VERSION=${GO_VERSION:-$(arg_default GO_VERSION)}
    TERRAFORM_VERSION=${TERRAFORM_VERSION:-$(arg_default TERRAFORM_VERSION)}
    [ -n "$GO_VERSION" ] || fail "$dockerfile has no ARG GO_VERSION=... pin"
    [ -n "$TERRAFORM_VERSION" ] || fail "$dockerfile has no ARG TERRAFORM_VERSION=... pin"
    go_version=$(run go version) || fail "go version failed"
    echo "$go_version"
    case "$go_version" in
      "go version go$GO_VERSION "*) ;;
      *) fail "go version reports '$go_version', not pinned GO_VERSION=$GO_VERSION" ;;
    esac
    tf_version=$(first_line "$(run terraform version)") || fail "terraform version failed"
    echo "$tf_version"
    [ "$tf_version" = "Terraform v$TERRAFORM_VERSION" ] \
      || fail "terraform version reports '$tf_version', not pinned TERRAFORM_VERSION=$TERRAFORM_VERSION"
    run make --version >/dev/null || fail "make --version failed"
    run gcc --version >/dev/null || fail "gcc --version failed"
    [ "$(run go env GOTOOLCHAIN)" = local ] || fail "GOTOOLCHAIN is not local"
    ;;
  java-services)
    JDK_VERSION=${JDK_VERSION:-$(arg_default JDK_VERSION)}
    POSTGRES_MAJOR=${POSTGRES_MAJOR:-$(arg_default POSTGRES_MAJOR)}
    FIREBASE_TOOLS_VERSION=${FIREBASE_TOOLS_VERSION:-$(arg_default FIREBASE_TOOLS_VERSION)}
    [ -n "$JDK_VERSION" ] || fail "$dockerfile has no ARG JDK_VERSION=... pin"
    [ -n "$POSTGRES_MAJOR" ] || fail "$dockerfile has no ARG POSTGRES_MAJOR=... pin"
    [ -n "$FIREBASE_TOOLS_VERSION" ] || fail "$dockerfile has no ARG FIREBASE_TOOLS_VERSION=... pin"
    java_version=$(first_line "$(run java -version 2>&1)") || fail "java -version failed"
    echo "$java_version"
    case "$java_version" in
      *"version \"$JDK_VERSION\""*) ;;
      *) fail "java -version reports '$java_version', not pinned JDK_VERSION=$JDK_VERSION" ;;
    esac
    check_node
    # The CLI runs from /tmp: it writes firebase-debug.log into its working
    # directory, which is read-only here and in the services check below.
    firebase_version=$(first_line "$(run sh -c 'cd /tmp && firebase --version')") || fail "firebase --version failed"
    echo "firebase-tools $firebase_version"
    [ "$firebase_version" = "$FIREBASE_TOOLS_VERSION" ] \
      || fail "firebase --version reports '$firebase_version', not pinned FIREBASE_TOOLS_VERSION=$FIREBASE_TOOLS_VERSION"
    psql_version=$(first_line "$(run psql --version)") || fail "psql --version failed"
    echo "$psql_version"
    case "$psql_version" in
      "psql (PostgreSQL) $POSTGRES_MAJOR."*) ;;
      *) fail "psql --version reports '$psql_version', not pinned POSTGRES_MAJOR=$POSTGRES_MAJOR" ;;
    esac
    redis_version=$(first_line "$(run redis-server --version)") || fail "redis-server --version failed"
    echo "$redis_version"
    jars=$(run sh -c 'ls "$FIREBASE_EMULATORS_PATH"') || fail "listing the emulators' jars failed"
    for emulator in firebase-database-emulator cloud-firestore-emulator cloud-storage-rules-runtime pubsub-emulator dataconnect-emulator ui-v; do
      case "$jars" in
        *"$emulator"*) ;;
        *) fail "the image has no $emulator download in FIREBASE_EMULATORS_PATH (found: $(echo $jars))" ;;
      esac
    done
    # The services, run for real the way a Cloud Run job may run them: a
    # read-only root filesystem with only /tmp and a fresh $HOME writable.
    # The checks after `start` are the ones EdgeServer's
    # scripts/check-local-test-env.sh makes (its db.user is postgres with an
    # empty password).
    services_check='
set -eu
fugaro-services start
fugaro-services start >/dev/null
fugaro-services status >/dev/null
export PGPASSWORD=
sql() { psql -h localhost -p 5432 -U postgres -Atq -v ON_ERROR_STOP=1 "$@"; }
[ "$(sql -d postgres -c "select count(*) from pg_available_extensions where name in ('"'"'postgis'"'"', '"'"'vector'"'"')")" = 2 ] \
  || { echo "postgis or vector is not available" >&2; exit 1; }
[ "$(sql -d postgres -c "select rolsuper and rolcreatedb from pg_roles where rolname = current_user")" = t ] \
  || { echo "postgres is not a superuser that may create databases" >&2; exit 1; }
sql -d postgres -c "create database smoke_db"
sql -d smoke_db -c "create extension postgis; create extension vector; select postgis_version(); select '"'"'[1,2,3]'"'"'::vector;"
sql -d postgres -c "drop database smoke_db"
[ "$(redis-cli -h localhost -p 6379 ping)" = PONG ] || { echo "redis does not PONG" >&2; exit 1; }
for port in 9000 9099 9199; do
  (exec 3<>"/dev/tcp/localhost/$port") 2>/dev/null || { echo "the emulator port $port is closed" >&2; exit 1; }
done
curl -fsS http://localhost:9099/ >/dev/null
fugaro-services stop
if (exec 3<>/dev/tcp/localhost/5432) 2>/dev/null; then echo "postgres still listens after stop" >&2; exit 1; fi
if (exec 3<>/dev/tcp/localhost/9000) 2>/dev/null; then echo "the database emulator still listens after stop" >&2; exit 1; fi
echo "services ok"
'
    docker run --rm --read-only --tmpfs /tmp --tmpfs /home/fugaro:uid=1000,gid=1000,mode=0755 "$image" bash -c "$services_check" \
      || fail "fugaro-services failed (output above)"
    ;;
  *) fail "unknown base $base" ;;
esac

user=$(run id -un) || fail "id -un failed"
[ "$user" = fugaro ] || fail "the image does not run as fugaro"

wd=$(run pwd) || fail "pwd failed"
[ "$wd" = /work/repo ] || fail "the working directory is not /work/repo"

pid1=$(run sh -c 'tr "\000" " " </proc/1/cmdline') || fail "reading PID 1's cmdline failed"
case "$pid1" in
  "/usr/bin/tini "*) ;;
  *) fail "PID 1 is not tini (got '$pid1')" ;;
esac

if run sudo -n true 2>/dev/null; then fail "fugaro has passwordless sudo"; fi

creds_mode=$(run stat -c '%a' /work/creds) || fail "stat /work/creds failed"
[ "$creds_mode" = 700 ] || fail "/work/creds is not mode 700 (got $creds_mode)"

foreign=$(run sh -c 'find "$HOME" ! -user fugaro 2>/dev/null') || fail "checking \$HOME ownership failed"
[ -z "$foreign" ] || fail "files under \$HOME are not owned by fugaro: $foreign"

if run sh -c 'test -e "$HOME/.git-credentials"' 2>/dev/null; then
  fail "\$HOME/.git-credentials exists in the image"
fi
if run sh -c 'test -e "$HOME/.claude.json"' 2>/dev/null; then
  fail "\$HOME/.claude.json exists in the image"
fi

echo "smoke: $image ok"
