#!/bin/sh
# Fixture test suite: tests alpha, beta, gamma in class pkg.Suite.
# Tests named in the file $FIXTURE_FAILS_FILE fail. Arguments, if any, are test
# IDs (pkg.Suite.<name>) to run instead of the whole suite.
fails=""
if [ -n "$FIXTURE_FAILS_FILE" ] && [ -f "$FIXTURE_FAILS_FILE" ]; then
  fails=$(cat "$FIXTURE_FAILS_FILE")
fi
tests="alpha beta gamma"
if [ $# -gt 0 ]; then
  tests=""
  for id in "$@"; do tests="$tests ${id##*.}"; done
fi
mkdir -p build/test-results
out=build/test-results/TEST-suite.xml
status=0
echo '<testsuite name="suite">' > "$out"
for t in $tests; do
  case " $fails " in
    *" $t "*) echo "<testcase classname=\"pkg.Suite\" name=\"$t\"><failure message=\"boom\"/></testcase>" >> "$out"; status=1 ;;
    *) echo "<testcase classname=\"pkg.Suite\" name=\"$t\"/>" >> "$out" ;;
  esac
done
echo '</testsuite>' >> "$out"
exit $status
