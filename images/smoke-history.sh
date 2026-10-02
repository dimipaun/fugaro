#!/bin/sh
# smoke-history.sh IMAGE checks a built history image with plain docker: the
# fugaro binary runs; the image runs as a non-root numeric user, has no shell,
# and carries no credentials; --rollover refuses naming M9d; --sweep without
# its environment refuses before any network call.
set -eu
image=${1:?usage: smoke-history.sh IMAGE}
fail() { echo "smoke-history: $*" >&2; exit 1; }
expect_refusal() { # expect_refusal WANT ARG...: exit 1 and WANT in the output
  want=$1
  shift
  set +e
  out=$(docker run --rm "$image" "$@" 2>&1)
  code=$?
  set -e
  [ "$code" -eq 1 ] || fail "$* exited $code, not 1: $out"
  case "$out" in
    *"$want"*) ;;
    *) fail "$* did not mention $want: $out" ;;
  esac
}

echo "fugaro $(docker run --rm "$image" version)" || fail "fugaro version failed"
user=$(docker inspect --format '{{.Config.User}}' "$image")
case "$user" in
  "" | 0 | 0:* | root | root:*) fail "the image runs as '$user', not a non-root user" ;;
esac
if docker run --rm --entrypoint sh "$image" -c true >/dev/null 2>&1; then
  fail "the image has a shell"
fi
env_dump=$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$image")
case "$env_dump" in
  *KEY* | *TOKEN* | *SECRET* | *PASSWORD* | *CREDENTIAL*) fail "the image environment carries something credential-shaped: $env_dump" ;;
esac
expect_refusal M9d budget history --rollover
expect_refusal FUGARO_PROJECT budget history --sweep
echo "smoke-history: $image ok"
