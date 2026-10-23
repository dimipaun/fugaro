#!/bin/sh
# smoke-credentials.sh IMAGE checks a built credentials image with plain
# docker: the fugaro binary runs; the image runs as a non-root numeric user,
# has no shell, and carries no credentials; and `credentials serve` -- the
# entrypoint Cloud Run runs, since the service's container sets no command and
# no args -- refuses by name when the installation module's environment is not
# there.
#
# What this script deliberately does NOT assert: that a fully configured
# `credentials serve` refuses to start. That refusal is a fact about the
# deployment rather than about this image -- the installation module sets three
# names on the service and the configuration the service needs four more facts
# it does not carry (internal/cli/credentials_serve.go's config builder) -- so a
# smoke test that pinned its text would have to be rewritten the day the
# container carries them. The refusal it does assert (a missing FUGARO_PROJECT,
# named, exit 1, before anything else) is this task's own contract and stays
# true either way.
set -eu
image=${1:?usage: smoke-credentials.sh IMAGE}
fail() { echo "smoke-credentials: $*" >&2; exit 1; }
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
# The service's own environment is the three names the installation module
# sets on it, and the image must carry none of them: a baked-in FUGARO_PROJECT
# would be an installation this image cannot serve. Nothing credential-shaped
# may be baked in either -- the bundle secret is read at run time, as the
# service's own account, and never copied into a layer.
env_dump=$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$image")
case "$env_dump" in
  *FUGARO_PROJECT* | *FUGARO_GCP_PROJECT* | *FUGARO_REGION*)
    fail "the image bakes in the service's environment: $env_dump" ;;
  *KEY* | *TOKEN* | *SECRET* | *PASSWORD* | *CREDENTIAL*)
    fail "the image environment carries something credential-shaped: $env_dump" ;;
esac
# With no environment at all, the first name the module sets is the one named.
expect_refusal FUGARO_PROJECT credentials serve
# And the name it reports is the one that is missing, not the first of the
# three: two set and one empty refuses naming the empty one.
set +e
out=$(docker run --rm -e FUGARO_PROJECT=fugaro -e FUGARO_GCP_PROJECT=fugaro-dev "$image" credentials serve 2>&1)
code=$?
set -e
[ "$code" -eq 1 ] || fail "credentials serve with one name missing exited $code, not 1: $out"
case "$out" in
  *FUGARO_REGION*) ;;
  *) fail "credentials serve did not name the missing FUGARO_REGION: $out" ;;
esac
echo "smoke-credentials: $image ok"
