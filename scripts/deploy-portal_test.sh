#!/usr/bin/env bash
# Tests for scripts/deploy-portal.sh, with node, npm, gcloud, firebase, git and
# go stubbed on PATH. Nothing here builds the real SPA, builds an image, deploys
# anything or reaches the network: the script is copied into a temporary
# repository layout whose stub `npm run build` writes a dist/.
#
# Usage: bash scripts/deploy-portal_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
root=$(cd "$here/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}

# The four values a build has to have, and the only ones the script insists on:
# a bundle without them is a portal nobody can sign into.
readonly BUNDLE_ENV=(
  VITE_FIREBASE_API_KEY=test-api-key
  VITE_FIREBASE_AUTH_DOMAIN=app.example.test
  VITE_FIREBASE_PROJECT_ID=example-project
  VITE_FIREBASE_APP_ID=1:2:web:3
)

# The names the script refuses a session by. This suite runs from an agent's own
# session, so every case below clears them and case 6 sets one on purpose.
readonly AGENT_MARKERS=(CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT)
readonly CLEAR_MARKERS=(-u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE -u CURSOR_AGENT -u AI_AGENT)

# A fresh fake repository: the script, the portal's directory with the files the
# build reads, and one stub per tool the script calls.
setup() {
  rm -rf "${work:?}/repo" "${work:?}/bin" "${work:?}/calls"
  mkdir -p "$work/repo/scripts" "$work/repo/ee/portal" "$work/repo/ee/keyservice" "$work/bin"
  cp "$here/deploy-portal.sh" "$work/repo/scripts/"
  echo '{"hosting":{"site":"fugaro-portal","public":"dist"}}' >"$work/repo/ee/portal/firebase.json"
  echo '{"name":"fugaro-portal","scripts":{"build":"vite build"}}' >"$work/repo/ee/portal/package.json"
  : >"$work/repo/ee/keyservice/cloudbuild.yaml"
  : >"$work/calls"
  for tool in gcloud firebase node; do
    printf '#!/usr/bin/env bash\necho "%s $*" >>"%s"\n' "$tool" "$work/calls" >"$work/bin/$tool"
  done
  printf '#!/usr/bin/env bash\necho "git $*" >>"%s"\necho abc1234\n' "$work/calls" >"$work/bin/git"
  printf '#!/usr/bin/env bash\necho "go $*" >>"%s"\necho "go-env GOOGLE_CLOUD_PROJECT=${GOOGLE_CLOUD_PROJECT:-}" >>"%s"\n' \
    "$work/calls" "$work/calls" >"$work/bin/go"
  # The stub npm records its arguments, and `npm run build` writes the bundle.
  cat >"$work/bin/npm" <<EOF
#!/usr/bin/env bash
echo "npm \$*" >>"$work/calls"
if [[ "\$*" == "run build" && -z "\${STUB_EMPTY_DIST:-}" ]]; then
  mkdir -p dist && echo '<!doctype html>' >dist/index.html
fi
EOF
  chmod +x "$work/bin/"*
}

# run ARGS...: runs the script with the stubs first on PATH and the bundle's
# configuration in the environment; sets $status and $out.
run() {
  set +e
  out=$(env "${CLEAR_MARKERS[@]}" "${BUNDLE_ENV[@]}" PATH="$work/bin:/usr/bin:/bin" \
    bash "$work/repo/scripts/deploy-portal.sh" "$@" 2>&1)
  status=$?
  set -e
}

called() { grep -qF -- "$1" "$work/calls"; }

# 1. --dry-run with no arguments: the defaults, the SPA built, nothing deployed.
setup
run --dry-run
[[ $status -eq 0 ]] || fail "dry run exited $status: $out"
[[ $out == *"project fugaro-prod"* ]] || fail "dry run does not print the default project id: $out"
[[ $out == *"region us-east5"* ]] || fail "dry run does not print the default region: $out"
called "npm run build" || fail "dry run did not build the SPA"
called "gcloud" && fail "dry run called gcloud: $(cat "$work/calls")"
called "firebase" && fail "dry run called firebase: $(cat "$work/calls")"
called "go " && fail "dry run called go: $(cat "$work/calls")"

# 2. The dry run prints every command it would run, the hosting one included and
#    with the portal's own configuration.
setup
run --dry-run --project my-portal-1 --region europe-west1
[[ $status -eq 0 ]] || fail "dry run exited $status: $out"
[[ $out == *"would run: gcloud builds submit"* ]] || fail "dry run does not print the Cloud Build: $out"
[[ $out == *"would run: gcloud run deploy fugaro-portal-api"* ]] || fail "dry run does not print the API's deploy: $out"
[[ $out == *"would run: gcloud run deploy fugaro-key-minter"* ]] || fail "dry run does not print the minter's deploy: $out"
[[ $out == *"would run: firebase deploy --only hosting,firestore:rules --project my-portal-1 --config ee/portal/firebase.json"* ]] ||
  fail "dry run does not print the hosting and rules deploy with the portal's config: $out"

# 3. A real run (stubbed) runs exactly those commands, in the deploy's order: the
#    build, then the two services, then the hosting site and its rules.
setup
run --project my-portal-1
[[ $status -eq 0 ]] || fail "deploy exited $status: $out"
for want in \
  "gcloud builds submit --config ee/keyservice/cloudbuild.yaml --project my-portal-1 --region us-east5 ." \
  "gcloud run deploy fugaro-portal-api --image us-east5-docker.pkg.dev/my-portal-1/fugaro-portal/fugaro-portal-api:abc1234 --project my-portal-1 --region us-east5" \
  "gcloud run deploy fugaro-key-minter --image us-east5-docker.pkg.dev/my-portal-1/fugaro-portal/fugaro-key-minter:abc1234 --project my-portal-1 --region us-east5" \
  "firebase deploy --only hosting,firestore:rules --project my-portal-1 --config ee/portal/firebase.json"; do
  called "$want" || fail "the deploy did not call: $want ($(cat "$work/calls"))"
done
[[ $(grep -n "firebase deploy" "$work/calls" | cut -d: -f1) -gt $(grep -n "gcloud run deploy fugaro-portal-api" "$work/calls" | cut -d: -f1) ]] ||
  fail "the hosting site was deployed before the API it rewrites to: $(cat "$work/calls")"

# 4. --seed-operations runs the API program's one-time subcommand, after the
#    deploy, against the project the deploy named.
setup
run --project my-portal-1 --seed-operations owner@example.test
[[ $status -eq 0 ]] || fail "the seeded run exited $status: $out"
called "go run ./ee/keyservice/cmd/portal-api seed-operations owner@example.test" ||
  fail "the seed did not run: $(cat "$work/calls")"
called "go-env GOOGLE_CLOUD_PROJECT=my-portal-1" || fail "the seed did not name the project: $(cat "$work/calls")"
[[ $(grep -n "seed-operations" "$work/calls" | cut -d: -f1) -gt $(grep -n "firebase deploy" "$work/calls" | cut -d: -f1) ]] ||
  fail "the seed ran before the deploy: $(cat "$work/calls")"

setup
run --dry-run --project my-portal-1 --seed-operations owner@example.test
[[ $status -eq 0 ]] || fail "the seeded dry run exited $status: $out"
[[ $out == *"would run: GOOGLE_CLOUD_PROJECT=my-portal-1 go run ./ee/keyservice/cmd/portal-api seed-operations owner@example.test"* ]] ||
  fail "the dry run does not print the seed: $out"
called "go " && fail "the dry run ran the seed: $(cat "$work/calls")"

# 5. A build with no Firebase configuration is refused before anything runs: it
#    would deploy a portal nobody can sign into.
for missing in "${BUNDLE_ENV[@]%%=*}"; do
  setup
  env_args=()
  for value in "${BUNDLE_ENV[@]}"; do
    [[ ${value%%=*} == "$missing" ]] || env_args+=("$value")
  done
  set +e
  out=$(env "${CLEAR_MARKERS[@]}" "${env_args[@]}" PATH="$work/bin:/usr/bin:/bin" \
    bash "$work/repo/scripts/deploy-portal.sh" --project my-portal-1 2>&1)
  status=$?
  set -e
  [[ $status -ne 0 ]] || fail "a run without $missing was accepted"
  [[ $out == *"$missing"* ]] || fail "the refusal does not name $missing: $out"
  [[ ! -s $work/calls ]] || fail "a run without $missing ran tools first: $(cat "$work/calls")"
done

# 6. An agent's session is refused: this script deploys the service that signs
#    licence keys, and no agent deploys or operates it.
for marker in "${AGENT_MARKERS[@]}"; do
  setup
  # env wants every option before every assignment, so the unsets and the one
  # marker under test are built in that order.
  marker_opts=()
  marker_assigns=()
  for name in "${AGENT_MARKERS[@]}"; do
    if [[ $name == "$marker" ]]; then marker_assigns+=("$name=1"); else marker_opts+=(-u "$name"); fi
  done
  set +e
  out=$(env "${marker_opts[@]}" "${marker_assigns[@]}" "${BUNDLE_ENV[@]}" PATH="$work/bin:/usr/bin:/bin" \
    bash "$work/repo/scripts/deploy-portal.sh" --project my-portal-1 2>&1)
  status=$?
  set -e
  [[ $status -ne 0 ]] || fail "a session with $marker set was accepted"
  [[ $out == *"$marker"* ]] || fail "the refusal does not name $marker: $out"
  [[ ! -s $work/calls ]] || fail "a session with $marker set ran tools first: $(cat "$work/calls")"
done

# 7. Bad arguments are refused before anything runs.
for args in \
  "--project" \
  "--project --dry-run" \
  "--project Bad_Id --dry-run" \
  "--region" \
  "--region nowhere --dry-run" \
  "--config" \
  "--config /nonexistent/firebase.json" \
  "--seed-operations" \
  "--dry-run --token abc"; do
  setup
  # shellcheck disable=SC2086 # word splitting is the point
  run $args
  [[ $status -ne 0 ]] || fail "'$args' was accepted"
  [[ ! -s $work/calls ]] || fail "'$args' ran tools before refusing: $(cat "$work/calls")"
done

# 8. A missing tool is refused before anything runs.
for tool in gcloud firebase; do
  setup
  rm "$work/bin/$tool"
  run --dry-run
  [[ $status -ne 0 && $out == *"$tool is not installed"* ]] || fail "a missing $tool was not reported: $out"
done

# 9. A build that produced nothing stops the deploy.
setup
STUB_EMPTY_DIST=1 run --project my-portal-1
[[ $status -ne 0 ]] || fail "a missing dist/ was deployed"
called "gcloud" && fail "gcloud was called without a build: $(cat "$work/calls")"

# 10. The services and the region are the infrastructure's, not the script's: the
#     two names are ee/keyservice/terraform/services' own defaults, and the region
#     is the one ee/portal/firebase.json's /api/** rewrite names, so a deploy
#     cannot put a service where the hosting configuration is not looking.
tf=$root/ee/keyservice/terraform/services/variables.tf
for pair in "api_service fugaro-portal-api" "minter_service fugaro-key-minter"; do
  # shellcheck disable=SC2086 # word splitting is the point
  set -- $pair
  want=$(grep -A 3 "variable \"$1\"" "$tf" | sed -n 's/.*default *= *"\(.*\)".*/\1/p' | head -1)
  [[ -n $want ]] || fail "$tf no longer gives $1 a default: this check reads it to keep the deploy and the infrastructure together"
  grep -q "run deploy $want" "$root/scripts/deploy-portal.sh" ||
    fail "scripts/deploy-portal.sh does not deploy $want, which $tf names as $1's default"
done
rewrite_region=$(jq -r '.hosting.rewrites[] | select(.run) | .run.region' "$root/ee/portal/firebase.json" 2>/dev/null || true)
script_region=$(sed -n 's/^DEFAULT_REGION=\(.*\)$/\1/p' "$root/scripts/deploy-portal.sh")
[[ -n $rewrite_region && $script_region == "$rewrite_region" ]] ||
  fail "scripts/deploy-portal.sh's default region ($script_region) is not the region ee/portal/firebase.json's /api/** rewrite names ($rewrite_region): a deploy in one region and a rewrite in another is an API the SPA cannot reach"

# 11. The script never names a token or a key.
if grep -nEi 'token|GOOGLE_APPLICATION_CREDENTIALS|--key|credentials_json' "$here/deploy-portal.sh" | grep -vE '^[0-9]+:#'; then
  fail "deploy-portal.sh names a credential outside its comments"
fi

if [[ $failures -gt 0 ]]; then
  echo "$failures failure(s)" >&2
  exit 1
fi
echo "deploy-portal_test: ok"
