#!/bin/bash
# Mutation harness: plant one bug, run one test, revert. Not part of the
# package; deleted before the commit that ends the task.
set -u
cd /work/repo
run() {
  local name="$1" file="$2" from="$3" to="$4" test="$5"
  python3 - "$file" "$from" "$to" <<'PY'
import sys
path, frm, to = sys.argv[1], sys.argv[2], sys.argv[3]
src = open(path).read()
if frm not in src:
    sys.exit("PATTERN NOT FOUND: " + frm)
open(path, "w").write(src.replace(frm, to, 1))
PY
  if [ $? -ne 0 ]; then echo "SKIP  $name (pattern not found)"; git checkout -- .; return; fi
  out=$(GOFLAGS=-p=2 go test ./internal/credsvc/... -count=1 -run "$test" 2>&1)
  if echo "$out" | grep -q "^ok\|^PASS"; then
    echo "NOT-PROVEN  $name: $test still passed"
  else
    echo "proven      $name -> $test failed"
  fi
  git checkout -- .
}
