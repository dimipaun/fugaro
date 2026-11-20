#!/bin/sh
# A fake `go` for scripts/release_rehearsal_test.go. The one call
# scripts/release-rehearsal.sh makes is `go build -o <path> ./cmd/fugaro` from
# the checkout, and this answers it by copying the fake fugaro to <path> and
# making it executable: the ladder from "build the CLI of this commit" to "run
# it" is then exercised without a module (the fixture checkout holds none) and
# without compiling anything.
#
#   FAKE_GO_DIR     state directory (required): log, with each call's cwd and argv
#   FAKE_GO_FUGARO  the file copied to the -o path (required)
set -eu
dir=$FAKE_GO_DIR
{
  printf 'cwd %s\n' "$PWD"
  printf 'argv %s\n' "$*"
} >>"$dir/log"
out=""
prev=""
for arg in "$@"; do
  if [ "$prev" = -o ]; then out=$arg; fi
  prev=$arg
done
if [ -z "$out" ]; then
  echo "fake-go: invoked as '$*' with no -o: the rehearsal builds the CLI to a path of its own, and a call this fake cannot answer is one the rehearsal should not make" >&2
  exit 64
fi
cp "$FAKE_GO_FUGARO" "$out"
chmod +x "$out"
