#!/bin/sh
# smoke.sh IMAGE BASE checks a built base image with plain docker (design
# §13): fugaro, Claude Code, gh and the toolchain run, and the image runs as
# the non-root fugaro user in /work/repo under tini, with no passwordless
# sudo, no leftover credential files, and no file under $HOME owned by
# anyone but fugaro. The image's Claude Code and gh are checked against the
# pins CLAUDE_CODE_VERSION and GH_VERSION, which default to the ARG lines in
# images/BASE/Dockerfile, so CI and release builds always assert the pins;
# set one in the environment to override it.
# For BASE=base, the pins additionally include MISE_VERSION, GCLOUD_VERSION,
# DOCKER_CLI_VERSION, YQ_VERSION, CODEX_VERSION, OPENCODE_VERSION,
# GOOSE_VERSION, CRUSH_VERSION, HARNESS_NODE_VERSION, UV_VERSION and
# GRAPHIFY_VERSION, each matched exactly
# (not as a substring) against a version token parsed from the tool's own
# output; every field of /etc/fugaro/base.json is checked against its own
# pin too. The image's presence checks (images/base/tools.tsv) and its mise
# cache directory contract (~/.cache/mise, fugaro's) are run with no network
# through `fugaro image selftest`.
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

case "$base" in
  base)
    # resolve_pin NAME is the NAME env var if set, else the Dockerfile's own
    # ARG NAME=... default; it fails if neither exists.
    resolve_pin() {
      v=$(eval "printf '%s' \"\${$1:-}\"")
      [ -n "$v" ] || v=$(arg_default "$1")
      [ -n "$v" ] || fail "$dockerfile has no ARG $1=... pin"
      printf '%s' "$v"
    }
    # pinned NAME SED CMD... runs CMD, extracts a clean version token from
    # its first line with the sed expression SED, and requires it to equal
    # the Dockerfile's pin NAME exactly: a substring match would let a pin
    # of 1.2.3 accept a reported 11.2.30 or 1.2.34.
    pinned() {
      name=$1; extract=$2; shift 2
      want=$(resolve_pin "$name")
      raw=$(first_line "$(run "$@" 2>&1)") || fail "$* failed"
      got=$(printf '%s' "$raw" | sed -e "$extract")
      echo "$raw"
      [ "$got" = "$want" ] || fail "$* reports '$raw' (parsed '$got'), not pinned $name=$want"
    }
    pinned MISE_VERSION 's/^\([^ ]*\).*/\1/' mise --version
    pinned GCLOUD_VERSION 's/^Google Cloud SDK //' gcloud --version
    pinned DOCKER_CLI_VERSION 's/^Docker version \([^,]*\),.*/\1/' docker --version
    pinned YQ_VERSION 's/.*version v//' yq --version
    pinned CODEX_VERSION 's/^codex-cli //' codex --version
    pinned OPENCODE_VERSION 's/^ *//' opencode --version
    pinned GOOSE_VERSION 's/^ *//' goose --version
    pinned CRUSH_VERSION 's/.*version v//' crush --version
    pinned HARNESS_NODE_VERSION 's/^v//' /opt/fugaro/node/bin/node -v
    pinned UV_VERSION 's/^uv \([0-9.]*\).*/\1/' /opt/fugaro/uv/bin/uv --version
    pinned GRAPHIFY_VERSION 's/^graphify //' graphify --version

    # /etc/fugaro/base.json (design base-image.md section 3) records the
    # base's own pins; every one of its fields must match the Dockerfile's
    # ARG, not just mise. It is a flat object with no escaped quote, so a
    # plain sed extraction is enough: no jq or yq needed on the smoke host.
    base_json=$(run cat /etc/fugaro/base.json) || fail "cat /etc/fugaro/base.json failed"
    echo "$base_json"
    check_json_field() {
      got=$(printf '%s' "$base_json" | sed -n 's/.*"'"$1"'":"\([^"]*\)".*/\1/p')
      [ "$got" = "$2" ] || fail "/etc/fugaro/base.json's \"$1\" is '$got', not pinned $2"
    }
    check_json_field debian "trixie-slim@$(resolve_pin DEBIAN_DIGEST)"
    check_json_field mise "$(resolve_pin MISE_VERSION)"
    check_json_field claude_code "$(resolve_pin CLAUDE_CODE_VERSION)"
    check_json_field gh "$(resolve_pin GH_VERSION)"
    check_json_field yq "$(resolve_pin YQ_VERSION)"
    check_json_field gcloud "$(resolve_pin GCLOUD_VERSION)"
    check_json_field docker_cli "$(resolve_pin DOCKER_CLI_VERSION)"
    check_json_field harness_node "$(resolve_pin HARNESS_NODE_VERSION)"
    check_json_field codex "$(resolve_pin CODEX_VERSION)"
    check_json_field opencode "$(resolve_pin OPENCODE_VERSION)"
    check_json_field goose "$(resolve_pin GOOSE_VERSION)"
    check_json_field crush "$(resolve_pin CRUSH_VERSION)"
    check_json_field uv "$(resolve_pin UV_VERSION)"
    check_json_field graphify "$(resolve_pin GRAPHIFY_VERSION)"
    echo "base.json matches every pin"

    # Every row of images/base/tools.tsv, presence only: no network, and the
    # selftest gives each tool an empty HOME. check_mise_cache adds the
    # runtime check that ~/.cache/mise is fugaro's directory, which a derived
    # build's cache mount needs to land on.
    report=$(printf '%s' '{"tools":"all","tools_only":true,"check_mise_cache":true}' | docker run --rm -i --network none "$image" fugaro image selftest) \
      || { printf '%s\n' "$report"; fail "the presence checks failed (report above)"; }
    case "$report" in *'"passed":true'*) ;; *) printf '%s\n' "$report"; fail "the presence checks did not pass" ;; esac
    echo "presence checks passed"
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
