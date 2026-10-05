#!/bin/bash
# Regenerates internal/infra/tf/testdata/golden/*.plan.json: real
# `terraform show -json` plans of the three roots (fresh creates), cut down to
# their resource_changes. Needs terraform, jq and the google provider already
# in a plugin cache directory (the one fugaro keeps: ~/.cache/fugaro/terraform-plugins).
# It plans with a fake access token and an unreachable HTTPS proxy, so no
# network call can leave the machine: a fresh create plan reads nothing from
# Google. The inputs are the golden tfvars the Go and Terraform tests already use.
# Usage: scripts/gen-golden-plans.sh [plugin-cache-dir]
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
cache=${1:-$HOME/.cache/fugaro/terraform-plugins}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cp -r "$repo/deploy/terraform/gcp" "$tmp/gcp"
cat > "$tmp/rc" <<RC
provider_installation {
  filesystem_mirror { path = "$cache" }
}
RC
cp "$repo/internal/infra/testdata/installation-budget-job.tfvars.json" "$tmp/gcp/roots/installation/terraform.tfvars.json"
cp "$repo/internal/infra/testdata/firebase.tfvars.json" "$tmp/gcp/roots/firebase/terraform.tfvars.json"
cp "$repo/deploy/terraform/gcp/roots/repo/tests/testdata/github-vertex.tfvars.json" "$tmp/gcp/roots/repo/terraform.tfvars.json"
export TF_CLI_CONFIG_FILE="$tmp/rc" TF_IN_AUTOMATION=1 TF_INPUT=0 CHECKPOINT_DISABLE=1
export HTTPS_PROXY=http://127.0.0.1:9 HTTP_PROXY=http://127.0.0.1:9 NO_PROXY=
export GOOGLE_OAUTH_ACCESS_TOKEN=fake
for r in installation repo firebase; do
  (
    cd "$tmp/gcp/roots/$r"
    sed -i.bak '/backend "gcs" {}/d' main.tf
    terraform init -backend=false >/dev/null
    terraform plan -out=plan.bin >/dev/null
    terraform show -json plan.bin | jq -c '{format_version,terraform_version,resource_changes}' > "$repo/internal/infra/tf/testdata/golden/$r.plan.json"
  )
done
echo "wrote $repo/internal/infra/tf/testdata/golden/*.plan.json"
