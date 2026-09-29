#!/usr/bin/env bash
# Captures the M4 job spec of the sandbox fixture. Run from the repository root, before the internal/infra refactor changes any code; rerunning it overwrites.
set -euo pipefail
out=internal/infra/testdata
bin=$(mktemp -d)/fugaro-m4
go build -o "$bin" ./cmd/fugaro
work=$(mktemp -d)
trap 'rm -rf "$work" "$(dirname "$bin")"' EXIT
cp -R deploy/bootstrap/sandbox "$work/sandbox"
git -C "$work/sandbox" init -q -b master
git -C "$work/sandbox" remote add origin https://bitbucket.org/acme/sandbox.git
cat > "$work/config.yaml" <<'YAML'
version: 1
project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry: us-east5-docker.pkg.dev/proj-1234/fugaro
build: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }
user: test@example.com
repos:
  acme/sandbox: { provider: bitbucket, base_branch: master, workflows: [web] }
YAML
cd "$work/sandbox"
export FUGARO_CONFIG="$work/config.yaml" HOME="$work" GIT_CONFIG_GLOBAL=/dev/null
"$bin" gcp job-spec --repo acme/sandbox --workflow web --json > "$OLDPWD/$out/m4-jobspec-sandbox.json"
for f in bucket-condition sa-display-name secret-names build-secret-ids labels env secrets; do
  printf '%s\t%s\n' "$f" "$("$bin" gcp job-spec --repo acme/sandbox --workflow web --field "$f" | tr '\n' ';')"
done > "$OLDPWD/$out/m4-jobspec-sandbox.fields.tsv"
