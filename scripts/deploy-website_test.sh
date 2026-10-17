#!/usr/bin/env bash
# Tests for scripts/deploy-website.sh, with node, npm and firebase stubbed on PATH.
# Nothing here builds the real site or reaches the network: the script is copied
# into a temporary repository layout whose stub `npm run check` writes a dist/.
#
# Usage: bash scripts/deploy-website_test.sh   (also run by scripts/deploy_website_test.go)
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}

# A fresh fake repository: the script, a firebase.json, a website/ with node_modules.
setup() {
  rm -rf "${work:?}/repo" "${work:?}/bin" "${work:?}/calls"
  mkdir -p "$work/repo/scripts" "$work/repo/website/node_modules" "$work/bin"
  cp "$here/deploy-website.sh" "$work/repo/scripts/"
  echo '{"hosting":{"public":"website/dist"}}' >"$work/repo/firebase.json"
  : >"$work/calls"
  for tool in node firebase; do
    printf '#!/usr/bin/env bash\necho "%s $*" >>"%s"\n' "$tool" "$work/calls" >"$work/bin/$tool"
  done
  # The stub npm records its arguments, and `npm run check` writes the build output.
  cat >"$work/bin/npm" <<EOF
#!/usr/bin/env bash
echo "npm \$*" >>"$work/calls"
if [[ "\$*" == "run check" && -z "\${STUB_EMPTY_DIST:-}" ]]; then
  mkdir -p dist && echo '<!doctype html>' >dist/index.html
fi
EOF
  chmod +x "$work/bin/"*
}

# run ARGS...: runs the script with the stubs first on PATH; sets $status and $out.
run() {
  set +e
  out=$(PATH="$work/bin:/usr/bin:/bin" bash "$work/repo/scripts/deploy-website.sh" "$@" 2>&1)
  status=$?
  set -e
}

called() { grep -qF -- "$1" "$work/calls"; }

# 1. --dry-run with no --project: default project, checks run, nothing deployed.
setup
run --dry-run
[[ $status -eq 0 ]] || fail "dry run exited $status: $out"
[[ $out == *"project fugaro-prod"* ]] || fail "dry run does not print the default project id: $out"
[[ $out == *"would run: firebase deploy --only hosting --project fugaro-prod"* ]] || fail "dry run does not print the deploy command: $out"
called "npm run check" || fail "dry run did not run npm run check"
! called "firebase" || fail "dry run called firebase: $(cat "$work/calls")"

# 2. --dry-run with an explicit --project and a preview channel.
setup
run --dry-run --project my-site-1 --channel pr-12
[[ $status -eq 0 ]] || fail "dry run with channel exited $status: $out"
[[ $out == *"would run: firebase hosting:channel:deploy pr-12 --project my-site-1"* ]] || fail "channel dry run prints the wrong command: $out"
! called "firebase" || fail "channel dry run called firebase"

# 3. A real deploy (stubbed) runs exactly the live deploy command.
setup
run --project my-site-1
[[ $status -eq 0 ]] || fail "deploy exited $status: $out"
called "firebase deploy --only hosting --project my-site-1" || fail "deploy did not call firebase deploy: $(cat "$work/calls")"

# 4. --project without a value, and an invalid id, are refused before anything runs.
for args in "--project" "--project --dry-run" "--project Bad_Id --dry-run"; do
  setup
  # shellcheck disable=SC2086 # word splitting is the point
  run $args
  [[ $status -ne 0 ]] || fail "'$args' was accepted"
  [[ ! -s $work/calls ]] || fail "'$args' ran tools before refusing: $(cat "$work/calls")"
done

# 5. An unknown argument is refused.
setup
run --dry-run --token abc
[[ $status -ne 0 ]] || fail "an unknown argument was accepted"

# 6. A missing firebase CLI is refused.
setup
rm "$work/bin/firebase"
run --dry-run
[[ $status -ne 0 && $out == *"firebase is not installed"* ]] || fail "a missing firebase CLI was not reported: $out"

# 7. An empty dist/ stops the deploy.
setup
STUB_EMPTY_DIST=1 run --project my-site-1
[[ $status -ne 0 ]] || fail "a missing dist/ was deployed"
! called "firebase" || fail "firebase was called without a build"

# 8. The script never names a token or a key.
if grep -nEi 'token|FIREBASE_TOKEN|GOOGLE_APPLICATION_CREDENTIALS|--key' "$here/deploy-website.sh" | grep -vE '^[0-9]+:#'; then
  fail "deploy-website.sh names a credential outside its comments"
fi

if [[ $failures -gt 0 ]]; then
  echo "$failures failure(s)" >&2
  exit 1
fi
echo "deploy-website_test: ok"
