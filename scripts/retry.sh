# Sourced by scripts/release.sh and scripts/release-gate.sh: a bounded retry
# for the calls a dropped connection to GitHub kills (a TLS handshake timeout,
# an unexpected EOF, a reset connection), which used to end a release half-way
# through a 45-minute wait. Only read-only calls go through retry_read. A call
# that changes something (a tag push, an auto-merge request) is never repeated
# blindly: its caller reads the state again first and decides
# (retry_is_transient tells it whether the failure is worth that).
#
# RELEASE_RETRY_TRIES (default 3) bounds the tries; the wait before try n is
# RELEASE_RETRY_SECONDS (default 5) times n-1. The tests set the wait to 0.
retry_tries=${RELEASE_RETRY_TRIES:-3}
retry_seconds=${RELEASE_RETRY_SECONDS:-5}

# The failures of gh and git (over https) that a dropped or flaky connection
# produces, and that a later try can clear. A refusal, a 404 or a missing ref
# is none of them, and is final on the first try.
retry_transient_errors='TLS handshake timeout|unexpected EOF|early EOF|i/o timeout|connection reset|connection refused|Recv failure|Could not resolve host|error connecting to|Failed to connect|Operation timed out|Connection timed out|remote end hung up unexpectedly|gnutls_handshake|SSL_ERROR|HTTP 50[234]|Bad Gateway|Service Unavailable|Gateway Time-?out'

# retry_is_transient TEXT succeeds when TEXT reads like a dropped connection.
retry_is_transient() {
  printf '%s' "$1" | grep -qiE "$retry_transient_errors"
}

# retry_read [--any] CMD... runs a read-only command up to retry_tries times,
# while it fails with a transient error (with --any, while it fails at all: for
# `gh auth status`, which reports a dropped API as "not logged in"). Only the
# last try's stdout reaches the caller, so a command substitution never holds a
# failed try's partial answer; the last try's stderr and exit status are
# passed on.
retry_read() {
  local any="" try=1 err out rc
  if [ "${1:-}" = --any ]; then
    any=1
    shift
  fi
  while :; do
    err=$(mktemp)
    rc=0
    out=$("$@" 2>"$err") || rc=$?
    if [ "$rc" = 0 ] || [ "$try" -ge "$retry_tries" ] || { [ -z "$any" ] && ! grep -qiE "$retry_transient_errors" "$err"; }; then
      cat "$err" >&2
      rm -f "$err"
      [ -z "$out" ] || printf '%s\n' "$out"
      return "$rc"
    fi
    echo "'$1 ${2:-}' failed (try $try of $retry_tries): $(head -1 "$err"); retrying in $((retry_seconds * try))s" >&2
    rm -f "$err"
    sleep $((retry_seconds * try))
    try=$((try + 1))
  done
}
